package ingest_test

import (
	"context"
	"errors"
	"testing"

	"go.mau.fi/whatsmeow/types/events"
	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

// cutWriter is the real store whose message writes fail from call number `from` on, like a store that stops answering
// in the middle of a chunk.
type cutWriter struct {
	*store.Store
	from, calls int
}

func (w *cutWriter) Apply(ctx context.Context, b store.Batch) (store.Applied, error) {
	if b.Message != nil {
		if w.calls++; w.calls >= w.from {
			return store.Applied{}, errors.New("database is locked")
		}
	}
	return w.Store.Apply(ctx, b)
}

func threeMessageChunk() *events.HistorySync {
	return chunk("g@g.us", webMsg("H1", "3@lid", false, text("a")), webMsg("H2", "3@lid", false, text("b")), webMsg("H3", "3@lid", false, text("c")))
}

func chunkCount(t *testing.T, st *store.Store) int {
	t.Helper()
	c, err := st.HistoryCoverage(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return c.Chunks
}

// Problem 4: SIGTERM in the middle of a chunk stops the import; the ledger row, which claims the whole chunk, is not
// written, so the redelivered chunk is imported again instead of looking done.
func TestInterruptedHistoryImportLeavesNoLedgerRow(t *testing.T) {
	st := testkit.Store(t)
	serve, cancel := context.WithCancel(t.Context())
	cancel() // SIGTERM has arrived
	p := newPipeline(&cutWriter{Store: st, from: 2}, &queueFake{}, &healthFake{}, newFakeTime()).WithContext(serve)
	_, _, err := p.ImportHistory(t.Context(), threeMessageChunk())
	if err == nil {
		t.Fatal("an interrupted chunk reported success")
	}
	if n := chunkCount(t, st); n != 0 {
		t.Fatalf("ledger has %d chunks after an interrupted import, want 0", n)
	}
}

// No-loss proof: the first delivery is cut after one message, the store is closed and reopened, and the chunk is
// delivered twice more. The archive ends identical to one uninterrupted delivery.
func TestNoHistoryLossAcrossInjectedShutdown(t *testing.T) {
	st := testkit.Store(t)
	path := st.Path()
	serve, cancel := context.WithCancel(t.Context())
	cancel()
	ft := newFakeTime()
	cut := newPipeline(&cutWriter{Store: st, from: 2}, &queueFake{}, &healthFake{}, ft).WithContext(serve)
	if _, _, err := cut.ImportHistory(t.Context(), threeMessageChunk()); err == nil {
		t.Fatal("the cut delivery reported success")
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	again := newPipeline(reopened, &queueFake{}, &healthFake{}, ft)
	for i := range 2 {
		if _, _, err := again.ImportHistory(t.Context(), threeMessageChunk()); err != nil {
			t.Fatalf("redelivery %d: %v", i+1, err)
		}
	}
	if got := rows(t, reopened, "SELECT count(DISTINCT id) FROM messages"); len(got) != 1 || got[0] != "3" {
		t.Fatalf("distinct messages=%v, want 3", got)
	}
	if n := chunkCount(t, reopened); n != 1 {
		t.Fatalf("ledger has %d chunks, want 1", n)
	}
	want := testkit.Store(t)
	if _, _, err := newPipeline(want, &queueFake{}, &healthFake{}, newFakeTime()).ImportHistory(t.Context(), threeMessageChunk()); err != nil {
		t.Fatal(err)
	}
	gotDump, err := ingest.DumpRaw(reopened)
	if err != nil {
		t.Fatal(err)
	}
	wantDump, err := ingest.DumpRaw(want)
	if err != nil {
		t.Fatal(err)
	}
	if gotDump != wantDump {
		t.Fatalf("archive after an interrupted and redelivered chunk differs from one delivery:\n%s\nwant:\n%s", gotDump, wantDump)
	}
}

// A chunk that failed to import is not acked, so WhatsApp delivers it again.
func TestHandleReportsFailedHistoryImportAsNotHandled(t *testing.T) {
	st := testkit.Store(t)
	p := ingest.NewPipeline(st, ingest.NewFilter(nil, nil), &queueFake{}, &healthFake{}, quiet()) // no history parser: the import errors
	if p.Handle(threeMessageChunk()) {
		t.Fatal("a failed history import was acked")
	}
}
