package store_test

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"whatsapp-archiver/internal/store"
)

// R10: the ledger key is (id, stage). A chunk can be ledgered as received and, later, as imported; both rows are
// inserts, so the table stays insert-only. A row without a stage is an imported chunk (what the importer writes today).
func TestHistoryLedgerKeyIsIDAndStage(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	for _, h := range []store.HistoryChunk{
		{ID: "c1", ReceivedAt: 1},
		{ID: "c1", Stage: store.StageReceived, SpoolID: 4, ReceivedAt: 1},
		{ID: "c1", Stage: store.StageImported, SpoolID: 4, ReceivedAt: 2}, // the same row as the first: ignored
	} {
		if err := s.InsertHistoryChunk(ctx, h); err != nil {
			t.Fatal(err)
		}
	}
	if got := text(t, s, "SELECT group_concat(stage||':'||spool_id, ',') FROM (SELECT * FROM history_sync_chunks ORDER BY stage)"); got != "imported:0,received:4" {
		t.Fatalf("ledger rows = %q, want imported:0,received:4", got)
	}
}

func TestHistoryLedgerRejectsAnUnknownStage(t *testing.T) {
	s := open(t)
	err := s.InsertHistoryChunk(context.Background(), store.HistoryChunk{ID: "c1", Stage: "dreamt"})
	if !errors.Is(err, store.ErrConstraint) {
		t.Fatalf("err = %v, want a constraint error", err)
	}
}

// Coverage is about imported history: a chunk that is only spooled has not made the archive any more complete.
func TestCoverageCountsImportedChunksOnly(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if err := s.InsertHistoryChunk(ctx, store.HistoryChunk{ID: "a", Stage: store.StageReceived, SyncType: "FULL", Progress: 100, ReceivedAt: 1}); err != nil {
		t.Fatal(err)
	}
	c, err := s.HistoryCoverage(ctx)
	if err != nil || c.Chunks != 0 || c.Complete {
		t.Fatalf("coverage = %+v, %v; want nothing yet", c, err)
	}
	if err = s.InsertHistoryChunk(ctx, store.HistoryChunk{ID: "a", Stage: store.StageImported, SyncType: "FULL", Progress: 100, ReceivedAt: 2}); err != nil {
		t.Fatal(err)
	}
	c, err = s.HistoryCoverage(ctx)
	if err != nil || c.Chunks != 1 || !c.Complete {
		t.Fatalf("coverage = %+v, %v; want one imported chunk, complete", c, err)
	}
}

func spool(chunk string) store.Spool {
	return store.Spool{ChunkID: chunk, SyncType: "FULL", ChunkOrder: 1, Progress: 40, Meta: `{"size":3}`, BlobFormat: "zlib", Blob: []byte("zzz"), ReceivedAt: 1000}
}

