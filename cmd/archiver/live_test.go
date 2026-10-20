package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
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
	"whatsapp-archiver/internal/media"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/wa"
)

const (
	liveLID = "1234@lid"
	livePN  = "9665@s.whatsapp.net"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// liveStore is an archive where liveLID and livePN are the same chat.
func liveStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "archive.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err = st.InsertIDMapping(context.Background(), store.IDMapping{LID: liveLID, PN: livePN, Source: "test", FirstSeen: time.Unix(1, 0)}); err != nil {
		t.Fatal(err)
	}
	return st
}

func newLive(st *store.Store, rules []store.ChatRule, aliases func(string) ([]string, error)) liveSettings {
	return liveSettings{st: st, cfg: &config.Config{Media: config.MediaRules{Download: []string{"image"}}}, log: quietLogger(), rules: ingest.NewRuleSet(rules), aliases: aliases}
}

func TestChatMediaFindsTheChatsOwnRule(t *testing.T) {
	st := liveStore(t)
	l := newLive(st, []store.ChatRule{{JID: liveLID, Logging: true, Media: &config.MediaRules{Download: []string{}}}}, storeAliases(st))
	r, err := l.chatMedia(liveLID)
	if err != nil || r == nil || r.Download == nil || len(r.Download) != 0 {
		t.Fatalf("rule=%+v err=%v, want the chat's own rule", r, err)
	}
}

// CON-7: a media rule set on the phone form applies to the LID form.
func TestChatMediaFindsARuleThroughAnAlias(t *testing.T) {
	st := liveStore(t)
	l := newLive(st, []store.ChatRule{{JID: livePN, Logging: true, Media: &config.MediaRules{VideoMaxMB: 3}}}, storeAliases(st))
	r, err := l.chatMedia(liveLID)
	if err != nil || r == nil || r.VideoMaxMB != 3 {
		t.Fatalf("rule=%+v err=%v, want the rule of the phone form", r, err)
	}
}

func TestChatMediaWithoutRuleUsesGlobal(t *testing.T) {
	st := liveStore(t)
	l := newLive(st, nil, storeAliases(st))
	if r, err := l.chatMedia(liveLID); err != nil || r != nil {
		t.Fatalf("rule=%+v err=%v, want nil (global rules)", r, err)
	}
}

func TestChatMediaReportsAFailedAliasLookup(t *testing.T) {
	boom := errors.New("database is locked")
	l := newLive(liveStore(t), nil, func(string) ([]string, error) { return nil, boom })
	if r, err := l.chatMedia(liveLID); !errors.Is(err, boom) || r != nil {
		t.Fatalf("rule=%+v err=%v, want the lookup error and no rule", r, err)
	}
}

func TestLiveMediaReadsTheSetting(t *testing.T) {
	st := liveStore(t)
	l := newLive(st, nil, storeAliases(st))
	if err := st.SetMediaRules(context.Background(), config.MediaRules{Download: []string{"audio"}}); err != nil {
		t.Fatal(err)
	}
	r, err := l.media()
	if err != nil || !slices.Equal(r.Download, []string{"audio"}) {
		t.Fatalf("media=%+v err=%v, want the stored setting", r, err)
	}
}

// After the import config.yaml is stale, so a failed settings read is an error, never config.yaml's rules.
func TestLiveMediaReportsAFailedRead(t *testing.T) {
	st := closedStore(t)
	l := newLive(st, nil, storeAliases(st))
	if _, err := l.media(); err == nil || !errors.Is(err, health.ErrRead) {
		t.Fatalf("err=%v, want a read error", err)
	}
}

type noDownloads struct{}

func (noDownloads) Download(context.Context, []byte, string) ([]byte, error)     { return nil, nil }
func (noDownloads) RequestMediaRetry(context.Context, wa.MediaRef, []byte) error { return nil }
func (noDownloads) ApplyMediaRetry(*events.MediaRetry, []byte) (string, error)   { return "", nil }

type healthCount struct {
	mu     sync.Mutex
	failed int
}

func (h *healthCount) Seen(time.Time, bool)    {}
func (h *healthCount) SetState(string, string) {}
func (h *healthCount) Panic(string)            {}
func (h *healthCount) WriteFailed(error)       { h.mu.Lock(); h.failed++; h.mu.Unlock() }

// I1 end to end: a chat set to download nothing on its phone form gets an image under its LID form while the alias
// lookup is failing. The pipeline retries the enqueue and alerts, and the row is decided by the chat's rule once the
// lookup answers; it is never decided by the global rules.
func TestFailedMediaRuleLookupIsRetriedNotGuessed(t *testing.T) {
	st := liveStore(t)
	failures := 6 // more than the retry policy's attempts, so the failure is alerted
	var mu sync.Mutex
	flaky := func(jid string) ([]string, error) {
		mu.Lock()
		defer mu.Unlock()
		if failures > 0 {
			failures--
			return nil, errors.New("database is locked")
		}
		return st.Aliases(context.Background(), jid)
	}
	l := newLive(st, []store.ChatRule{{JID: livePN, Logging: true, Media: &config.MediaRules{Download: []string{}}}}, flaky)
	q := media.NewQueue(t.TempDir(), l.media, l.chatMedia, st, noDownloads{}, fixedClock{}, quietLogger(), nil)
	h := &healthCount{}
	p := ingest.NewPipeline(st, ingest.NewFilter(l.rules, storeAliases(st)), q, h, quietLogger()).WithClock(fixedClock{}.Now, func(time.Duration) {})
	chat := mustJID(t, liveLID)
	p.Handle(&events.Message{Info: types.MessageInfo{MessageSource: types.MessageSource{Chat: chat, Sender: chat}, ID: "IMG", Timestamp: time.Unix(10, 0)},
		Message: &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Mimetype: proto.String("image/jpeg"), FileLength: proto.Uint64(10)}}})
	m, err := st.MediaByID(context.Background(), liveLID, "IMG")
	if err != nil {
		t.Fatalf("media row: %v", err)
	}
	if m.State != "skipped" {
		t.Fatalf("state %q, want skipped by the chat's own rule", m.State)
	}
	if h.failed == 0 {
		t.Fatal("the failing rule lookup was not reported")
	}
}
