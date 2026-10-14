package media

import (
	"fmt"
	"sync"
	"testing"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
	"whatsapp-archiver/internal/wa"
)

// H3: expired media is re-requested once with the row's identity and raw proto; the MediaRetry answer brings a new
// direct path, the row is downloaded from it and ends done.
func TestExpiredMediaRetryCompletes(t *testing.T) {
	st := testkit.Store(t)
	var mu sync.Mutex
	var raws []string
	d := &fakeDown{
		download: func(_ []byte, path string) ([]byte, error) {
			if path == "new" {
				return []byte("re-uploaded"), nil
			}
			return nil, wa.ErrMediaExpired
		},
		request: func(_ wa.MediaRef, raw []byte) error {
			mu.Lock()
			raws = append(raws, string(raw))
			mu.Unlock()
			return nil
		},
	}
	q := newTestQueue(t, st, d, config.MediaRules{Download: []string{"image"}, Workers: 1}, newClock(), nil)
	enqueue(t, q, store.Media{ChatJID: "123@g.us", MessageID: "EXP", SenderJID: "5@lid", FromMe: true, Source: "history", Type: "image", RawProto: []byte("raw-exp")})
	stop := start(t, q)
	eventually(t, "re-upload requested", func() bool { return row(t, st, "123@g.us", "EXP").State == "retry_requested" })
	chat, _ := types.ParseJID("123@g.us")
	q.OnRetry(t.Context(), &events.MediaRetry{ChatID: chat, MessageID: "EXP"})
	eventually(t, "row done", func() bool { return row(t, st, "123@g.us", "EXP").State == "done" })
	stop()
	refs := d.requests()
	want := wa.MediaRef{Chat: "123@g.us", Sender: "5@lid", ID: "EXP", FromMe: true}
	mu.Lock()
	defer mu.Unlock()
	if len(refs) != 1 || refs[0] != want || len(raws) != 1 || raws[0] != "raw-exp" {
		t.Fatalf("requests=%+v raws=%q, want one %+v with raw-exp", refs, raws, want)
	}
	if m := row(t, st, "123@g.us", "EXP"); m.RetryDirectPath != "new" || m.Path == "" {
		t.Fatalf("row=%+v", m)
	}
}

// A row whose raw proto has no media key fails with reason "no media key" instead of waiting for a re-upload.
func TestExpiredMediaWithoutKeyFails(t *testing.T) {
	st := testkit.Store(t)
	d := &fakeDown{
		download: func([]byte, string) ([]byte, error) { return nil, wa.ErrMediaExpired },
		request:  func(wa.MediaRef, []byte) error { return fmt.Errorf("build request: %w", wa.ErrNoMediaKey) },
	}
	q := newTestQueue(t, st, d, config.MediaRules{Download: []string{"image"}, Workers: 1}, newClock(), nil)
	enqueue(t, q, store.Media{ChatJID: "123@g.us", MessageID: "NOKEY", SenderJID: "5@lid", Type: "image", RawProto: []byte("x")})
	stop := start(t, q)
	eventually(t, "row failed", func() bool { return row(t, st, "123@g.us", "NOKEY").State == "failed" })
	stop()
	if m := row(t, st, "123@g.us", "NOKEY"); m.Reason != "no media key" {
		t.Fatalf("row=%+v", m)
	}
}
