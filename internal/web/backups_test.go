package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/health"
	"whatsapp-archiver/internal/testkit"
)

func containsText(s, sub string) bool { return strings.Contains(s, sub) }

type fakeNotifier struct {
	mu     sync.Mutex
	titles []string
	err    error
}

func (f *fakeNotifier) Notify(_ context.Context, title, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.titles = append(f.titles, title)
	return f.err
}

func (f *fakeNotifier) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.titles...)
}

type backupsRig struct {
	cfg  *config.Config
	snap health.Snapshot
	n    *fakeNotifier
	h    http.Handler
}

func newBackupsRig(t *testing.T, withNotifier bool) *backupsRig {
	t.Helper()
	r := &backupsRig{n: &fakeNotifier{}, cfg: &config.Config{
		DataDir: "/var/lib/whatsapp-archiver", TimeZone: "Asia/Riyadh",
		Backup: config.BackupConfig{Destination: "/mnt/data/backups/whatsapp-archiver", Time: "03:30", Keep: 7},
	}}
	d := Deps{Store: testkit.Store(t), Config: r.cfg, Health: func() health.Snapshot { return r.snap }, Now: func() time.Time { return dashNow }}
	if withNotifier {
		d.Notifier = r.n
	}
	r.h = New(d).Handler()
	return r
}

func (r *backupsRig) page(t *testing.T) string {
	t.Helper()
	body, code, _ := get(t, r.h, "/backups")
	if code != http.StatusOK {
		t.Fatalf("GET /backups: %d %s", code, body)
	}
	return visibleText(string(body))
}

func TestBackupsPageShowsScheduleDestinationAndLastResult(t *testing.T) {
	r := newBackupsRig(t, true)
	r.snap.LastBackup = time.Date(2026, 10, 20, 0, 30, 0, 0, time.UTC)
	r.snap.LastBackupPath = "/mnt/data/backups/whatsapp-archiver/20261020T003000Z/archive.db"
	p := r.page(t)
	contains(t, p, "Backups", "every day at 03:30, keeping 7 snapshots", "/mnt/data/backups/whatsapp-archiver",
		"2026-10-20 03:30 +03", "Succeeded", "20261020T003000Z/archive.db")
}

func TestBackupsPageShowsFailedResult(t *testing.T) {
	r := newBackupsRig(t, true)
	r.snap.LastBackup = time.Date(2026, 10, 20, 0, 30, 0, 0, time.UTC)
	r.snap.LastBackupErr = "destination not writable"
	p := r.page(t)
	contains(t, p, "Failed", "destination not writable")
	if containsText(p, "Succeeded") {
		t.Errorf("a failed backup shows Succeeded:\n%s", p)
	}
}

func TestBackupsPageBeforeAnyBackup(t *testing.T) {
	contains(t, newBackupsRig(t, true).page(t), "No backup has run since the archiver started")
}

func TestBackupsPageSaysWhetherNtfyIsConfigured(t *testing.T) {
	contains(t, newBackupsRig(t, true).page(t), "Phone alerts (ntfy) configured")
	p := newBackupsRig(t, false).page(t)
	contains(t, p, "Phone alerts (ntfy) not configured")
	if containsText(p, "Send test alert") {
		t.Errorf("test button offered without ntfy:\n%s", p)
	}
}

func TestBackupsPageHasNoRestoreButton(t *testing.T) {
	r := newBackupsRig(t, true)
	body, _, _ := get(t, r.h, "/backups")
	s := string(body)
	for _, f := range strings.Split(s, "<form")[1:] {
		tag := f[:strings.Index(f, ">")]
		if !strings.Contains(tag, `action="/backups/test-alert"`) {
			t.Errorf("a form on the backups page posts somewhere else: <form%s>", tag)
		}
	}
	if strings.Count(s, "<form") != 1 {
		t.Errorf("want exactly the test-alert form, got %d forms", strings.Count(s, "<form"))
	}
	if strings.Contains(strings.ToLower(s), "restore</button") || strings.Contains(s, "/restore") {
		t.Errorf("restore control on the page:\n%s", s)
	}
	contains(t, visibleText(s), "To restore", "session.db is never backed up", "archiver status --data", "/mnt/data/backups/whatsapp-archiver")
}

func TestTestAlertSendsOnce(t *testing.T) {
	r := newBackupsRig(t, true)
	body, code := postForm(t, r.h, "/backups/test-alert", nil)
	if code != http.StatusOK {
		t.Fatalf("status %d: %s", code, body)
	}
	if got := r.n.calls(); len(got) != 1 || got[0] != "Test alert from WhatsApp Archiver" {
		t.Fatalf("notifier calls = %q, want one \"Test alert from WhatsApp Archiver\"", got)
	}
	contains(t, visibleText(body), "Sent")
	if !containsText(body, `class="ok"`) || containsText(body, `class="bad"`) {
		t.Fatalf("a sent test alert is not shown as ok:\n%s", body)
	}
}

func TestTestAlertShowsTheError(t *testing.T) {
	r := newBackupsRig(t, true)
	r.n.err = errors.New("ntfy status 403 Forbidden")
	body, code := postForm(t, r.h, "/backups/test-alert", nil)
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	p := visibleText(body)
	contains(t, p, "ntfy status 403 Forbidden")
	if body, _ := postForm(t, r.h, "/backups/test-alert", nil); !containsText(body, `class="bad"`) {
		t.Errorf("a failed test alert is not shown as an error:\n%s", body)
	}
	if containsText(p, "Sent") {
		t.Errorf("failure reported as sent:\n%s", p)
	}
}

func TestTestAlertWithoutNtfySendsNothing(t *testing.T) {
	r := newBackupsRig(t, false)
	body, code := postForm(t, r.h, "/backups/test-alert", nil)
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	contains(t, visibleText(body), "not configured")
	if got := r.n.calls(); len(got) != 0 {
		t.Fatalf("notifier called: %q", got)
	}
}

func TestViewingBackupsNeverSendsAnAlert(t *testing.T) {
	r := newBackupsRig(t, true)
	r.page(t)
	if _, code, _ := get(t, r.h, "/backups/test-alert"); code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /backups/test-alert = %d, want 405", code)
	}
	if got := r.n.calls(); len(got) != 0 {
		t.Fatalf("a GET sent %q", got)
	}
}

func TestTestAlertNeedsSameOrigin(t *testing.T) {
	r := newBackupsRig(t, true)
	req := httptest.NewRequest(http.MethodPost, "/backups/test-alert", nil)
	req.Host = "localhost:8080"
	req.Header.Set("Origin", "http://evil.example")
	w := httptest.NewRecorder()
	r.h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden || len(r.n.calls()) != 0 {
		t.Fatalf("cross-origin POST: %d, calls %q", w.Code, r.n.calls())
	}
}

func TestBackupsPageUsesTheLiveSchedule(t *testing.T) {
	r := newBackupsRig(t, true)
	st := testkit.Store(t)
	if err := st.SetBackupSchedule(t.Context(), config.BackupConfig{Time: "01:15", Keep: 3}); err != nil {
		t.Fatal(err)
	}
	r.h = New(Deps{Store: st, Config: r.cfg, Notifier: r.n, Now: func() time.Time { return dashNow }}).Handler()
	contains(t, r.page(t), "every day at 01:15, keeping 3 snapshots")
}
