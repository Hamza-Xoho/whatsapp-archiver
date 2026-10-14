package health

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"
	"whatsapp-archiver/internal/backup"
	"whatsapp-archiver/internal/config"
)

// rig is a Monitor on a fake clock with a recording notifier, running until stop.
type rig struct {
	m    *Monitor
	n    *note
	c    *clock
	free uint64
	stop func()
}

func newRig(t *testing.T, alerts config.AlertConfig) *rig {
	t.Helper()
	r := &rig{n: &note{}, c: &clock{time.Unix(1_700_000_000, 0).UTC()}, free: 99 << 30}
	cfg := &config.Config{DataDir: t.TempDir(), Alerts: alerts}
	r.m = NewMonitor(cfg, r.n, r.c, func(string) (uint64, error) { return r.free, nil }, func() string { return "2.3000.1" })
	r.stop = running(t, r.m)
	return r
}

// running runs m until the returned stop is called (or the test ends); stop returns once Run has returned.
func running(t *testing.T, m *Monitor) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); m.Run(ctx) }()
	stop = func() { cancel(); <-done }
	t.Cleanup(stop)
	return stop
}

// sent stops the monitor and returns every alert body it handed to the notifier.
func (r *rig) sent() []string          { r.stop(); return r.n.bodies }
func (r *rig) advance(d time.Duration) { r.c.now = r.c.now.Add(d) }

func wantBodies(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("alerts=%q, want %d matching %q", got, len(want), want)
	}
	for i := range want {
		if !strings.Contains(got[i], want[i]) {
			t.Fatalf("alert %d = %q, want it to contain %q (all: %q)", i, got[i], want[i], got)
		}
	}
}

func TestLoggedOutAlertsOnceAndIsReported(t *testing.T) {
	r := newRig(t, config.AlertConfig{})
	r.m.SetState("logged_out", "401")
	r.advance(time.Hour)
	r.m.SetState("logged_out", "401")
	if s := r.m.Status(); s.State != "logged_out" || s.Detail != "401" {
		t.Fatalf("status=%+v", s)
	}
	wantBodies(t, r.sent(), "401")
}

func TestOutdatedAlertNamesVersion(t *testing.T) {
	r := newRig(t, config.AlertConfig{})
	r.m.SetState("client_outdated", "")
	wantBodies(t, r.sent(), "2.3000.1")
}

func TestTempBanAlertIncludesExpiry(t *testing.T) {
	r := newRig(t, config.AlertConfig{})
	r.m.SetState("temp_banned", "24h0m0s")
	got := r.sent()
	wantBodies(t, got, "24h0m0s")
	if !strings.Contains(got[0], "paused") {
		t.Fatalf("temp ban alert %q must say capture is paused", got[0])
	}
}

func TestStateAlertRecoversWhenConnected(t *testing.T) {
	r := newRig(t, config.AlertConfig{})
	r.m.SetState("stream_replaced", "")
	r.m.SetState("connected", "")
	r.m.SetState("connected", "")
	wantBodies(t, r.sent(), "", "ecovered")
}

func TestDisconnectedLongerThanThresholdAlerts(t *testing.T) {
	r := newRig(t, config.AlertConfig{DisconnectedAfter: 15 * time.Minute})
	r.m.SetState("connected", "")
	r.advance(time.Hour)
	r.m.SetState("disconnected", "")
	r.advance(14 * time.Minute)
	r.m.check()
	r.advance(6 * time.Minute) // 20 minutes down
	r.m.check()
	r.advance(time.Minute)
	r.m.check()
	r.m.SetState("connected", "")
	r.m.check()
	wantBodies(t, r.sent(), "20m", "ecovered")
}

func TestDisconnectedBeforeFirstConnectMeasuredFromStart(t *testing.T) {
	r := newRig(t, config.AlertConfig{DisconnectedAfter: 15 * time.Minute})
	r.advance(10 * time.Minute)
	r.m.SetState("disconnected", "")
	r.advance(6 * time.Minute)
	r.m.check()
	wantBodies(t, r.sent(), "16m")
}

func TestRecoveredMessageAfterEventsResume(t *testing.T) {
	r := newRig(t, config.AlertConfig{NoEventsAfter: 6 * time.Hour})
	r.advance(6*time.Hour + time.Minute) // no Seen since start
	r.m.check()
	r.advance(time.Minute)
	r.m.check()
	r.m.Seen(r.c.now, false)
	r.advance(time.Minute)
	r.m.check()
	r.m.Seen(r.c.now, false)
	wantBodies(t, r.sent(), "", "ecovered")
}

func TestPhoneQuietAlert(t *testing.T) {
	r := newRig(t, config.AlertConfig{PhoneQuietAfter: 240 * time.Hour})
	for i := 0; i < 241; i++ { // 10 days and 1 hour of messages from others only
		r.advance(time.Hour)
		r.m.Seen(r.c.now, false)
		r.m.check()
	}
	r.m.Seen(r.c.now, true)
	r.m.check()
	wantBodies(t, r.sent(), "open WhatsApp on your phone to avoid the 14-day unlink", "ecovered")
}

func TestDiskBelowThresholdAlertsOnceThenRecovers(t *testing.T) {
	r := newRig(t, config.AlertConfig{DiskFreeMinGB: 5})
	r.free = 1 << 30
	r.m.check()
	r.advance(time.Minute)
	r.m.check()
	r.free = 10 << 30
	r.m.check()
	r.m.check()
	wantBodies(t, r.sent(), "1073741824", "ecovered")
}

func TestWriteFailedAndPanicAlertOncePerSixHours(t *testing.T) {
	r := newRig(t, config.AlertConfig{})
	r.m.WriteFailed(errors.New("disk I/O error"))
	r.m.Panic("handler")
	r.advance(time.Hour)
	r.m.WriteFailed(errors.New("disk I/O error"))
	r.m.Panic("handler")
	if p := r.m.Status().Panics; p != 2 {
		t.Fatalf("panics=%d", p)
	}
	r.advance(5 * time.Hour)
	r.m.WriteFailed(errors.New("disk I/O error"))
	r.m.Panic("handler")
	wantBodies(t, r.sent(), "disk I/O error", "handler", "disk I/O error", "handler")
}

func TestFailedBackupAlertNamesErrorThenRecovers(t *testing.T) {
	r := newRig(t, config.AlertConfig{})
	r.m.BackupResult(backup.Report{}, errors.New("destination not writable"))
	r.m.BackupResult(backup.Report{}, nil)
	r.m.BackupResult(backup.Report{}, nil)
	wantBodies(t, r.sent(), "destination not writable", "ecovered")
}

func TestHealthJSONRoundTrip(t *testing.T) {
	r := newRig(t, config.AlertConfig{})
	r.m.Seen(r.c.now, true)
	r.advance(time.Minute)
	r.m.Panic("media")
	r.m.SetState("temp_banned", "1h0m0s")
	data, err := os.ReadFile(filepath.Join(r.m.cfg.DataDir, "health.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got Snapshot
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if want := r.m.Status(); got != want || got.State != "temp_banned" || got.Panics != 1 {
		t.Fatalf("health.json=%+v status=%+v", got, want)
	}
}

func TestRunLeavesNoGoroutines(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	r := newRig(t, config.AlertConfig{})
	r.m.SetState("logged_out", "")
	r.m.WriteFailed(errors.New("x"))
	r.stop()
}
