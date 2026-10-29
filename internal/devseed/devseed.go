// Package devseed creates deterministic, synthetic archives for UI development.
// It has no WhatsApp client dependency: every archive event enters through ingest.Pipeline.Ingest.
package devseed

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"go.mau.fi/whatsmeow/types/events"
	"whatsapp-archiver/internal/activity"
	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/health"
	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/web"
)

type Options struct {
	DataDir      string
	Chats        int
	Messages     int
	Seed         int64
	SimulateLive float64
	Activity     *activity.Bus
	Serve        bool
}

type Report struct {
	Chats, Messages int
	KindCounts      map[string]int
	Duration        time.Duration
}

func Seed(ctx context.Context, opts Options) (Report, error) {
	started := time.Now()
	if err := validate(opts); err != nil {
		return Report{}, err
	}
	if err := os.MkdirAll(opts.DataDir, 0o700); err != nil {
		return Report{}, fmt.Errorf("create data directory: %w", err)
	}
	if err := writeDevConfig(opts.DataDir); err != nil {
		return Report{}, err
	}
	st, err := store.Open(filepath.Join(opts.DataDir, "archive.db"))
	if err != nil {
		return Report{}, err
	}
	defer func() { _ = st.Close() }()
	if err = st.OptimizeForDevSeed(); err != nil {
		return Report{}, err
	}

	bus := opts.Activity
	if bus == nil && opts.Serve {
		bus = activity.NewBus(500)
	}
	pipe, rules := newPipeline(st, bus)
	g := newGenerator(opts)
	report := Report{Chats: opts.Chats, Messages: opts.Messages, KindCounts: make(map[string]int, len(store.MessageKinds))}
	for i := 0; i < opts.Messages; i++ {
		if err = ctx.Err(); err != nil {
			return Report{}, err
		}
		result, kind, file := g.message(i)
		if file != nil {
			if err = writeMedia(opts.DataDir, result.Media, file); err != nil {
				return Report{}, err
			}
		}
		if err = pipe.Ingest(ctx, result); err != nil {
			return Report{}, fmt.Errorf("ingest message %d: %w", i, err)
		}
		report.KindCounts[kind]++
	}
	if err = g.addGaps(ctx, pipe); err != nil {
		return Report{}, err
	}
	if err = addOutages(ctx, st, g.base); err != nil {
		return Report{}, err
	}

	if opts.SimulateLive > 0 {
		if opts.Serve {
			err = serveAndSimulate(ctx, opts.DataDir, st, rules, bus, pipe, g, opts.Messages, opts.SimulateLive)
		} else {
			err = simulate(ctx, pipe, g, opts.Messages, opts.SimulateLive)
		}
		if err != nil {
			return Report{}, err
		}
	}
	report.Duration = time.Since(started)
	return report, nil
}

func validate(opts Options) error {
	switch {
	case opts.DataDir == "":
		return fmt.Errorf("data directory is required")
	case opts.Chats < 1:
		return fmt.Errorf("chats must be at least 1")
	case opts.Messages < len(store.MessageKinds):
		return fmt.Errorf("messages must be at least %d so every kind is present", len(store.MessageKinds))
	case opts.SimulateLive < 0 || opts.SimulateLive > 10000:
		return fmt.Errorf("simulate-live rate must be between 0 and 10000 messages/s")
	}
	return nil
}

func newPipeline(st *store.Store, bus *activity.Bus) (*ingest.Pipeline, *ingest.RuleSet) {
	rules := ingest.NewRuleSet(nil)
	filter := ingest.NewFilter(rules, nil)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return ingest.NewPipeline(st, filter, seedQueue{}, seedHealth{}, log).WithActivity(bus), rules
}

type seedQueue struct{}

func (seedQueue) Decide(context.Context, *store.Media) error  { return nil }
func (seedQueue) Wake()                                       {}
func (seedQueue) OnRetry(context.Context, *events.MediaRetry) {}

type seedHealth struct{}

func (seedHealth) Seen(time.Time, bool)    {}
func (seedHealth) SetState(string, string) {}
func (seedHealth) WriteFailed(error)       {}
func (seedHealth) Panic(string)            {}

func writeMedia(dataDir string, media *store.Media, body []byte) error {
	dir := filepath.Join(dataDir, "media", "devseed")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create devseed media directory: %w", err)
	}
	name := filepath.Base(media.Path)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return fmt.Errorf("write synthetic media: %w", err)
	}
	sum := sha256.Sum256(body)
	media.Path = filepath.ToSlash(filepath.Join("media", "devseed", name))
	media.SHA256 = fmt.Sprintf("%x", sum)
	media.DeclaredSize = int64(len(body))
	return nil
}

func addOutages(ctx context.Context, st *store.Store, base int64) error {
	periods := []store.ConnectionPeriod{
		{State: "disconnected", CauseClass: "dns", StartedAt: base + 300, EndedAt: base + 420, ErrorCount: 3, LastError: "synthetic DNS outage"},
		{State: "degraded", CauseClass: "keepalive", StartedAt: base + 900, EndedAt: base + 960, ErrorCount: 1, LastError: "synthetic keepalive timeout"},
	}
	for _, period := range periods {
		if err := st.InsertClosedConnectionPeriod(ctx, period); err != nil {
			return fmt.Errorf("insert synthetic outage: %w", err)
		}
	}
	return nil
}

func simulate(ctx context.Context, pipe *ingest.Pipeline, g *generator, offset int, rate float64) error {
	interval := time.Duration(float64(time.Second) / rate)
	if interval < time.Microsecond {
		interval = time.Microsecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for i := offset; ; i++ {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			result, _, _ := g.message(i)
			result.Message.Source = "live"
			result.Received = time.Now()
			result.Message.ReceivedAt = result.Received.Unix()
			if result.Media != nil {
				result.Media = nil // the simulator exercises messages without creating files forever
			}
			if err := pipe.Ingest(ctx, result); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return fmt.Errorf("ingest simulated live message: %w", err)
			}
		}
	}
}

func serveAndSimulate(ctx context.Context, dataDir string, st *store.Store, rules *ingest.RuleSet, bus *activity.Bus, pipe *ingest.Pipeline, g *generator, offset int, rate float64) error {
	cfg, err := config.Load(filepath.Join(dataDir, "config.yaml"))
	if err != nil {
		return err
	}
	cfg.DataDir = dataDir
	liveCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	listening := make(chan struct{})
	serverDone := make(chan error, 1)
	ui := web.New(web.Deps{
		Store: st, Rules: rules, Activity: bus, Config: cfg,
		Health: func() health.Snapshot {
			return health.Snapshot{State: "connected", LastEvent: time.Now(), UpdatedAt: time.Now()}
		},
		Listening: func(string) { close(listening) },
	})
	go func() { serverDone <- ui.Run(liveCtx, cfg.Web.Listen) }()
	select {
	case <-listening:
	case err = <-serverDone:
		return err
	case <-ctx.Done():
		return nil
	}
	simulatorDone := make(chan error, 1)
	go func() { simulatorDone <- simulate(liveCtx, pipe, g, offset, rate) }()
	select {
	case err = <-serverDone:
		cancel()
		return errors.Join(err, <-simulatorDone)
	case err = <-simulatorDone:
		cancel()
		return errors.Join(err, <-serverDone)
	}
}

func writeDevConfig(dataDir string) error {
	path := filepath.Join(dataDir, "config.yaml")
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat dev config: %w", err)
	}
	const config = "web:\n  listen: 127.0.0.1:8090\n"
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		return fmt.Errorf("write dev config: %w", err)
	}
	return nil
}
