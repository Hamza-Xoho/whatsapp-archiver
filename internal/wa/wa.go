package wa

import (
	"context"
	"fmt"
	"io"
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
	sock      Socket
	refresh   func(context.Context) (changed bool, err error)
	sleep     func(context.Context, time.Duration) error
	// download fetches a history chunk; historyTimeout bounds it. Tests replace both.
	download       historyDownloader
	historyTimeout time.Duration
	paired         atomic.Bool
	pairedOnce     sync.Once
	// qrChannel is GetQRChannel; tests replace it with a fake channel.
	qrChannel func(context.Context) (<-chan whatsmeow.QRChannelItem, error)
}

// Socket is the connection boundary the reconnect loop drives; *whatsmeow.Client satisfies it.
type Socket interface {
	ConnectContext(context.Context) error
	Disconnect()
}

// UseSocket replaces the WhatsApp connection with s. Tests outside this package use it so that nothing reaches
// WhatsApp; the service never calls it.
func (c *Client) UseSocket(s Socket) { c.sock = s }

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
	inner.EnableDecryptedEventBuffer = true
	inner.ManualHistorySyncDownload = true // historyHandler downloads, so the notification is acked only after its chunk is stored
	c := &Client{client: inner, container: container, cfg: cfg, log: log, sock: inner, sleep: sleepCtx,
		download: liveHistoryDownloader(inner), historyTimeout: historyDownloadTimeout}
	c.ver.Store(&clientVersion{version, reason})
	c.refresh = c.refreshVersion
	c.qrChannel = inner.GetQRChannel
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

// Paired reports whether the device holds a login. The web server asks from its own goroutines while whatsmeow
// writes Store.ID in handlePair, so the device is read once, on the first call (Run and the Pairer make it before
// any connect), and the answer is then kept in an atomic, set on PairSuccess and cleared on LoggedOut.
func (c *Client) Paired() bool {
	c.pairedOnce.Do(func() { c.paired.Store(c.client.Store.ID != nil) })
	return c.paired.Load()
}

// setPaired records a pairing or a logout.
func (c *Client) setPaired(v bool) {
	c.pairedOnce.Do(func() {})
	c.paired.Store(v)
}
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

// Run captures until ctx ends. With pairing, it first shows the QR codes in the terminal (`archiver pair`).
func (c *Client) Run(ctx context.Context, h func(any) bool, pairing bool) error {
	return c.run(ctx, h, pairing, printQR)
}

// run is Run with the QR codes handed to show, which runs in its own goroutine and must return when ctx ends or the
// channel closes. The QR channel is taken before the first connect, as whatsmeow requires (qrchan.go:217).
// A pairing that never completed removes its event handler on return, so a later attempt does not add a second
// one; the socket is already disconnected then. Capture keeps its handler to the end.
func (c *Client) run(ctx context.Context, h func(any) bool, pairing bool, show func(context.Context, <-chan whatsmeow.QRChannelItem)) error {
	if pairing && c.Paired() {
		return fmt.Errorf("already paired")
	}
	if !pairing && !c.Paired() {
		return fmt.Errorf("not paired")
	}
	if pairing {
		qr, err := c.qrChannel(ctx)
		if err != nil {
			return fmt.Errorf("start QR pairing: %w", err)
		}
		go show(ctx, qr)
	}
	ctl := make(chan any, 8)
	id := c.client.AddEventHandlerWithSuccessStatus(c.trackPairing(eventHandler(c.historyHandler(ctx, h), pairing, os.Stdout, ctl)))
	err := c.reconnect(ctx, ctl)
	if pairing && !c.Paired() {
		c.client.RemoveEventHandler(id)
	}
	return err
}

// eventHandler adapts h to whatsmeow. It returns h's verdict, so whatsmeow acknowledges an event only when h reports
// it stored; the pairing line and the control events are handled whatever the verdict.
func eventHandler(h func(any) bool, pairing bool, out io.Writer, ctl chan<- any) whatsmeow.EventHandlerWithSuccessStatus {
	return func(evt any) bool {
		ok := h(evt)
		if line := pairLine(evt); pairing && line != "" {
			_, _ = fmt.Fprintln(out, line)
		}
		forwardControl(ctl, evt)
		return ok
	}
}

// trackPairing keeps Paired current: true after PairSuccess, false after LoggedOut (whatsmeow deletes the device).
func (c *Client) trackPairing(next whatsmeow.EventHandlerWithSuccessStatus) whatsmeow.EventHandlerWithSuccessStatus {
	return func(evt any) bool {
		switch evt.(type) {
		case *events.PairSuccess:
			c.setPaired(true)
		case *events.LoggedOut:
			c.setPaired(false)
		}
		return next(evt)
	}
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
