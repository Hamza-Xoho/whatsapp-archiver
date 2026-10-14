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

// runFixtureTool runs `scrub <in> <out>` or `replay <events-dir> [--data <dir>] [--own <jid>] [--own-lid <jid>]`.
// Neither opens a WhatsApp session.
func runFixtureTool(cmd string, args []string, out, errOut io.Writer) int {
	if cmd == "scrub" {
		if len(args) != 2 {
			_, _ = fmt.Fprintln(errOut, "usage: archiver scrub <recorded-dir> <scrubbed-dir>")
			return 2
		}
		if err := ingest.Scrub(args[0], args[1]); err != nil {
			_, _ = fmt.Fprintln(errOut, err)
			return 1
		}
		_, _ = fmt.Fprintf(out, "scrubbed %s into %s\n", args[0], args[1])
		return 0
	}
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(errOut)
	data := fs.String("data", "./data", "data directory for the fresh archive")
	// History messages sent by this account are attributed to these IDs; recorded fixtures use the placeholders.
	own := fs.String("own", "10000000000@s.whatsapp.net", "own phone-number JID for history parsing")
	ownLID := fs.String("own-lid", "10000000000@lid", "own LID for history parsing")
	err := fs.Parse(args)
	dir := fs.Arg(0)
	if err == nil && fs.NArg() > 0 {
		err = fs.Parse(fs.Args()[1:]) // flags may follow the folder
	}
	if err != nil || dir == "" || fs.NArg() > 0 {
		_, _ = fmt.Fprintln(errOut, "usage: archiver replay <events-dir> --data <dir>")
		return 2
	}
	n, err := replay(dir, *data, *own, *ownLID, errOut)
	if err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		return 1
	}
	_, _ = fmt.Fprintf(out, "replayed %d events\n", n)
	return 0
}

func replay(dir, data, own, ownLID string, errOut io.Writer) (int, error) {
	pn, err := types.ParseJID(own)
	if err != nil {
		return 0, err
	}
	lid, err := types.ParseJID(ownLID)
	if err != nil {
		return 0, err
	}
	cfg, err := config.Load(filepath.Join(data, "config.yaml"))
	if err != nil {
		return 0, err
	}
	evts, err := ingest.Load(dir)
	if err != nil {
		return 0, err
	}
	path := filepath.Join(data, "archive.db")
	if _, err = os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		return 0, fmt.Errorf("replay needs a fresh data folder: %s exists", path)
	}
	st, err := store.Open(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	aliases := func(jid string) []string { v, _ := st.Aliases(ctx, jid); return v }
	log := slog.New(slog.NewJSONHandler(errOut, nil))
	p := ingest.NewPipeline(st, ingest.NewFilter(cfg.Exclude, aliases), noMedia{}, noHealth{}, log).WithHistoryParser(wa.NewHistoryParser(pn, lid))
	return len(evts), ingest.Replay(ctx, p, evts)
}

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
