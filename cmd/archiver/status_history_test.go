package main

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"whatsapp-archiver/internal/store"
)

// An old INITIAL_BOOTSTRAP that reached 100 does not make history complete while the newer FULL sync is at 57.
func TestStatusHistoryLineFollowsTheLatestSync(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "archive.db"))
	mustDo(t, err)
	_, err = st.InsertMessage(context.Background(), msg(familyJID, "H1", "history", "2024-03-01T21:30:00Z"))
	mustDo(t, err)
	at := utc("2026-10-10T08:00:00Z")
	for _, c := range []store.HistoryChunk{
		{ID: "b1", SyncType: "INITIAL_BOOTSTRAP", Progress: 100, ReceivedAt: at},
		{ID: "f1", SyncType: "FULL", Progress: 57, ReceivedAt: at + 600},
	} {
		mustDo(t, st.InsertHistoryChunk(context.Background(), c))
	}
	mustDo(t, st.Close())
	writeFile(t, filepath.Join(dir, "config.yaml"), "time_zone: UTC\n")
	want := "history: in progress (57%), chunks 2, last chunk 2026-10-10 08:10 UTC"
	code, out, errOut := runCmd(t, "status", "--data", dir)
	if code != 0 || !slices.Contains(lines(out), want) {
		t.Fatalf("code=%d stderr=%q want line %q in\n%s", code, errOut, want, out)
	}
}
