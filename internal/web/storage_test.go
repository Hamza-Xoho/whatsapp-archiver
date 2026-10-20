package web

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

const gib = int64(1) << 30

type storageRig struct {
	st   *store.Store
	cfg  *config.Config
	now  time.Time
	free uint64
	h    http.Handler
}

// newStorageRig is one server for the whole test, so its cache lives across requests; time is the rig's now.
func newStorageRig(t *testing.T, free int64) *storageRig {
	t.Helper()
	r := &storageRig{st: testkit.Store(t), now: dashNow, free: uint64(free)}
	r.cfg = &config.Config{
		DataDir: t.TempDir(),
		Alerts:  config.AlertConfig{DiskFreeMinGB: 5},
		Backup:  config.BackupConfig{Destination: t.TempDir()},
	}
	r.h = New(Deps{Store: r.st, Config: r.cfg, Now: func() time.Time { return r.now },
		DiskFree: func(string) (uint64, error) { return r.free, nil }}).Handler()
	return r
}

func (r *storageRig) page(t *testing.T) string {
	t.Helper()
	body, code, _ := get(t, r.h, "/storage")
	if code != http.StatusOK {
		t.Fatalf("GET /storage: %d %s", code, body)
	}
	return visibleText(string(body))
}

func (r *storageRig) file(t *testing.T, path string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, n), 0o600); err != nil {
		t.Fatal(err)
	}
}

// growth makes the archive grow bytes a week: bytes*4 of done media in the last 28 days.
func (r *storageRig) growth(t *testing.T, bytes int64) {
	t.Helper()
	m := store.Media{ChatJID: "g@g.us", MessageID: "big", Type: "video", DeclaredSize: bytes * 4, State: "done", UpdatedAt: r.now.Add(-time.Hour).Unix(), Source: "live"}
	if err := r.st.InsertMedia(t.Context(), m); err != nil {
		t.Fatal(err)
	}
}

func contains(t *testing.T, page string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !containsText(page, w) {
			t.Errorf("page lacks %q:\n%s", w, page)
		}
	}
}

func TestProjectionDaysUntilFull(t *testing.T) {
	r := newStorageRig(t, 10*gib)
	r.growth(t, 7*gib) // 1 GB a day
	r.file(t, filepath.Join(r.cfg.DataDir, "media", "a", "x.jpg"), 3072)
	r.file(t, filepath.Join(r.cfg.Backup.Destination, "20261020", "archive.db"), 5120)
	p := r.page(t)
	contains(t, p, "Storage", "3.0 KB", "5.0 KB", "10.0 GB", "7.0 GB a week", "10 days",
		"Warning: free space is projected to reach the 5 GB alert level in about 5 days")
}

func TestProjectionMoreThanAYear(t *testing.T) {
	r := newStorageRig(t, 1000*gib)
	r.growth(t, 7*gib)
	p := r.page(t)
	contains(t, p, "more than a year")
	if containsText(p, "Warning") {
		t.Errorf("a year of room warned:\n%s", p)
	}
}

func TestProjectionBoundaries(t *testing.T) {
	cases := []struct {
		free   uint64
		weekly int64
		want   string
	}{
		{365, 7, "365 days"},
		{366, 7, "more than a year"},
		{1, 7, "1 day"},
		{0, 7, "0 days"},
		{100, 0, "no growth measured yet"},
		{100, -5, "no growth measured yet"},
	}
	for _, c := range cases {
		if got := projectionText(c.free, c.weekly); got != c.want {
			t.Errorf("projectionText(%d, %d) = %q, want %q", c.free, c.weekly, got, c.want)
		}
	}
}

