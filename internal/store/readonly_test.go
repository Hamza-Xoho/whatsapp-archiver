package store_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"whatsapp-archiver/internal/store"
)

// folderState lists the files in dir with their sizes and modification times.
func folderState(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("%s %s %d", e.Name(), info.ModTime().UTC().Format(time.RFC3339Nano), info.Size()))
	}
	return out
}

func seeded(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "archive.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.InsertMessage(context.Background(), message("g@g.us", "A", time.Unix(5, 0))); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	if err = os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestOpenReadOnlyMissingArchiveFailsAndCreatesNothing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "typo", "archive.db")
	s, err := store.OpenReadOnly(path)
	if err == nil {
		_ = s.Close()
		t.Fatal("OpenReadOnly of a missing archive succeeded")
	}
	if want := "no archive at " + path + ": run archiver first"; err.Error() != want {
		t.Fatalf("err=%q want %q", err, want)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("OpenReadOnly created %v", entries)
	}
}

func TestOpenReadOnlyReadsWithoutTouchingTheFolder(t *testing.T) {
	path := seeded(t)
	dir := filepath.Dir(path)
	before := folderState(t, dir)
	s, err := store.OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	stats, err := s.ChatStats(context.Background())
	if err != nil || len(stats) != 1 || stats[0].MessageCount != 1 {
		t.Fatalf("stats=%+v err=%v", stats, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	if after := folderState(t, dir); !slices.Equal(before, after) {
		t.Fatalf("folder changed:\nbefore %v\nafter  %v", before, after)
	}
}

func TestOpenReadOnlyStoreRejectsWrites(t *testing.T) {
	path := seeded(t)
	s, err := store.OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()
	if ok, err := s.InsertMessage(ctx, message("g@g.us", "B", time.Unix(6, 0))); err == nil || ok {
		t.Fatalf("InsertMessage inserted=%v err=%v", ok, err)
	}
	if err = s.UpsertChat(ctx, store.Chat{JID: "g@g.us", Name: "x"}); err == nil {
		t.Fatal("UpsertChat succeeded on a read-only store")
	}
	if _, err = s.InsertEvent(ctx, store.MessageEvent{ChatJID: "g@g.us", TargetID: "A", Kind: "edit", EventID: "E"}); err == nil {
		t.Fatal("InsertEvent succeeded on a read-only store")
	}
}

// While the service runs, archive.db is in WAL mode with -wal and -shm present; a reader must still see
// committed rows without stopping the writer.
func TestOpenReadOnlyReadsWhileTheWriterIsOpen(t *testing.T) {
	w := open(t)
	ctx := context.Background()
	if _, err := w.InsertMessage(ctx, message("g@g.us", "A", time.Unix(5, 0))); err != nil {
		t.Fatal(err)
	}
	r, err := store.OpenReadOnly(w.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	if _, err = w.InsertMessage(ctx, message("g@g.us", "B", time.Unix(6, 0))); err != nil {
		t.Fatal(err)
	}
	stats, err := r.ChatStats(ctx)
	if err != nil || len(stats) != 1 || stats[0].MessageCount != 2 {
		t.Fatalf("stats=%+v err=%v", stats, err)
	}
}

func TestOpenReadOnlyRejectsAFileThatIsNotAnArchive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archive.db")
	if err := os.WriteFile(path, []byte("not a database, just text that is long enough to look odd"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := store.OpenReadOnly(path)
	if err == nil {
		_, err = s.ChatStats(context.Background())
		_ = s.Close()
	}
	if err == nil {
		t.Fatal("a text file was read as an archive")
	}
}

func TestChatStatsCountsMediaByState(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if _, err := s.InsertMessage(ctx, message("g@g.us", "A", time.Unix(5, 0))); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertMessage(ctx, message("other@g.us", "Z", time.Unix(5, 0))); err != nil {
		t.Fatal(err)
	}
	states := map[string]string{"f1": "failed", "f2": "failed", "e1": "expired", "r1": "retry_requested", "p1": "pending", "d1": "downloading", "ok": "done", "sk": "skipped"}
	for id, state := range states {
		if err := s.InsertMedia(ctx, store.Media{ChatJID: "g@g.us", MessageID: id, State: state}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.InsertMedia(ctx, store.Media{ChatJID: "other@g.us", MessageID: "o", State: "failed"}); err != nil {
		t.Fatal(err)
	}
	stats, err := s.ChatStats(ctx)
	if err != nil || len(stats) != 2 {
		t.Fatalf("stats=%+v err=%v", stats, err)
	}
	g := stats[0]
	if !strings.HasPrefix(g.CanonicalJID, "g@") {
		g = stats[1]
	}
	// failed rows only; expired includes a request waiting for the phone's answer; pending includes a running download.
	if g.MediaFailed != 2 || g.MediaExpired != 2 || g.MediaPending != 2 {
		t.Fatalf("failed=%d expired=%d pending=%d, want 2/2/2 (%+v)", g.MediaFailed, g.MediaExpired, g.MediaPending, g)
	}
}

func TestCoverageIsCompleteWhenAnyChunkReachedHundred(t *testing.T) {
	for _, c := range []struct {
		name     string
		progress []int
		want     bool
	}{{"none", nil, false}, {"partial", []int{20, 60}, false}, {"hundred then lower", []int{100, 40}, true}, {"hundred last", []int{40, 100}, true}} {
		t.Run(c.name, func(t *testing.T) {
			s := open(t)
			for i, p := range c.progress {
				if err := s.InsertHistoryChunk(context.Background(), store.HistoryChunk{ID: fmt.Sprint("c", i), Progress: p, ReceivedAt: int64(100 + i)}); err != nil {
					t.Fatal(err)
				}
			}
			cov, err := s.HistoryCoverage(context.Background())
			if err != nil || cov.Complete != c.want {
				t.Fatalf("complete=%v want %v err=%v", cov.Complete, c.want, err)
			}
		})
	}
}
