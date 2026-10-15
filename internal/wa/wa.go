package wa

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mdp/qrterminal/v3"
	"go.mau.fi/whatsmeow"
	wmstore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types/events"
	_ "modernc.org/sqlite"

	"whatsapp-archiver/internal/config"
)

type clientVersion struct{ version, reason string }

type Client struct {
	client    *whatsmeow.Client
	container *sqlstore.Container
	cfg       *config.Config
	log       *slog.Logger
	ver       atomic.Pointer[clientVersion] // version and reason as one pair: the monitor reads while the reconnect loop refreshes
	closeOnce sync.Once
	sock      socket
	refresh   func(context.Context) (changed bool, err error)
	sleep     func(context.Context, time.Duration) error
}

// socket is the connection boundary the reconnect loop drives; *whatsmeow.Client satisfies it.
type socket interface {
	ConnectContext(context.Context) error
	Disconnect()
}

func Open(ctx context.Context, cfg *config.Config, log *slog.Logger) (*Client, error) {
	if log == nil {
		log = slog.Default()
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	wal := newWALogger(log)
	container, err := sqlstore.New(ctx, "sqlite", sessionDSN(filepath.Join(cfg.DataDir, "session.db")), wal.Sub("Database"))
	if err != nil {
		return nil, fmt.Errorf("open session: %w", err)
	}
	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		_ = container.Close()
		return nil, fmt.Errorf("load device: %w", err)
	}
	wmstore.DeviceProps = historyProps(cfg.History.DaysLimit, cfg.History.SizeMBLimit)
	version, reason, err := chooseVersion(ctx, cfg)
	if err != nil {
		_ = container.Close()
		return nil, err
	}
	parsed, err := parseVersion(version)
	if err != nil {
		_ = container.Close()
		return nil, err
	}
	wmstore.SetWAVersion(parsed)
	inner := whatsmeow.NewClient(device, wal.Sub("Client"))
	inner.AutomaticMessageRerequestFromPhone = true
	inner.SynchronousAck = true
	c := &Client{client: inner, container: container, cfg: cfg, log: log, sock: inner, sleep: sleepCtx}
	c.ver.Store(&clientVersion{version, reason})
	c.refresh = c.refreshVersion
	return c, nil
}

func chooseVersion(ctx context.Context, cfg *config.Config) (string, string, error) {
	built := wmstore.GetWAVersion().String()
	latest := ""
	var latestErr error
	if cfg.WA.CheckLatestVersion {
		v, err := whatsmeow.GetLatestVersion(ctx, nil)
		latestErr = err
		if v != nil {
			latest = v.String()
		}
	}
	use, reason := decideVersion(built, latest, cfg.WA.VersionOverride, latestErr, cfg.WA.CheckLatestVersion)
	return use, reason, nil
}
func (c *Client) Paired() bool { return c.client.Store.ID != nil }
func (c *Client) VersionInUse() string {
	if v := c.ver.Load(); v != nil {
		return v.version
	}
	return ""
}
func (c *Client) VersionReason() string {
	if v := c.ver.Load(); v != nil {
		return v.reason
	}
	return ""
}
func (c *Client) Close() error {
	var err error
	c.closeOnce.Do(func() { c.client.Disconnect(); err = c.container.Close() })
	return err
}

func (c *Client) Run(ctx context.Context, h func(any), pairing bool) error {
	if pairing && c.Paired() {
		return fmt.Errorf("already paired")
	}
	if !pairing && !c.Paired() {
		return fmt.Errorf("not paired")
	}
	var qr <-chan whatsmeow.QRChannelItem
	var err error
	if pairing {
		qr, err = c.client.GetQRChannel(ctx)
		if err != nil {
			return fmt.Errorf("start QR pairing: %w", err)
		}
	}
	if qr != nil {
		go printQR(ctx, qr)
	}
	ctl := make(chan any, 8)
	c.client.AddEventHandler(func(evt any) {
		h(evt)
		if line := pairLine(evt); pairing && line != "" {
			_, _ = fmt.Fprintln(os.Stdout, line)
		}
		forwardControl(ctl, evt)
	})
	return c.reconnect(ctx, ctl)
}

// forwardControl hands the reconnect loop the events its policy acts on, never blocking the handler.
func forwardControl(ctl chan<- any, evt any) {
	switch evt.(type) {
	case *events.Connected, *events.ClientOutdated, *events.TemporaryBan, *events.LoggedOut, *events.StreamReplaced,
		*events.ConnectFailure, *events.CATRefreshError:
		select {
		case ctl <- evt:
		default:
		}
	}
}

// reconnect feeds control events to the reconnect policy and carries out its actions.
// It returns nil once ctx is cancelled.
func (c *Client) reconnect(ctx context.Context, ctl <-chan any) error {
	defer c.sock.Disconnect()
	var p policy
	var in any = startInput{}
	for {
		a := p.next(in, time.Now())
		in = nil
		if a.stop {
			c.log.Error("logged out; reconnecting stopped")
			<-ctx.Done()
			return nil
		}
		if a.disconnect {
			c.sock.Disconnect()
		}
		if a.wait > 0 {
			c.log.Warn("reconnect waiting", "wait", a.wait)
			if c.sleep(ctx, a.wait) != nil {
				return nil
			}
		}
		switch {
		case a.refresh:
			changed, err := c.refresh(ctx)
			if err != nil {
				c.log.Error("version refresh failed", "error", err)
			}
			in = versionRefreshed{changed: changed}
		case a.connect:
			if err := c.sock.ConnectContext(ctx); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				c.log.Error("connect failed", "error", err)
				in = connectFailed{}
			}
		}
		if in == nil {
			select {
			case <-ctx.Done():
				return nil
			case in = <-ctl:
			}
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (c *Client) refreshVersion(ctx context.Context) (bool, error) {
	use, reason, err := chooseVersion(ctx, c.cfg)
	if err != nil {
		return false, err
	}
	parsed, err := parseVersion(use)
	if err != nil {
		return false, err
	}
	wmstore.SetWAVersion(parsed)
	changed := use != c.VersionInUse()
	c.ver.Store(&clientVersion{use, reason})
	return changed, nil
}
func printQR(ctx context.Context, ch <-chan whatsmeow.QRChannelItem) {
	for {
		select {
		case <-ctx.Done():
			return
		case item, ok := <-ch:
			if !ok {
				return
			}
			if item.Event == whatsmeow.QRChannelEventCode {
				qrterminal.GenerateHalfBlock(item.Code, qrterminal.L, os.Stdout)
			} else if line := pairLine(item); line != "" {
				_, _ = fmt.Fprintln(os.Stdout, line)
			}
		}
	}
}
