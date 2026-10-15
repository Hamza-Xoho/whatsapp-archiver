package ingest_test

import (
	"context"
	"math"
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

// capWriter panics once the writer has been hit `limit` times, so a retry loop that ignores shutdown ends the test
// (the handler guard recovers the panic and reports it) instead of spinning forever on the fake clock.
type capWriter struct {
	*flakyWriter
	limit int
}

func (w *capWriter) InsertMessage(ctx context.Context, m store.Message) (bool, error) {
	if w.calls["InsertMessage"] >= w.limit {
		panic("write retried after the serve context ended")
	}
	return w.flakyWriter.InsertMessage(ctx, m)
}

// M5 / B1: a write that keeps failing is retried until the pipeline's context ends; Handle then returns, the event
// is not stored (whatsmeow has not acked it), and the failure was reported once.
func TestHandleStopsRetryingWhenTheServeContextEnds(t *testing.T) {
	st := testkit.Store(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	w := &capWriter{flakyWriter: newFlakyWriter(st, "InsertMessage", math.MaxInt), limit: 1000}
	ft := newFakeTime()
	sleeps := 0
	ft.onTick = func() {
		if sleeps++; sleeps == 8 { // SIGTERM arrives while the write is still failing
			cancel()
		}
	}
	h := &healthFake{}
	p := newPipeline(w, &queueFake{}, h, ft).WithContext(ctx)
	p.Handle(messageEvent("g@g.us", "A", text("x")))
	if h.panics != 0 {
		t.Fatalf("Handle kept retrying after the context ended (%d writes)", w.calls["InsertMessage"])
	}
	if calls := w.calls["InsertMessage"]; calls < 8 || calls > 12 {
		t.Fatalf("%d write attempts, want about one per sleep up to the cancel", calls)
	}
	if h.failed != 1 || messageCount(t, st) != 0 {
		t.Fatalf("WriteFailed=%d stored=%d, want 1 and 0", h.failed, messageCount(t, st))
	}
}

// M5 round 1: the serve context only ends the retry loop. At SIGTERM a healthy write still completes: the event, its
// id mapping and its media job are stored, and no write failure is reported.
func TestHandleStoresHealthyEventAfterTheServeContextEnded(t *testing.T) {
	st := testkit.Store(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel() // SIGTERM has already arrived
	h := &healthFake{}
	q := &queueFake{}
	p := ingest.NewPipeline(st, ingest.NewFilter(nil, nil), q, h, quiet()).WithContext(ctx)
	e := messageEvent("1234@lid", "IMG", &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Mimetype: proto.String("image/jpeg")}})
	e.Info.RecipientAlt = jid("9665@s.whatsapp.net")
	p.Handle(e)
	if got := rows(t, st, "SELECT id FROM messages"); len(got) != 1 || h.failed != 0 || h.seen != 1 || len(q.items) != 1 {
		t.Fatalf("stored=%v writeFailed=%d seen=%d media=%d, want 1 message, no failure, 1 media job", got, h.failed, h.seen, len(q.items))
	}
	if got := rows(t, st, "SELECT lid FROM id_mappings"); len(got) != 1 {
		t.Fatalf("mappings=%v", got)
	}
}
