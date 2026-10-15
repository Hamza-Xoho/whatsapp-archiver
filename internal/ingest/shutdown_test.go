package ingest_test

import (
	"context"
	"math"
	"testing"

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
