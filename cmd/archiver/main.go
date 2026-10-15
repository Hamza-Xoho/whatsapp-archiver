package main

import (
	"context"
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

// exitNotPaired tells systemd not to restart (RestartPreventExitStatus in deploy/archiver.service).
const exitNotPaired = 3

type systemClock struct{}

func (systemClock) Now() time.Time                         { return time.Now() }
func (systemClock) After(d time.Duration) <-chan time.Time { return time.After(d) }
func main()                                                { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
func run(args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(errOut, "usage: archiver <pair|run|status|chats|backup|scrub|replay|version>")
		return 2
	}
	cmd := args[0]
	if cmd == "version" {
		_, _ = fmt.Fprintf(out, "whatsapp-archiver %s\nwhatsmeow %s\n", version, whatsmeowVersion)
		return 0
	}
	if cmd == "scrub" || cmd == "replay" { // offline fixture tools: no WhatsApp session, own arguments
		return runFixtureTool(cmd, args[1:], out, errOut)
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
		paired, err := wa.IsPaired(context.Background(), *data)
		if err != nil {
			_, _ = fmt.Fprintln(errOut, err)
			return 1
		}
		if !paired {
			_, _ = fmt.Fprintln(errOut, notPairedHint)
			alertNotPaired(cfg, errOut)
			return exitNotPaired
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
	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// ctx ends on SIGTERM and also when client.Run returns by itself; the pipeline and every loop stop on it.
	ctx, cancel := context.WithCancel(sigCtx)
	defer cancel()
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	st, err := store.Open(filepath.Join(cfg.DataDir, "archive.db"))
	if err != nil {
		return err
	}
	client, err := wa.Open(ctx, cfg, log)
	if err != nil {
		_ = st.Close()
		return err
	}
	var notifier health.Notifier
	if cfg.Alerts.Ntfy != nil {
		notifier = health.NewNtfy(cfg.Alerts.Ntfy)
	}
	mon := health.NewMonitor(cfg, notifier, systemClock{}, health.DiskFree, client.VersionInUse).WithVersionReason(client.VersionReason).WithLogger(log)
	aliases := func(jid string) ([]string, error) { return st.Aliases(context.Background(), jid) }
	queue := media.NewQueue(cfg.DataDir, cfg.Media, st, client, systemClock{}, log, mon.Panic)
	pipeline := ingest.NewPipeline(st, ingest.NewFilter(cfg.Exclude, aliases), queue, mon, log).WithHistoryParser(client.HistoryParser()).WithContext(ctx)
	handler := pipeline.Handle
	if recordDir != "" {
		handler = ingest.NewRecorder(recordDir).Wrap(handler)
	}
	if pairing {
		_, _ = fmt.Fprintln(out, "Scan the QR code from WhatsApp → Linked Devices.")
	}
	loops := []func(context.Context){
		queue.Run,
		mon.Run,
		func(ctx context.Context) { backup.Schedule(ctx, cfg, systemClock{}, mon.BackupResult) },
	}
	// Shutdown order: cancel ctx, client.Run returns, the loops are joined, then the client and the store are closed.
	return runService(ctx, cancel, func(ctx context.Context) error { return client.Run(ctx, handler, pairing) }, loops, client.Close, st.Close)
}

const notPairedHint = "not paired: run `archiver pair` in a terminal first"

// alertNotPaired sends one ntfy alert when configured; a send failure is reported, not fatal.
func alertNotPaired(cfg *config.Config, errOut io.Writer) {
	if cfg.Alerts.Ntfy == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := health.NewNtfy(cfg.Alerts.Ntfy).Notify(ctx, "WhatsApp Archiver is not paired", notPairedHint); err != nil {
		_, _ = fmt.Fprintln(errOut, "ntfy alert failed:", err)
	}
}
