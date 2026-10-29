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
	"whatsapp-archiver/internal/activity"
	"whatsapp-archiver/internal/backup"
	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/health"
	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/media"
	"whatsapp-archiver/internal/network"
	"whatsapp-archiver/internal/sdnotify"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/wa"
	"whatsapp-archiver/internal/web"
)

// version is set at link time by the Makefile (-ldflags "-X main.version=..."); "dev" marks an unstamped build.
var version = "dev"

const whatsmeowVersion = "v0.0.0-20261013142727-4cf8bc8e0aeb"

// exitNotPaired tells systemd not to restart (RestartPreventExitStatus in deploy/archiver.service). Since phase 2
// (FR-1) `run` no longer returns it: unpaired, it stays up and pairs from the dashboard.
const exitNotPaired = 3

type systemClock struct{}

func (systemClock) Now() time.Time                         { return time.Now() }
func (systemClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Test seams: tests stop serve by cancelling serveContext (the signal) and keep it off the network with testSocket.
var (
	serveContext = context.Background
	testSocket   wa.Socket
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
func run(args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(errOut, "usage: archiver <pair|run|status|chats|backup|rebuild-derived|scrub|replay|version>")
		_, _ = fmt.Fprintln(errOut, "  rebuild-derived --data <dir>: recreate chat_stats and the search index; stop the service first (it takes the archive exclusively)")
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
		if !paired { // stay up: the dashboard pairs, and nothing connects until it is asked to
			_, _ = fmt.Fprintln(errOut, notPairedHint)
			alertNotPaired(cfg, errOut)
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
	case "rebuild-derived":
		err = rebuildDerived(cfg, out)
	case "backup":
		var r backup.Report
		if err = applyArchiveKeep(cfg); err == nil {
			r, err = backup.Run(context.Background(), cfg)
		}
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
	sigCtx, stop := signal.NotifyContext(serveContext(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// ctx ends on SIGTERM and also when client.Run returns by itself; the pipeline and every loop stop on it.
	ctx, cancel := context.WithCancel(sigCtx)
	defer cancel()
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	st, err := store.Open(filepath.Join(cfg.DataDir, "archive.db"))
	if err != nil {
		return err
	}
	if err = st.RecordProcessStart(ctx, time.Now().Unix()); err != nil {
		_ = st.Close()
		return err
	}
	// The first start of this build imports config.yaml's exclusions, media rules, alert thresholds and backup
	// schedule into the archive; from then on the archive's copy is the live one.
	if imported, err := st.ImportConfigOnce(ctx, cfg); err != nil {
		_ = st.Close()
		return err
	} else if imported {
		log.Info("config.yaml settings imported into the archive", "exclusions", len(cfg.Exclude))
	}
	chatRules, err := st.ChatRules(ctx)
	if err != nil {
		_ = st.Close()
		return err
	}
	rules := ingest.NewRuleSet(chatRules)
	aliases := storeAliases(st)
	warnIgnoredExclusions(log, cfg.Exclude, ingest.NewFilter(rules, aliases))
	seen, err := ingest.NewSeenTracker(ctx, st)
	if err != nil {
		_ = st.Close()
		return err
	}
	live := liveSettings{st: st, cfg: cfg, log: log, rules: rules, aliases: aliases}
	httpClient, err := network.NewHTTPClient(ctx, cfg.Network.FallbackDNS, st)
	if err != nil {
		_ = st.Close()
		return fmt.Errorf("configure network fallback: %w", err)
	}
	client, err := wa.Open(ctx, cfg, log)
	if err != nil {
		_ = st.Close()
		return err
	}
	if testSocket != nil {
		client.UseSocket(testSocket)
	}
	client.UseHTTPClient(httpClient)
	bus := activity.NewBus(500)
	var notifier health.Notifier
	if cfg.Alerts.Ntfy != nil {
		notifier = health.NewNtfyWithClient(cfg.Alerts.Ntfy, httpClient)
	}
	mon := health.NewMonitor(cfg, notifier, systemClock{}, health.DiskFree, client.VersionInUse).WithVersionReason(client.VersionReason).WithLogger(log).WithThresholds(live.alerts).WithActivity(bus).WithConnectionLedger(st).WithAlertStore(st).WithHeartbeatClient(httpClient)
	client.WithConnectionHealth(mon)
	mediaConnected := func() bool { return mon.Status().State == "connected" }
	queue := media.NewQueue(cfg.DataDir, live.media, live.chatMedia, st, client, systemClock{}, log, mon.Panic).WithConnected(mediaConnected).WithActivity(bus)
	filter := ingest.NewFilter(rules, aliases)
	pipeline := ingest.NewPipeline(st, filter, queue, mon, log).WithHistoryParser(client.HistoryParser()).WithVoteDecryptor(client.VoteDecryptor()).WithContext(ctx).WithSeen(seen).WithActivity(bus)
	handler := pipeline.Handle
	if recordDir != "" {
		handler = ingest.NewRecorder(recordDir).WithFilter(filter).Wrap(handler)
	}
	contacts := newContactSync(client.ContactNames(), st, log)
	handler = withContactSync(contacts, handler)
	handlers := &inFlight{}
	handler = handlers.wrap(handler)
	watchdog := serviceWatchdog{writer: st, handlers: handlers, now: time.Now, notify: sdnotify.Notify}
	// `run` connects through the pairer: capture at once when paired, else nothing until the dashboard starts
	// pairing, which carries on as capture. Media downloads and backups wait until the device is paired.
	// `pair` pairs in the terminal as before, and the dashboard then offers no pairing of its own.
	pairer := wa.NewPairer(client)
	connect := func(ctx context.Context) error { return pairer.Run(ctx, handler) }
	ready := pairer.Ready()
	var browser web.Pairer = &browserPairer{p: pairer}
	if pairing {
		_, _ = fmt.Fprintln(out, "Scan the QR code from WhatsApp → Linked Devices.")
		connect = func(ctx context.Context) error { return client.Run(ctx, handler, true) }
		now := make(chan struct{})
		close(now)
		ready, browser = now, nil // the loops start at once, as before phase 2
	}
	ui := web.New(web.Deps{Store: st, Rules: rules, Health: mon.Status, Activity: bus, Config: cfg, Notifier: notifier, Pairer: browser,
		Listening: func(addr string) { log.Info("dashboard listening", "listen", addr) }})
	loops := []func(context.Context){
		watchdog.run,
		afterPaired(ready, queue.Run),
		mon.Run,
		contacts.Run, // idle until a connect or contact event asks for a sync
		afterPaired(ready, func(ctx context.Context) {
			backup.Schedule(ctx, cfg, systemClock{}, publishBackup(bus, mon.BackupResult), live.backup)
		}),
		// The dashboard stops with the other loops. If it cannot listen (the port is taken), capture carries on.
		func(ctx context.Context) {
			if err := ui.Run(ctx, cfg.Web.Listen); err != nil {
				log.Error("dashboard stopped; capture continues", "err", err)
			}
		},
	}
	if err := sdnotify.Notify("READY=1"); err != nil {
		_ = client.Close()
		_ = st.Close()
		return fmt.Errorf("notify systemd ready: %w", err)
	}
	// Shutdown order: cancel ctx, client.Run returns, the loops are joined, the event handler in flight is waited for
	// (at most handlerDrainBound), then the client and the store are closed.
	return runService(ctx, cancel, connect, loops, handlers.drain, client.Close, st.Close)
}

// publishBackup wraps the backup result callback so each result also reaches the live feed: the outcome and the
// size, never the error text (it can name a path).
func publishBackup(bus *activity.Bus, next func(backup.Report, error)) func(backup.Report, error) {
	return func(r backup.Report, err error) {
		e := activity.Event{At: time.Now(), Kind: "backup", Detail: fmt.Sprintf("snapshot done · %d bytes", r.Bytes)}
		if err != nil {
			e.Detail = "failed"
		}
		bus.Publish(e)
		next(r, err)
	}
}

// storeAliases is the exclusion filter's alias lookup: the other forms (LID or phone) of a chat known to the store.
// A failure is marked as a read, so its alert is titled as one.
func storeAliases(st *store.Store) func(string) ([]string, error) {
	return func(jid string) ([]string, error) {
		aliases, err := st.Aliases(context.Background(), jid)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", health.ErrRead, err)
		}
		return aliases, nil
	}
}

const notPairedHint = "not paired: open the dashboard through the SSH tunnel and pair (or stop the service and run `archiver pair` in a terminal)"

// notPairedBody is the not-paired alert's text.
const notPairedBody = "Open the dashboard through the SSH tunnel and pair"

// alertNotPaired sends one ntfy alert when configured; a send failure is reported, not fatal.
func alertNotPaired(cfg *config.Config, errOut io.Writer) {
	if cfg.Alerts.Ntfy == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	httpClient, err := network.NewHTTPClient(ctx, cfg.Network.FallbackDNS, nil)
	if err == nil {
		err = health.NewNtfyWithClient(cfg.Alerts.Ntfy, httpClient).Notify(ctx, "WhatsApp Archiver is not paired", notPairedBody)
	}
	if err != nil {
		_, _ = fmt.Fprintln(errOut, "ntfy alert failed:", err)
	}
}
