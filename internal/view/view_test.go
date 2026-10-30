package view

import (
	"strings"
	"testing"
	"time"

	"whatsapp-archiver/internal/store"
)

func TestHumanSize(t *testing.T) {
	for _, c := range []struct {
		n    int64
		want string
	}{
		{0, "0.0 B"}, {1023, "1023.0 B"}, {1024, "1.0 KB"}, {1536, "1.5 KB"}, {1<<20 - 1024, "1023.0 KB"}, {1 << 20, "1.0 MB"},
		{5<<20 + 1<<19, "5.5 MB"}, {1 << 30, "1.0 GB"}, {3 << 30, "3.0 GB"}, {1 << 40, "1024.0 GB"},
	} {
		if got := HumanSize(c.n); got != c.want {
			t.Errorf("HumanSize(%d)=%q want %q", c.n, got, c.want)
		}
	}
}

func TestHistoryLinesSeparateSyncTypesAndStallAtFortyEightHours(t *testing.T) {
	last := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	c := store.Coverage{Syncs: []store.SyncCoverage{
		{SyncType: "FULL", ReceivedChunks: 2, ImportedChunks: 1, LastProgress: 57, LastChunkTime: last.Unix()},
		{SyncType: "RECENT", ReceivedChunks: 1, ImportedChunks: 1, LastProgress: 100, LastChunkTime: last.Add(time.Hour).Unix(), Complete: true},
	}}
	before := strings.Join(HistoryLinesAt(time.UTC, c, last.Add(48*time.Hour-time.Second)), "\n")
	if !strings.Contains(before, "history FULL: in progress (57%)") || strings.Contains(before, "stalled") {
		t.Fatalf("before boundary = %q", before)
	}
	at := HistoryLinesAt(time.UTC, c, last.Add(48*time.Hour))
	joined := strings.Join(at, "\n")
	if !strings.Contains(joined, "history FULL: stalled at 57% (no chunk for 48 h)") || strings.Contains(joined, "history FULL: in progress") {
		t.Fatalf("at boundary = %q", joined)
	}
	if !strings.Contains(joined, "history RECENT: complete") || len(at) != 2 {
		t.Fatalf("per-type lines = %#v", at)
	}
}
