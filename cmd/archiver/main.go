package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
	"whatsapp-archiver/internal/backup"
	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/health"
	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/media"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/wa"
)

const version = "0.1.0-phase0"
const whatsmeowVersion = "v0.0.0-20261013142727-4cf8bc8e0aeb"

type systemClock struct{}

func (systemClock) Now() time.Time                         { return time.Now() }
func (systemClock) After(d time.Duration) <-chan time.Time { return time.After(d) }
func main()                                                { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
func run(args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(errOut, "usage: archiver <pair|run|status|chats|backup|version>")
		return 2
	}
	cmd := args[0]
	if cmd == "version" {
		_, _ = fmt.Fprintf(out, "whatsapp-archiver %s\nwhatsmeow %s\n", version, whatsmeowVersion)
		return 0
	}
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(errOut)
	data := fs.String("data", "./data", "data directory")
	record := fs.String("record", "", "record supported events")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	cfg, err := config.Load(filepath.Join(*data, "config.yaml"))
	if err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		return 1
	}
	cfg.DataDir = *data
	switch cmd {
	case "run":
		if _, err = os.Stat(filepath.Join(*data, "session.db")); err != nil {
			_, _ = fmt.Fprintln(errOut, "not paired; run `archiver pair` in a terminal first")
			return 1
		}
		fallthrough
	case "pair":
		if err = serve(cfg, cmd == "pair", *record, out); err != nil {
			_, _ = fmt.Fprintln(errOut, err)
			return 1
		}
	case "status":
		err = printStatus(cfg, out)
	case "chats":
		err = printChats(cfg, out)
	case "backup":
		var r backup.Report
		r, err = backup.Run(context.Background(), cfg)
		if err == nil {
			_, _ = fmt.Fprintf(out, "snapshot %s (%d bytes, %s)\n", r.Snapshot, r.Bytes, r.Duration)
		}
	default:
		_, _ = fmt.Fprintln(errOut, "unknown command:", cmd)
		return 2
	}
	if err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		return 1
	}
	return 0
}
func serve(cfg *config.Config, pairing bool, recordDir string, out io.Writer) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	st, err := store.Open(filepath.Join(cfg.DataDir, "archive.db"))
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	client, err := wa.Open(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	var notifier health.Notifier
	if cfg.Alerts.Ntfy != nil {
		notifier = health.NewNtfy(cfg.Alerts.Ntfy)
	}
	mon := health.NewMonitor(cfg, notifier, systemClock{}, health.DiskFree, client.VersionInUse)
	aliases := func(jid string) []string { v, _ := st.Aliases(context.Background(), jid); return v }
	queue := media.NewQueue(cfg.DataDir, cfg.Media, st, client, systemClock{}, log)
	pipeline := ingest.NewPipeline(st, ingest.NewFilter(cfg.Exclude, aliases), queue, mon, log)
	handler := pipeline.Handle
	if recordDir != "" {
		handler = ingest.NewRecorder(recordDir).Wrap(handler)
	}
	go queue.Run(ctx)
	go mon.Run(ctx)
	go backup.Schedule(ctx, cfg, systemClock{}, mon.BackupResult)
	if pairing {
		_, _ = fmt.Fprintln(out, "Scan the QR code from WhatsApp → Linked Devices.")
	}
	err = client.Run(ctx, handler, pairing)
	if pairing && err == nil {
		_, _ = fmt.Fprintln(out, "Paired. Capture is running.")
	}
	return err
}
func printStatus(cfg *config.Config, out io.Writer) error {
	st, err := store.Open(filepath.Join(cfg.DataDir, "archive.db"))
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	var snap health.Snapshot
	if data, e := os.ReadFile(filepath.Join(cfg.DataDir, "health.json")); e == nil {
		_ = json.Unmarshal(data, &snap)
	}
	coverage, _ := st.HistoryCoverage(context.Background())
	stats, err := st.ChatStats(context.Background())
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "state: %s\nlast event: %s\nhistory chunks: %d (progress %d)\n", snap.State, snap.LastEvent, coverage.Chunks, coverage.LastProgress)
	for _, s := range stats {
		_, _ = fmt.Fprintf(out, "%s\t%s\t%d messages\toldest %d\tnewest %d\tmedia failed %d\topen gaps %d\n", s.CanonicalJID, s.Name, s.MessageCount, s.Oldest, s.Newest, s.MediaFailed, s.OpenGaps)
	}
	return nil
}
func printChats(cfg *config.Config, out io.Writer) error {
	st, err := store.Open(filepath.Join(cfg.DataDir, "archive.db"))
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	stats, err := st.ChatStats(context.Background())
	if err != nil {
		return err
	}
	excluded := map[string]bool{}
	for _, v := range cfg.Exclude {
		excluded[v] = true
	}
	for _, s := range stats {
		_, _ = fmt.Fprintf(out, "%s\t%s\t%d\texcluded=%v\n", s.CanonicalJID, s.Name, s.MessageCount, excluded[s.CanonicalJID])
	}
	return nil
}
