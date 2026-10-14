package backup

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/store"
)

func TestSnapshotVerifiesAndNeverIncludesSession(t *testing.T) {
	data := t.TempDir()
	st, err := store.Open(filepath.Join(data, "archive.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = st.InsertMessage(context.Background(), store.Message{ChatJID: "g@g.us", ID: "A", Source: "live", Timestamp: 1, ReceivedAt: 1, RawProto: []byte("x")})
	_ = st.Close()
	if err = os.WriteFile(filepath.Join(data, "session.db"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{DataDir: data, Backup: config.BackupConfig{Destination: filepath.Join(t.TempDir(), "backups"), Keep: 2}}
	report, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = Verify(report.Snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(filepath.Dir(report.Snapshot), "session.db")); !os.IsNotExist(err) {
		t.Fatal("session backed up")
	}
}
func TestVerifyRejectsTruncated(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bad.db")
	_ = os.WriteFile(p, []byte("bad"), 0o600)
	if Verify(p) == nil {
		t.Fatal("expected failure")
	}
}

// seededConfig returns a config whose data folder holds an archive.db with n messages.
func seededConfig(t *testing.T, dest string, keep, n int) (*config.Config, *store.Store) {
	t.Helper()
	data := t.TempDir()
	st, err := store.Open(filepath.Join(data, "archive.db"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if _, err = st.InsertMessage(context.Background(), store.Message{ChatJID: "g@g.us", ID: fmt.Sprint("M", i), Source: "live", Timestamp: int64(i), ReceivedAt: int64(i), RawProto: []byte("x")}); err != nil {
			t.Fatal(err)
		}
	}
	return &config.Config{DataDir: data, Backup: config.BackupConfig{Destination: dest, Keep: keep}}, st
}

func countMessages(t *testing.T, path string) int {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var n int
	if err = db.QueryRow("SELECT COUNT(*) FROM messages").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func TestRetentionOnlyRemovesSnapshotFolders(t *testing.T) {
	dest := t.TempDir()
	old := []string{"20200101T000000.000000000Z", "20210101T000000.000000000Z"}
	foreign := []string{"0-keep-me", "2023-family-photos", "media", "zzz-after", "20230101"}
	for _, d := range append(append([]string{}, old...), foreign...) {
		if err := os.MkdirAll(filepath.Join(dest, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dest, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, st := seededConfig(t, dest, 1, 1)
	_ = st.Close()
	report, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, n := range names(t, dest) {
		got[n] = true
	}
	for _, f := range append(foreign, "notes.txt", filepath.Base(filepath.Dir(report.Snapshot))) {
		if !got[f] {
			t.Errorf("%s was removed", f)
		}
	}
	for _, o := range old {
		if got[o] {
			t.Errorf("old snapshot %s kept beyond Keep", o)
		}
	}
}

func TestKeepTwoLeavesTwoSnapshots(t *testing.T) {
	dest := t.TempDir()
	cfg, st := seededConfig(t, dest, 2, 1)
	_ = st.Close()
	var snaps []string
	for i := 0; i < 3; i++ {
		report, err := Run(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		snaps = append(snaps, filepath.Base(filepath.Dir(report.Snapshot)))
	}
	got := names(t, dest)
	if len(got) != 2 || got[0] != snaps[1] || got[1] != snaps[2] {
		t.Fatalf("destination = %v, want the newest two %v", got, snaps[1:])
	}
}

func TestFailedSnapshotLeavesNoFolder(t *testing.T) {
	dest := t.TempDir()
	cfg := &config.Config{DataDir: filepath.Join(t.TempDir(), "missing"), Backup: config.BackupConfig{Destination: dest, Keep: 3}}
	if _, err := Run(context.Background(), cfg); err == nil {
		t.Fatal("expected an error")
	}
	if got := names(t, dest); len(got) != 0 {
		t.Fatalf("failed snapshot left %v", got)
	}
}

func TestSnapshotRowCountMatchesSource(t *testing.T) {
	cfg, st := seededConfig(t, t.TempDir(), 2, 25)
	_ = st.Close()
	report, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := countMessages(t, report.Snapshot); got != 25 {
		t.Fatalf("snapshot has %d messages, want 25", got)
	}
}

func TestSnapshotDuringConcurrentInserts(t *testing.T) {
	cfg, st := seededConfig(t, t.TempDir(), 2, 10)
	defer func() { _ = st.Close() }()
	ctx, stop := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	started := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ctx.Err() == nil; i++ {
			_, _ = st.InsertMessage(ctx, store.Message{ChatJID: "g@g.us", ID: fmt.Sprint("C", i), Source: "live", Timestamp: int64(i), ReceivedAt: int64(i), RawProto: []byte("x")})
			if i == 0 {
				close(started)
			}
		}
	}()
	<-started
	report, err := Run(context.Background(), cfg)
	stop()
	wg.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if err = Verify(report.Snapshot); err != nil {
		t.Fatal(err)
	}
	if got := countMessages(t, report.Snapshot); got < 10 {
		t.Fatalf("snapshot has %d messages, want at least 10", got)
	}
}

func TestDestinationNotWritableReturnsError(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, st := seededConfig(t, filepath.Join(blocker, "backups"), 2, 1)
	_ = st.Close()
	if _, err := Run(context.Background(), cfg); err == nil {
		t.Fatal("expected an error")
	}
}

func TestVerifyRejectsFailedIntegrityCheck(t *testing.T) {
	p := filepath.Join(t.TempDir(), "corrupt.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(p))
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"PRAGMA journal_mode=DELETE", "PRAGMA page_size=4096", "CREATE TABLE t(a, b)", "CREATE INDEX t_b ON t(b)"} {
		if _, err = db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 50; i++ {
		if _, err = db.Exec("INSERT INTO t VALUES(?,?)", i, i); err != nil {
			t.Fatal(err)
		}
	}
	var root int
	if err = db.QueryRow("SELECT rootpage FROM sqlite_master WHERE name='t_b'").Scan(&root); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if Verify(p) != nil {
		t.Fatal("intact database must verify")
	}
	// replace the index root with an empty index leaf: the file still opens, but rows are missing from the index
	page := make([]byte, 4096)
	page[0], page[5], page[6] = 0x0a, 0x10, 0x00
	f, err := os.OpenFile(p, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteAt(page, int64(root-1)*4096); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if Verify(p) == nil {
		t.Fatal("expected the integrity check to fail")
	}
}

// fakeClock fires the first After immediately; on the second it cancels the schedule and blocks.
type fakeClock struct {
	now    time.Time
	cancel context.CancelFunc
	waits  []time.Duration
}

func (c *fakeClock) Now() time.Time { return c.now }
func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.waits = append(c.waits, d)
	if len(c.waits) == 1 {
		ch := make(chan time.Time, 1)
		ch <- c.now.Add(d)
		return ch
	}
	c.cancel()
	return nil
}

func TestScheduleFiresOnceAtConfiguredTime(t *testing.T) {
	cfg, st := seededConfig(t, t.TempDir(), 7, 1)
	_ = st.Close()
	cfg.Backup.Time = "03:30"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clock := &fakeClock{now: time.Date(2026, 10, 14, 3, 0, 0, 0, time.UTC), cancel: cancel}
	runs := 0
	Schedule(ctx, cfg, clock, func(r Report, err error) {
		runs++
		if err != nil {
			t.Error(err)
		}
	})
	if runs != 1 {
		t.Fatalf("ran %d times, want 1", runs)
	}
	if len(clock.waits) == 0 || clock.waits[0] != 30*time.Minute {
		t.Fatalf("waits = %v, want first wait 30m", clock.waits)
	}
	if got := len(names(t, cfg.Backup.Destination)); got != 1 {
		t.Fatalf("%d snapshot folders, want 1", got)
	}
}

func TestRetentionFailureKeepsNewSnapshot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: directory permissions do not block RemoveAll")
	}
	dest := t.TempDir()
	old := filepath.Join(dest, "20200101T000000.000000000Z")
	if err := os.MkdirAll(old, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "archive.db"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(old, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(old, 0o700) })
	cfg, st := seededConfig(t, dest, 1, 1)
	_ = st.Close()
	report, err := Run(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected the retention error")
	}
	if report.Snapshot == "" {
		t.Fatal("report lacks the snapshot")
	}
	if verr := Verify(report.Snapshot); verr != nil {
		t.Fatalf("new snapshot was not kept: %v", verr)
	}
}
