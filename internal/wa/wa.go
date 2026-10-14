package wa

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mdp/qrterminal/v3"
	"go.mau.fi/whatsmeow"
	wmstore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types/events"
	_ "modernc.org/sqlite"

	"whatsapp-archiver/internal/config"
)

type Client struct {
	client        *whatsmeow.Client
	container     *sqlstore.Container
	cfg           *config.Config
	log           *slog.Logger
	version       string
	versionReason string
	closeOnce     sync.Once
}

func Open(ctx context.Context, cfg *config.Config, log *slog.Logger) (*Client, error) {
	if log == nil {
		log = slog.Default()
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	container, err := sqlstore.New(ctx, "sqlite", sessionDSN(filepath.Join(cfg.DataDir, "session.db")), nil)
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
	inner := whatsmeow.NewClient(device, nil)
	inner.AutomaticMessageRerequestFromPhone = true
	inner.SynchronousAck = true
	return &Client{client: inner, container: container, cfg: cfg, log: log, version: version, versionReason: reason}, nil
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
func (c *Client) Paired() bool          { return c.client.Store.ID != nil }
func (c *Client) VersionInUse() string  { return c.version }
func (c *Client) VersionReason() string { return c.versionReason }
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
	outdated := make(chan struct{}, 1)
	c.client.AddEventHandler(func(evt any) {
		h(evt)
		if _, ok := evt.(*events.ClientOutdated); ok {
			select {
			case outdated <- struct{}{}:
			default:
			}
		}
	})
	for {
		if err = c.client.ConnectContext(ctx); err != nil && !errors.Is(err, context.Canceled) {
			c.log.Error("connect failed", "error", err)
		}
		if pairing && qr != nil {
			go printQR(ctx, qr)
		}
		select {
		case <-ctx.Done():
			c.client.Disconnect()
			return nil
		case <-outdated:
			c.client.Disconnect()
			if err = c.refreshVersion(ctx); err != nil {
				c.log.Error("version refresh failed", "error", err)
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(30 * time.Minute):
				}
			}
		case <-time.After(5 * time.Second):
			if c.client.IsConnected() {
				<-ctx.Done()
				c.client.Disconnect()
				return nil
			}
		}
	}
}

func (c *Client) refreshVersion(ctx context.Context) error {
	use, reason, err := chooseVersion(ctx, c.cfg)
	if err != nil {
		return err
	}
	parsed, err := parseVersion(use)
	if err != nil {
		return err
	}
	wmstore.SetWAVersion(parsed)
	c.version, c.versionReason = use, reason
	return nil
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
			if item.Event == "code" {
				qrterminal.GenerateHalfBlock(item.Code, qrterminal.L, os.Stdout)
			}
		}
	}
}
