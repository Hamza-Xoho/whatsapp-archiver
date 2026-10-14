package media

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

// lockedBuf is a goroutine-safe log sink.
type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// I-1: a re-upload is requested once per message. When the re-uploaded media is expired again the row fails
// instead of cycling expired → request → download, however many passes run and however far the clock moves.
func TestReuploadedMediaThatExpiresAgainFailsAfterOneRequest(t *testing.T) {
	st := testkit.Store(t)
	clock := newClock()
	d := expiredDown() // every direct path, including the re-uploaded one, is expired
	q := newTestQueue(t, st, d, config.MediaRules{Download: []string{"image"}, Workers: 1}, clock, nil)
	enqueue(t, q, store.Media{ChatJID: "123@g.us", MessageID: "TWICE", SenderJID: "5@lid", Type: "image", RawProto: []byte("x")})
	stop := start(t, q)
	eventually(t, "re-upload requested", func() bool { return row(t, st, "123@g.us", "TWICE").State == "retry_requested" })
	chat, _ := types.ParseJID("123@g.us")
	q.OnRetry(t.Context(), &events.MediaRetry{ChatID: chat, MessageID: "TWICE"})
	eventually(t, "row failed or requested again", func() bool {
		return row(t, st, "123@g.us", "TWICE").State == "failed" || len(d.requests()) >= 2
	})
	for i := 0; i < 3; i++ { // later passes, minutes apart: the limiter refills but nothing may be requested
		clock.Advance(2 * time.Minute)
		reads := clock.Reads()
		eventually(t, "later worker passes", func() bool { return clock.Reads() >= reads+20 })
	}
	stop()
	if n := len(d.requests()); n != 1 {
		t.Fatalf("re-upload requests = %d, want exactly 1", n)
	}
	m := row(t, st, "123@g.us", "TWICE")
	if m.State != "failed" || m.Reason != "re-uploaded media expired" {
		t.Fatalf("row state=%q reason=%q, want failed / re-uploaded media expired", m.State, m.Reason)
	}
}

// I-1: an expired row that used up its 20 attempts fails without another re-upload request.
func TestExpiredRowAtAttemptCapFailsWithoutRequest(t *testing.T) {
	st := testkit.Store(t)
	clock := newClock()
	d := expiredDown()
	if err := st.InsertMedia(t.Context(), store.Media{ChatJID: "123@g.us", MessageID: "CAP", SenderJID: "5@lid", Type: "image", State: "expired", Attempts: maxAttempts, RawProto: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	q := newTestQueue(t, st, d, config.MediaRules{Download: []string{"image"}, Workers: 1}, clock, nil)
	stop := start(t, q)
	eventually(t, "row failed or requested", func() bool {
		return row(t, st, "123@g.us", "CAP").State == "failed" || len(d.requests()) > 0
	})
	reads := clock.Reads()
	eventually(t, "later worker passes", func() bool { return clock.Reads() >= reads+20 })
	stop()
	if n := len(d.requests()); n != 0 {
		t.Fatalf("re-upload requests = %d, want 0", n)
	}
	if m := row(t, st, "123@g.us", "CAP"); m.State != "failed" || m.Reason == "" {
		t.Fatalf("row state=%q reason=%q, want failed with a reason", m.State, m.Reason)
	}
}

// I-2: when the startup reset of stuck rows fails, the error is logged at Error. Run gets an already cancelled
// context, so the reset fails and no worker does anything else.
func TestResetMediaOnStartFailureIsLogged(t *testing.T) {
	var sink lockedBuf
	log := slog.New(slog.NewTextHandler(&sink, nil))
	q := NewQueue(t.TempDir(), config.MediaRules{Download: []string{"image"}, Workers: 1}, testkit.Store(t), &fakeDown{}, newClock(), log, nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	q.Run(ctx)
	if out := sink.String(); !strings.Contains(out, "level=ERROR") {
		t.Fatalf("log has no Error record for the failed reset:\n%s", out)
	}
}
