package store_test

import (
	"context"
	"fmt"
	"testing"

	"whatsapp-archiver/internal/store"
)

// insertChunks stores one history chunk per progress value, in the order given (received order, not progress order).
func insertChunks(t *testing.T, s *store.Store, progress ...int) {
	t.Helper()
	for i, p := range progress {
		if err := s.InsertHistoryChunk(context.Background(), store.HistoryChunk{ID: fmt.Sprint("c", i), Progress: p, ReceivedAt: int64(100 + i)}); err != nil {
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
func insertSyncChunk(t *testing.T, s *store.Store, id, syncType string, progress int, at int64) {
	t.Helper()
	if err := s.InsertHistoryChunk(context.Background(), store.HistoryChunk{ID: id, SyncType: syncType, Progress: progress, ReceivedAt: at}); err != nil {
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
		insertSyncChunk(t, s, "b1", "INITIAL_BOOTSTRAP", 100, 100)
		insertSyncChunk(t, s, "f1", "FULL", 30, 200)
		insertSyncChunk(t, s, "f2", "FULL", 57, 201)
		c, err := s.HistoryCoverage(ctx)
		if err != nil || c.Complete || c.LastProgress != 57 {
			t.Fatalf("coverage=%+v err=%v; want Complete=false, LastProgress=57", c, err)
		}
	})
	t.Run("FULL chunk at 100 received out of order is complete", func(t *testing.T) {
		s := open(t)
		insertSyncChunk(t, s, "b1", "INITIAL_BOOTSTRAP", 40, 100)
		insertSyncChunk(t, s, "f1", "FULL", 100, 200)
		insertSyncChunk(t, s, "f2", "FULL", 57, 201)
		c, err := s.HistoryCoverage(ctx)
		if err != nil || !c.Complete || c.LastProgress != 57 {
			t.Fatalf("coverage=%+v err=%v; want Complete=true, LastProgress=57", c, err)
		}
	})
	t.Run("a bootstrap at 100 completes history when there is no FULL sync", func(t *testing.T) {
		s := open(t)
		insertSyncChunk(t, s, "b1", "INITIAL_BOOTSTRAP", 100, 100)
		insertSyncChunk(t, s, "r1", "RECENT", 10, 200)
		c, err := s.HistoryCoverage(ctx)
		if err != nil || !c.Complete {
			t.Fatalf("coverage=%+v err=%v; want Complete=true", c, err)
		}
	})
	t.Run("a RECENT chunk at 100 does not complete history", func(t *testing.T) {
		s := open(t)
		insertSyncChunk(t, s, "f1", "FULL", 20, 100)
		insertSyncChunk(t, s, "r1", "RECENT", 100, 200)
		c, err := s.HistoryCoverage(ctx)
		if err != nil || c.Complete {
			t.Fatalf("coverage=%+v err=%v; want Complete=false", c, err)
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