func TestProjectionWarning(t *testing.T) {
	min := uint64(5 * gib)
	cases := []struct {
		name   string
		free   uint64
		weekly int64
		want   bool
	}{
		{"already under the alert level", 4 * uint64(gib), 0, true},
		{"29 days to the alert level", 5*uint64(gib) + 29*uint64(gib), 7 * gib, true},
		{"30 days to the alert level", 5*uint64(gib) + 30*uint64(gib), 7 * gib, false},
		{"exactly at the alert level", 5 * uint64(gib), 0, true},
		{"no growth", 100 * uint64(gib), 0, false},
	}
	for _, c := range cases {
		if got := projectionWarn(c.free, c.weekly, min); got != c.want {
			t.Errorf("%s: projectionWarn = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestStorageIsComputedAtMostEveryFiveMinutes(t *testing.T) {
	r := newStorageRig(t, 100*gib)
	media := filepath.Join(r.cfg.DataDir, "media", "a", "x.jpg")
	r.file(t, media, 1024)
	contains(t, r.page(t), "1.0 KB")
	r.file(t, media, 2048)
	r.now = r.now.Add(5*time.Minute - time.Second)
	if p := r.page(t); !containsText(p, "1.0 KB") || containsText(p, "2.0 KB") {
		t.Fatalf("recomputed inside the five minutes:\n%s", p)
	}
	r.now = r.now.Add(time.Second)
	if p := r.page(t); !containsText(p, "2.0 KB") {
		t.Fatalf("not recomputed after five minutes:\n%s", p)
	}
}

func TestStorageWithoutBackupDestination(t *testing.T) {
	r := newStorageRig(t, 100*gib)
	r.cfg.Backup.Destination = ""
	contains(t, r.page(t), "Backup folder", "not set")
}

func TestStorageHasNoDeleteButtons(t *testing.T) {
	r := newStorageRig(t, 100*gib)
	body, _, _ := get(t, r.h, "/storage")
	if containsText(string(body), "<form") || containsText(string(body), "<button") {
		t.Fatalf("storage page has a form or button:\n%s", body)
	}
}

func TestStorageWarnsWhenAlreadyUnderTheAlertLevel(t *testing.T) {
	r := newStorageRig(t, 4*gib)
	contains(t, r.page(t), "Warning: free space is already at or under the 5 GB alert level.")
}

func TestStorageWarningUsesTheLiveAlertLevel(t *testing.T) {
	r := newStorageRig(t, 10*gib)
	if err := r.st.SetAlertThresholds(t.Context(), config.AlertConfig{NoEventsAfter: time.Hour, DisconnectedAfter: time.Hour, PhoneQuietAfter: time.Hour, DiskFreeMinGB: 20}); err != nil {
		t.Fatal(err)
	}
	contains(t, r.page(t), "already at or under the 20 GB alert level")
}

func TestStorageWithoutFreeSpaceReading(t *testing.T) {
	r := newStorageRig(t, 10*gib)
	r.h = New(Deps{Store: r.st, Config: r.cfg, Now: func() time.Time { return r.now },
		DiskFree: func(string) (uint64, error) { return 0, fmt.Errorf("no such disk") }}).Handler()
	contains(t, r.page(t), "Free disk space unavailable", "Disk full in unknown")
}

func TestStorageListsLargestChatsWithNamesAndStopsAtTen(t *testing.T) {
	r := newStorageRig(t, 100*gib)
	for i := range 12 {
		m := store.Media{ChatJID: fmt.Sprintf("c%02d@g.us", i), MessageID: "m", Type: "image", DeclaredSize: int64(i+1) << 20, State: "done", UpdatedAt: r.now.Unix(), Source: "live"}
		if err := r.st.InsertMedia(t.Context(), m); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.st.UpsertChat(t.Context(), store.Chat{JID: "c11@g.us", Name: "Family"}); err != nil {
		t.Fatal(err)
	}
	p := r.page(t)
	contains(t, p, "Family c11@g.us 12.0 MB", "c02@g.us 3.0 MB")
	for _, gone := range []string{"c00@g.us", "c01@g.us"} {
		if containsText(p, gone) {
			t.Errorf("%s is not in the ten largest:\n%s", gone, p)
		}
	}
	contains(t, p, "image 78.0 MB")
}
