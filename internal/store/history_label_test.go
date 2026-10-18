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

// Characterization of the current rule: Complete is true when ANY chunk reached 100 (MAX(progress)>=100), even when
// a later chunk is below it. The live archive has 2 of 57 chunks at 100, so this rule is the open question in
// PROGRESS.md; this test pins it so a change of rule is a deliberate edit of this test.
func TestHistoryCompleteWhenAnyChunkReachesHundred(t *testing.T) {
	s := open(t)
	insertChunks(t, s, 10, 100, 57)
	c, err := s.HistoryCoverage(context.Background())
	if err != nil || !c.Complete || c.LastProgress != 57 {
		t.Fatalf("coverage=%+v err=%v; want Complete=true, LastProgress=57 under the current rule", c, err)
	}
}
