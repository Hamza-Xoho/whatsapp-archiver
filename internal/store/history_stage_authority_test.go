package store_test

import (
	"context"
	"testing"

	"whatsapp-archiver/internal/store"
)

func spoolHistoryChunk(t *testing.T, s *store.Store, id string, order, progress int, receivedAt int64) int64 {
	t.Helper()
	spoolID, _, err := s.SpoolReceive(context.Background(), store.Spool{
		ChunkID: id, SyncType: "FULL", ChunkOrder: order, Progress: progress,
		ReceivedAt: receivedAt, BlobFormat: store.BlobRaw, Blob: []byte("x"),
	}, store.HistoryChunk{ID: id, SyncType: "FULL", ChunkOrder: order, Progress: progress, ReceivedAt: receivedAt})
	if err != nil {
		t.Fatal(err)
	}
	return spoolID
}

// W5.2 review: import may be delayed by a restart. Its imported ledger row must not move the last-arrival time and
// thereby reset the 48-hour stall clock.
func TestHistoryReceivedStageOwnsTheStallClockAfterDelayedImport(t *testing.T) {
	s := open(t)
	spoolID := spoolHistoryChunk(t, s, "old", 0, 57, 100)
	if err := s.SpoolImported(context.Background(), spoolID, 1000, store.HistoryChunk{
		ID: "old", SyncType: "FULL", ChunkOrder: 0, Progress: 57, ReceivedAt: 1000,
	}); err != nil {
		t.Fatal(err)
	}
	c, err := s.HistoryCoverage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Syncs) != 1 || c.Syncs[0].LastChunkTime != 100 || c.Syncs[0].LastProgress != 57 {
		t.Fatalf("coverage after delayed import = %+v; received stage must keep time 100 and progress 57", c.Syncs)
	}
}

// An older completed chunk imported after a newer sync started must not replace the newer sync's progress or make it
// complete. Imported-only rows remain supported separately for legacy/direct ledger callers.
func TestDelayedOldImportDoesNotOverrideNewerHistorySync(t *testing.T) {
	s := open(t)
	oldID := spoolHistoryChunk(t, s, "old-complete", 0, 100, 100)
	_ = spoolHistoryChunk(t, s, "new-progress", 0, 57, 200)
	if err := s.SpoolImported(context.Background(), oldID, 300, store.HistoryChunk{
		ID: "old-complete", SyncType: "FULL", ChunkOrder: 0, Progress: 100, ReceivedAt: 300,
	}); err != nil {
		t.Fatal(err)
	}
	c, err := s.HistoryCoverage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := store.SyncCoverage{SyncType: "FULL", ReceivedChunks: 2, ImportedChunks: 1, LastProgress: 57, LastChunkTime: 200}
	if len(c.Syncs) != 1 || c.Syncs[0] != want || c.Complete || c.LastProgress != 57 || c.LastChunkTime != 200 {
		t.Fatalf("coverage after old import = %+v (aggregate %+v), want newer sync %+v", c.Syncs, c, want)
	}
}
