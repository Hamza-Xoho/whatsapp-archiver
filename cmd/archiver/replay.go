package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/wa"
)

// runFixtureTool runs `scrub [--key-file <path>] <in> <out>` or `replay <events-dir> [--data <dir>] [--own <jid>] [--own-lid <jid>]`.
// Neither opens a WhatsApp session.
func runFixtureTool(cmd string, args []string, out, errOut io.Writer) int {
	if cmd == "scrub" {
		return runScrub(args, out, errOut)
	}
	flags := flag.NewFlagSet(cmd, flag.ContinueOnError)
	flags.SetOutput(errOut)
	data := flags.String("data", "./data", "data directory for the fresh archive")
	// History messages sent by this account are attributed to these IDs; recorded fixtures use the placeholders.
	own := flags.String("own", "10000000000@s.whatsapp.net", "own phone-number JID for history parsing")
	ownLID := flags.String("own-lid", "10000000000@lid", "own LID for history parsing")
	dump := flags.Bool("dump", false, "print the raw-table dump instead of writing <data>/raw.sql")
	err := flags.Parse(args)
	dir := flags.Arg(0)
	if err == nil && flags.NArg() > 0 {
		err = flags.Parse(flags.Args()[1:]) // flags may follow the folder
	}
	if err != nil || dir == "" || flags.NArg() > 0 {
		_, _ = fmt.Fprintln(errOut, "usage: archiver replay <events-dir> --data <dir>")
		return 2
	}
	n, raw, err := replay(dir, *data, *own, *ownLID, errOut)
	if err == nil && *dump {
		_, _ = fmt.Fprintf(errOut, "replayed %d events\n", n)
		_, _ = fmt.Fprint(out, raw)
		return 0
	}
	path := filepath.Join(*data, "raw.sql")
	if err == nil {
		err = os.WriteFile(path, []byte(raw), 0o600)
	}
	if err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		return 1
	}
	_, _ = fmt.Fprintf(out, "replayed %d events\nraw dump: %s\n", n, path)
	return 0
}

// runScrub runs `scrub [--key-file <path>] <in> <out>`. The fakes are keyed with the key file, by default
// <user config dir>/archiver/scrub.key, created on first use; it must never be committed.
func runScrub(args []string, out, errOut io.Writer) int {
	flags := flag.NewFlagSet("scrub", flag.ContinueOnError)
	flags.SetOutput(errOut)
	keyFile := flags.String("key-file", "", "secret key of the fakes (default <user config dir>/archiver/scrub.key)")
	if err := flags.Parse(args); err != nil || flags.NArg() != 2 {
		_, _ = fmt.Fprintln(errOut, "usage: archiver scrub [--key-file <path>] <recorded-dir> <scrubbed-dir>")
		return 2
	}
	in, dst := flags.Arg(0), flags.Arg(1)
	err := func() error {
		if *keyFile == "" {
			dir, err := os.UserConfigDir()
			if err != nil {
				return err
			}
			*keyFile = filepath.Join(dir, "archiver", "scrub.key")
		}
		key, err := ingest.LoadScrubKey(*keyFile)
		if err != nil {
			return err
		}
		return ingest.Scrub(in, dst, key)
	}()
	if err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		return 1
	}
	_, _ = fmt.Fprintf(out, "scrubbed %s into %s (key %s)\n", in, dst, *keyFile)
	return 0
}

// replay loads the events in dir and feeds them through a fresh pipeline into <data>/archive.db; it returns the
// number of events and the DumpRaw of the result.
func replay(dir, data, own, ownLID string, errOut io.Writer) (int, string, error) {
	pn, err := types.ParseJID(own)
	if err != nil {
		return 0, "", err
	}
	lid, err := types.ParseJID(ownLID)
	if err != nil {
		return 0, "", err
	}
	cfg, err := config.Load(filepath.Join(data, "config.yaml"))
	if err != nil {
		return 0, "", err
	}
	evts, err := ingest.Load(dir)
	if err != nil {
		return 0, "", err
	}
	path := filepath.Join(data, "archive.db")
	if _, err = os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		return 0, "", fmt.Errorf("replay needs a fresh data folder: %s exists", path)
	}
	st, err := store.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	aliases := func(jid string) ([]string, error) { return st.Aliases(ctx, jid) }
	log := slog.New(slog.NewJSONHandler(errOut, nil))
	p := ingest.NewPipeline(replayWriter(st), ingest.NewFilter(cfg.Exclude, aliases), noMedia{}, noHealth{}, log).WithHistoryParser(wa.NewHistoryParser(pn, lid))
	if err = ingest.Replay(ctx, p, evts); err != nil {
		return 0, "", err
	}
	raw, err := ingest.DumpRaw(st)
	return len(evts), raw, err
}

// replayWriter is what the replay pipeline writes to; tests replace it with a failing store.
var replayWriter = func(st *store.Store) ingest.Writer { return st }

// noMedia drops media jobs: a replay downloads nothing.
type noMedia struct{}

func (noMedia) Enqueue(context.Context, store.Media) error  { return nil }
func (noMedia) OnRetry(context.Context, *events.MediaRetry) {}

// noHealth ignores health reports: a replay has no connection to monitor.
type noHealth struct{}

func (noHealth) Seen(time.Time, bool)    {}
func (noHealth) SetState(string, string) {}
func (noHealth) WriteFailed(error)       {}
func (noHealth) Panic(string)            {}
