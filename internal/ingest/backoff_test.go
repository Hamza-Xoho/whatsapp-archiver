package ingest_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"testing"
	"time"

	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

// runawayWriter cancels the context once the writer has been hit `limit` times, so a missing back-off ends the
// test (with a failure) instead of spinning forever on a fake clock.
type runawayWriter struct {
	*flakyWriter
	cancel context.CancelFunc
	limit  int
}

func (w *runawayWriter) InsertMessage(ctx context.Context, m store.Message) (bool, error) {
	if w.calls["InsertMessage"] >= w.limit {
		w.cancel()
	}
	return w.flakyWriter.InsertMessage(ctx, m)
}

// constraintWriter fails every message insert the way SQLite reports a constraint violation. cancel (if set)
// fires after 20 calls so a missing "permanent" classification ends the test with a failure instead of hanging.
type constraintWriter struct {
	*store.Store
	calls  int
	cancel context.CancelFunc
}

func (w *constraintWriter) InsertMessage(context.Context, store.Message) (bool, error) {
	w.calls++
	if w.calls >= 20 {
		w.cancel()
	}
	return false, fmt.Errorf("insert message: %w", store.ErrConstraint)
}

// H2: after the retry policy gives up, the write is retried at its longest interval, not in a hot loop.
func TestWriteRetryBacksOff(t *testing.T) {
	st := testkit.Store(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	w := &runawayWriter{flakyWriter: newFlakyWriter(st, "InsertMessage", math.MaxInt), cancel: cancel, limit: 5000}
	ft := newFakeTime()
	start := ft.clock.Now()
	ft.onTick = func() {
		if ft.clock.Now().Sub(start) >= time.Minute {
			cancel()
		}
	}
	h := &healthFake{}
	p := newPipeline(w, &queueFake{}, h, ft)
	err := p.Ingest(ctx, ingest.FromMessage(messageEvent("g@g.us", "A", text("x")), "live", ft.clock.Now()))
	if err == nil {
		t.Fatal("Ingest returned nil although the write never succeeded")
	}
	if calls := w.calls["InsertMessage"]; calls > 400 {
		t.Fatalf("%d insert attempts in one fake minute: no back-off", calls)
	}
	ms := time.Millisecond
	want := []time.Duration{10 * ms, 20 * ms, 40 * ms, 80 * ms}
	if len(ft.sleeps) < 10 || fmt.Sprint(ft.sleeps[:4]) != fmt.Sprint(want) {
		t.Fatalf("sleeps=%v", ft.sleeps)
	}
	for _, d := range ft.sleeps[4:] {
		if d != 160*ms {
			t.Fatalf("retry interval %v, want 160ms; sleeps=%v", d, ft.sleeps)
		}
	}
	if h.failed != 1 {
		t.Fatalf("WriteFailed=%d, want exactly 1", h.failed)
	}
}

// Re-delivering an event the archive already holds is a no-op: no error, no alert, no retry.
func TestDuplicateEventIsNotARetryableError(t *testing.T) {
	st := testkit.Store(t)
	ft := newFakeTime()
	h := &healthFake{}
	p := newPipeline(st, &queueFake{}, h, ft)
	r := ingest.Result{
		Kind:     ingest.KindReaction,
		Chat:     store.Chat{JID: "g@g.us"},
		Event:    &store.MessageEvent{ChatJID: "g@g.us", TargetID: "A", Kind: "reaction", EventID: "E1", Timestamp: 10, ReceivedAt: 10},
		Received: ft.clock.Now(),
	}
	for i := 0; i < 2; i++ {
		if err := p.Ingest(t.Context(), r); err != nil {
			t.Fatalf("delivery %d: %v", i+1, err)
		}
	}
	if h.failed != 0 || h.seen != 2 || len(ft.sleeps) != 0 {
		t.Fatalf("WriteFailed=%d seen=%d sleeps=%v", h.failed, h.seen, ft.sleeps)
	}
}

// A constraint violation can never succeed on retry: alert once, log what failed, return the error, and do not
// block ingestion.
func TestConstraintErrorIsPermanent(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var buf bytes.Buffer
	w := &constraintWriter{Store: testkit.Store(t), cancel: cancel}
	ft := newFakeTime()
	h := &healthFake{}
	p := ingest.NewPipeline(w, ingest.NewFilter(nil, nil), &queueFake{}, h, slog.New(slog.NewTextHandler(&buf, nil))).WithClock(ft.clock.Now, ft.sleep)
	err := p.Ingest(ctx, ingest.FromMessage(messageEvent("g@g.us", "A", text("x")), "live", ft.clock.Now()))
	if !errors.Is(err, store.ErrConstraint) {
		t.Fatalf("err=%v", err)
	}
	if w.calls != 1 || h.failed != 1 || len(ft.sleeps) != 0 || h.seen != 0 {
		t.Fatalf("insert calls=%d WriteFailed=%d sleeps=%v seen=%d", w.calls, h.failed, ft.sleeps, h.seen)
	}
	if out := buf.String(); !strings.Contains(out, "level=ERROR") || !strings.Contains(out, "g@g.us") {
		t.Fatalf("log=%q", out)
	}
}

// lateConstraintWriter fails like a locked database for the first `transient` inserts, then with a constraint error.
type lateConstraintWriter struct {
	constraintWriter
	transient int
}

func (w *lateConstraintWriter) InsertMessage(ctx context.Context, m store.Message) (bool, error) {
	if w.calls < w.transient {
		w.calls++
		return false, errors.New("database is locked")
	}
	return w.constraintWriter.InsertMessage(ctx, m)
}

// B1/H2 carry-over: a constraint error that appears after the retry policy gave up still stops the retry loop.
func TestConstraintErrorDuringRetryLoopIsPermanent(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	w := &lateConstraintWriter{constraintWriter: constraintWriter{Store: testkit.Store(t), cancel: cancel}, transient: 6}
	ft := newFakeTime()
	h := &healthFake{}
	p := newPipeline(w, &queueFake{}, h, ft)
	err := p.Ingest(ctx, ingest.FromMessage(messageEvent("g@g.us", "A", text("x")), "live", ft.clock.Now()))
	if !errors.Is(err, store.ErrConstraint) || w.calls != 7 || ctx.Err() != nil || h.failed != 1 {
		t.Fatalf("err=%v insert calls=%d cancelled=%v WriteFailed=%d", err, w.calls, ctx.Err() != nil, h.failed)
	}
}