// ADR-37: the spool row and the `received` ledger row are one transaction. A resent chunk is a no-op.
func TestSpoolReceiveWritesSpoolAndLedgerAndIgnoresResends(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	before := commitsOn(s)
	id, isNew, err := s.SpoolReceive(ctx, spool("c1"), store.HistoryChunk{ID: "c1", SyncType: "FULL", ChunkOrder: 1, Progress: 40, ReceivedAt: 1000})
	if err != nil || !isNew || id == 0 {
		t.Fatalf("SpoolReceive = %d, %v, %v", id, isNew, err)
	}
	if n := commitsOn(s) - before; n != 1 {
		t.Fatalf("SpoolReceive committed %d times, want 1", n)
	}
	again, isNew, err := s.SpoolReceive(ctx, spool("c1"), store.HistoryChunk{ID: "c1", SyncType: "FULL", ReceivedAt: 1001})
	if err != nil || isNew || again != id {
		t.Fatalf("resend = %d, %v, %v; want the same row and not new", again, isNew, err)
	}
	if got := text(t, s, "SELECT stage||':'||spool_id FROM history_sync_chunks"); got != "received:"+itoa(id) {
		t.Fatalf("ledger = %q", got)
	}
	if got := text(t, s, "SELECT size||':'||blob_format||':'||attempts FROM history_spool"); got != "3:zlib:0" {
		t.Fatalf("spool row = %q, want size 3 taken from the blob", got)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestSpoolRejectsBadFormatAndBadMeta(t *testing.T) {
	s := open(t)
	for name, mutate := range map[string]func(*store.Spool){
		"blob format": func(sp *store.Spool) { sp.BlobFormat = "zstd" },
		"meta":        func(sp *store.Spool) { sp.Meta = "{" },
	} {
		sp := spool("c-" + name)
		mutate(&sp)
		if _, _, err := s.SpoolReceive(context.Background(), sp, store.HistoryChunk{ID: sp.ChunkID}); !errors.Is(err, store.ErrConstraint) {
			t.Errorf("%s: err = %v, want a constraint error", name, err)
		}
	}
	if n := count(t, s, "SELECT COUNT(*) FROM history_spool") + count(t, s, "SELECT COUNT(*) FROM history_sync_chunks"); n != 0 {
		t.Fatalf("a rejected chunk left %d rows", n)
	}
}

// The importer's work list is the not-yet-imported rows in arrival order; marking a row imported writes the
// `imported` ledger row in the same transaction and takes it off the list.
func TestSpoolTodoAndMarkImported(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	ids := map[string]int64{}
	for _, c := range []string{"c1", "c2", "c3"} {
		id, _, err := s.SpoolReceive(ctx, spool(c), store.HistoryChunk{ID: c, SyncType: "FULL", ReceivedAt: 1000})
		if err != nil {
			t.Fatal(err)
		}
		ids[c] = id
	}
	todo, err := s.SpoolTodo(ctx, 2)
	if err != nil || len(todo) != 2 || todo[0].ChunkID != "c1" || todo[1].ChunkID != "c2" || string(todo[0].Blob) != "zzz" || todo[0].BlobFormat != "zlib" || todo[0].Meta != `{"size":3}` {
		t.Fatalf("todo = %+v, %v", todo, err)
	}
	if err = s.SpoolImported(ctx, ids["c1"], 2000, store.HistoryChunk{ID: "c1", SyncType: "FULL", MessageCount: 7, ReceivedAt: 2000}); err != nil {
		t.Fatal(err)
	}
	todo, err = s.SpoolTodo(ctx, 10)
	if err != nil || len(todo) != 2 || todo[0].ChunkID != "c2" {
		t.Fatalf("todo after import = %+v, %v", todo, err)
	}
	if got := text(t, s, "SELECT stage||':'||spool_id||':'||message_count FROM history_sync_chunks WHERE id='c1' AND stage='imported'"); got != "imported:"+itoa(ids["c1"])+":7" {
		t.Fatalf("imported ledger row = %q", got)
	}
	if got := text(t, s, "SELECT imported_at FROM history_spool WHERE chunk_id='c1'"); got != "2000" {
		t.Fatalf("imported_at = %q", got)
	}
}

func TestSpoolFailedCountsAttemptsAndKeepsTheRowOnTheList(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	id, _, _ := s.SpoolReceive(ctx, spool("c1"), store.HistoryChunk{ID: "c1"})
	for range 2 {
		if err := s.SpoolFailed(ctx, id, "decode: boom"); err != nil {
			t.Fatal(err)
		}
	}
	todo, err := s.SpoolTodo(ctx, 10)
	if err != nil || len(todo) != 1 || todo[0].Attempts != 2 || todo[0].Error != "decode: boom" {
		t.Fatalf("todo = %+v, %v", todo, err)
	}
}

// Decision Q1: the blob is cleared 7 days after a successful import; the row, its size and its metadata stay.
func TestSpoolClearBlobsAfterImport(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	imp := func(chunk string, at int64) int64 {
		id, _, err := s.SpoolReceive(ctx, spool(chunk), store.HistoryChunk{ID: chunk})
		if err != nil {
			t.Fatal(err)
		}
		if at > 0 {
			if err = s.SpoolImported(ctx, id, at, store.HistoryChunk{ID: chunk}); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	imp("old", 1000)
	imp("recent", 5000)
	imp("never", 0)
	n, err := s.SpoolClearBlobs(ctx, 3000) // imported before 3000
	if err != nil || n != 1 {
		t.Fatalf("cleared %d rows, %v; want 1", n, err)
	}
	got := text(t, s, "SELECT group_concat(chunk_id||':'||length(blob)||':'||size, ',') FROM (SELECT * FROM history_spool ORDER BY id)")
	if got != "old:0:3,recent:3:3,never:3:3" {
		t.Fatalf("spool rows = %q", got)
	}
	if n, err = s.SpoolClearBlobs(ctx, 3000); err != nil || n != 0 {
		t.Fatalf("second clear = %d, %v; want 0", n, err)
	}
}
