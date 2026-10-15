package main

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/health"
	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/store"
)

// closedStore is an archive whose connections are closed, so every read fails.
func closedStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "archive.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Close(); err != nil {
		t.Fatal(err)
	}
	return st
}

// Ingest M2: the serve alias lookup reports a failed read as an error, never as "no aliases".
func TestStoreAliasesReportsAFailedRead(t *testing.T) {
	aliases, err := storeAliases(closedStore(t))("777777@lid")
	if err == nil || aliases != nil {
		t.Fatalf("aliases=%v err=%v, want the read error and no aliases", aliases, err)
	}
}

// alertsFake records every alert the monitor sends.
type alertsFake struct {
	mu             sync.Mutex
	titles, bodies []string
}

func (a *alertsFake) Notify(_ context.Context, title, body string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.titles, a.bodies = append(a.titles, title), append(a.bodies, body)
	return nil
}

type noQueue struct{}

func (noQueue) Enqueue(context.Context, store.Media) error  { return nil }
func (noQueue) OnRetry(context.Context, *events.MediaRetry) {}

type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.Unix(1_700_000_000, 0) }

// I1: when the alias lookup of a chat fails, the push alert is titled as a read failure and its body names neither
// the chat (possibly the LID alias of an excluded chat) nor the error, which goes to the public ntfy server.
func TestReadFailureAlertNamesNoChat(t *testing.T) {
	notes := &alertsFake{}
	cfg := &config.Config{DataDir: t.TempDir()}
	mon := health.NewMonitor(cfg, notes, fixedClock{}, nil, func() string { return "2.3000.1" })
	monCtx, stopMon := context.WithCancel(context.Background())
	monDone := make(chan struct{})
	go func() { defer close(monDone); mon.Run(monCtx) }()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	sleeps := 0
	sleep := func(time.Duration) {
		if sleeps++; sleeps == 8 { // SIGTERM while the lookup is still failing
			cancel()
		}
	}
	st := closedStore(t)
	p := ingest.NewPipeline(st, ingest.NewFilter([]string{"9665@s.whatsapp.net"}, storeAliases(st)), noQueue{}, mon, slog.New(slog.NewTextHandler(io.Discard, nil))).
		WithClock(fixedClock{}.Now, sleep).WithContext(ctx)
	chat := mustJID(t, "777777@lid")
	p.Handle(&events.Message{Info: types.MessageInfo{MessageSource: types.MessageSource{Chat: chat, Sender: chat}, ID: "A", Timestamp: time.Unix(10, 0)}, Message: &waE2E.Message{Conversation: proto.String("secret")}})
	stopMon()
	<-monDone
	notes.mu.Lock()
	defer notes.mu.Unlock()
	if len(notes.bodies) != 1 || notes.titles[0] != "Archive read failed" {
		t.Fatalf("titles=%q bodies=%q, want one read-failure alert", notes.titles, notes.bodies)
	}
	if body := notes.bodies[0]; strings.Contains(body, "777777") || strings.Contains(body, "alias") || strings.Contains(body, "closed") {
		t.Fatalf("alert body %q carries the chat or the error", body)
	}
	if body := notes.bodies[0]; body != "A write or read is failing; see the journal" {
		t.Fatalf("body=%q", body)
	}
}
