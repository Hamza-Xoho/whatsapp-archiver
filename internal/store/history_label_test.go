package store_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"whatsapp-archiver/internal/store"
)

// W5.2: status keeps each sync type and each durable ledger stage separate. A received chunk is visible before the
// asynchronous importer finishes it, while imported coverage remains distinguishable.
func TestHistoryCoverageReportsEachSyncTypeAndLedgerStage(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	spoolID, _, err := s.SpoolReceive(ctx, store.Spool{ChunkID: "f2", SyncType: "FULL", Progress: 57, ReceivedAt: 200, BlobFormat: store.BlobRaw, Blob: []byte("x")}, store.HistoryChunk{ID: "f2", SyncType: "FULL", ChunkOrder: 2, Progress: 57, ReceivedAt: 200})
	if err != nil {
		t.Fatal(err)
	}
	insertSyncChunk(t, s, "f1", "FULL", 1, 30, 100)
	insertSyncChunk(t, s, "b1", "INITIAL_BOOTSTRAP", 0, 100, 50)
	if err = s.SpoolImported(ctx, spoolID, 201, store.HistoryChunk{ID: "f2", SyncType: "FULL", ChunkOrder: 2, Progress: 57, ReceivedAt: 200}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.SpoolReceive(ctx, store.Spool{ChunkID: "r1", SyncType: "RECENT", Progress: 20, ReceivedAt: 300, BlobFormat: store.BlobRaw, Blob: []byte("x")}, store.HistoryChunk{ID: "r1", SyncType: "RECENT", Progress: 20, ReceivedAt: 300}); err != nil {
		t.Fatal(err)
	}

	c, err := s.HistoryCoverage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []store.SyncCoverage{
		{SyncType: "FULL", ReceivedChunks: 1, ImportedChunks: 2, LastProgress: 57, LastChunkTime: 200},
		{SyncType: "INITIAL_BOOTSTRAP", ImportedChunks: 1, LastProgress: 100, LastChunkTime: 50, Complete: true},
		{SyncType: "RECENT", ReceivedChunks: 1, LastProgress: 20, LastChunkTime: 300},
	}
	if !reflect.DeepEqual(c.Syncs, want) {
		t.Fatalf("sync coverage = %+v, want %+v", c.Syncs, want)
	}
}

// insertChunks stores one history chunk per progress value, in the order given (received order, not progress order).
func insertChunks(t *testing.T, s *store.Store, progress ...int) {
	t.Helper()
	for i, p := range progress {
		if err := s.InsertHistoryChunk(context.Background(), store.HistoryChunk{ID: fmt.Sprint("c", i), ChunkOrder: i, Progress: p, ReceivedAt: int64(100 + i)}); err != nil {
			t.Fatal(err)
		}
	}
}

// The "complete" label while no chunk has reached 100: history is still in progress.
func TestHistoryNotCompleteWhileNoChunkReachesHundred(t *testing.T) {
	s := open(t)
	insertChunks(t, s, 10, 40, 57)
	c, err := s.HistoryCoverage(context.Background())
	if err != nil || c.Complete || c.LastProgress != 57 {
		t.Fatalf("coverage=%+v err=%v; want Complete=false, LastProgress=57", c, err)
	}
}

// insertSyncChunk stores one chunk of a given sync type; at is its received_at.
func insertSyncChunk(t *testing.T, s *store.Store, id, syncType string, order, progress int, at int64) {
	t.Helper()
	if err := s.InsertHistoryChunk(context.Background(), store.HistoryChunk{ID: id, SyncType: syncType, ChunkOrder: order, Progress: progress, ReceivedAt: at}); err != nil {
		t.Fatal(err)
	}
}

// History is complete only when the newest sync of type FULL (else INITIAL_BOOTSTRAP) has a chunk at progress 100.
// "Newest" is the most recent received_at among the chunks of that sync type. An older sync that finished says
// nothing about a newer one that is still running.
func TestHistoryCompleteOnlyWhenLatestSyncReachesHundred(t *testing.T) {
	ctx := context.Background()
	t.Run("old bootstrap at 100, newer FULL at 57 is in progress", func(t *testing.T) {
		s := open(t)
		insertSyncChunk(t, s, "b1", "INITIAL_BOOTSTRAP", 0, 100, 100)
		insertSyncChunk(t, s, "f1", "FULL", 0, 30, 200)
		insertSyncChunk(t, s, "f2", "FULL", 1, 57, 201)
		c, err := s.HistoryCoverage(ctx)
		if err != nil || c.Complete || c.LastProgress != 57 {
			t.Fatalf("coverage=%+v err=%v; want Complete=false, LastProgress=57", c, err)
		}
	})
	t.Run("FULL chunk at 100 received out of order is complete", func(t *testing.T) {
		s := open(t)
		insertSyncChunk(t, s, "b1", "INITIAL_BOOTSTRAP", 0, 40, 100)
		insertSyncChunk(t, s, "f0", "FULL", 0, 30, 200)
		insertSyncChunk(t, s, "f2", "FULL", 2, 100, 201)
		insertSyncChunk(t, s, "f1", "FULL", 1, 57, 202)
		c, err := s.HistoryCoverage(ctx)
		if err != nil || !c.Complete || c.LastProgress != 57 {
			t.Fatalf("coverage=%+v err=%v; want Complete=true, LastProgress=57", c, err)
		}
	})
	t.Run("a bootstrap at 100 completes history when there is no FULL sync", func(t *testing.T) {
		s := open(t)
		insertSyncChunk(t, s, "b1", "INITIAL_BOOTSTRAP", 0, 100, 100)
		insertSyncChunk(t, s, "r1", "RECENT", 0, 10, 200)
		c, err := s.HistoryCoverage(ctx)
		if err != nil || !c.Complete {
			t.Fatalf("coverage=%+v err=%v; want Complete=true", c, err)
		}
	})
	t.Run("a RECENT chunk at 100 does not complete history", func(t *testing.T) {
		s := open(t)
		insertSyncChunk(t, s, "f1", "FULL", 0, 20, 100)
		insertSyncChunk(t, s, "r1", "RECENT", 0, 100, 200)
		c, err := s.HistoryCoverage(ctx)
		if err != nil || c.Complete {
			t.Fatalf("coverage=%+v err=%v; want Complete=false", c, err)
		}
	})
	t.Run("an old FULL sync at 100 does not complete a newer FULL sync at 57", func(t *testing.T) {
		s := open(t)
		insertSyncChunk(t, s, "o1", "FULL", 1, 40, 100)
		insertSyncChunk(t, s, "o2", "FULL", 2, 100, 101)
		insertSyncChunk(t, s, "n1", "FULL", 1, 57, 500) // the restarted sync re-sends chunk 1
		c, err := s.HistoryCoverage(ctx)
		if err != nil || c.Complete || c.LastProgress != 57 {
			t.Fatalf("coverage=%+v err=%v; want Complete=false, LastProgress=57", c, err)
		}
	})
	t.Run("the newer FULL sync completes when it reaches 100", func(t *testing.T) {
		s := open(t)
		insertSyncChunk(t, s, "o1", "FULL", 1, 40, 100)
		insertSyncChunk(t, s, "o2", "FULL", 2, 100, 101)
		insertSyncChunk(t, s, "n1", "FULL", 1, 57, 500)
		insertSyncChunk(t, s, "n2", "FULL", 2, 100, 501)
		c, err := s.HistoryCoverage(ctx)
		if err != nil || !c.Complete || c.LastProgress != 100 {
			t.Fatalf("coverage=%+v err=%v; want Complete=true, LastProgress=100", c, err)
		}
	})
	t.Run("no chunks is unchanged", func(t *testing.T) {
		s := open(t)
		c, err := s.HistoryCoverage(ctx)
		if err != nil || c.Complete || c.Chunks != 0 || c.LastProgress != 0 {
			t.Fatalf("coverage=%+v err=%v; want the empty coverage", c, err)
		}
	})
}

// LastProgress follows the sync type that decides completeness: a RECENT or PUSH_NAME chunk received after the FULL
// chunks must not replace the FULL sync's progress. With no chunk of that type the newest chunk of any type is used.
func TestHistoryLastProgressFollowsSyncType(t *testing.T) {
	ctx := context.Background()
	t.Run("a later RECENT chunk at 0 does not hide FULL at 57", func(t *testing.T) {
		s := open(t)
		insertSyncChunk(t, s, "f1", "FULL", 0, 30, 100)
		insertSyncChunk(t, s, "f2", "FULL", 1, 57, 101)
		insertSyncChunk(t, s, "r1", "RECENT", 0, 0, 200)
		c, err := s.HistoryCoverage(ctx)
		if err != nil || c.Complete || c.LastProgress != 57 {
			t.Fatalf("coverage=%+v err=%v; want Complete=false, LastProgress=57", c, err)
		}
	})
	t.Run("a later PUSH_NAME chunk at 30 does not hide FULL at 57", func(t *testing.T) {
		s := open(t)
		insertSyncChunk(t, s, "f1", "FULL", 0, 57, 100)
		insertSyncChunk(t, s, "p1", "PUSH_NAME", 0, 30, 200)
		c, err := s.HistoryCoverage(ctx)
		if err != nil || c.LastProgress != 57 {
			t.Fatalf("coverage=%+v err=%v; want LastProgress=57", c, err)
		}
	})
	t.Run("a bootstrap's progress is used when there is no FULL sync", func(t *testing.T) {
		s := open(t)
		insertSyncChunk(t, s, "b1", "INITIAL_BOOTSTRAP", 0, 40, 100)
		insertSyncChunk(t, s, "r1", "RECENT", 0, 10, 200)
		c, err := s.HistoryCoverage(ctx)
		if err != nil || c.LastProgress != 40 {
			t.Fatalf("coverage=%+v err=%v; want LastProgress=40", c, err)
		}
	})
	t.Run("equal received_at takes the highest chunk order", func(t *testing.T) {
		s := open(t)
		insertSyncChunk(t, s, "f2", "FULL", 2, 57, 100)
		insertSyncChunk(t, s, "f1", "FULL", 1, 30, 100)
		c, err := s.HistoryCoverage(ctx)
		if err != nil || c.LastProgress != 57 {
			t.Fatalf("coverage=%+v err=%v; want LastProgress=57", c, err)
		}
	})
	t.Run("only RECENT chunks fall back to the newest chunk", func(t *testing.T) {
		s := open(t)
		insertSyncChunk(t, s, "r1", "RECENT", 0, 10, 100)
		insertSyncChunk(t, s, "r2", "RECENT", 1, 20, 101)
		c, err := s.HistoryCoverage(ctx)
		if err != nil || c.Complete || c.LastProgress != 20 {
			t.Fatalf("coverage=%+v err=%v; want Complete=false, LastProgress=20", c, err)
		}
	})
}
