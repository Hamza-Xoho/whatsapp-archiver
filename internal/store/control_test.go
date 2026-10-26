package store_test

import (
	"context"
	"errors"
	"testing"

	"whatsapp-archiver/internal/store"
)

// Connection periods (W3 reads and writes them; task (b) provides the methods). At most one period is open.
func TestConnectionPeriodsOpenCloseAndOneOpenAtATime(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	a, err := s.OpenConnectionPeriod(ctx, store.ConnectionPeriod{State: "degraded", StartedAt: 100, CauseClass: "dns"})
	if err != nil || a == 0 {
		t.Fatalf("open = %d, %v", a, err)
	}
	// Opening another period ends the open one at the new start time: one open row, no overlap.
	b, err := s.OpenConnectionPeriod(ctx, store.ConnectionPeriod{State: "disconnected", StartedAt: 160, CauseClass: "tcp"})
	if err != nil || b == a {
		t.Fatalf("second open = %d, %v", b, err)
	}
	if n := count(t, s, "SELECT COUNT(*) FROM connection_periods WHERE ended_at IS NULL"); n != 1 {
		t.Fatalf("%d open periods, want 1", n)
	}
	if got := text(t, s, "SELECT ended_at FROM connection_periods WHERE id=?", a); got != "160" {
		t.Fatalf("first period ended_at = %q, want 160", got)
	}
	closed, err := s.CloseConnectionPeriod(ctx, 400)
	if err != nil || !closed {
		t.Fatalf("close = %v, %v", closed, err)
	}
	closed, err = s.CloseConnectionPeriod(ctx, 500)
	if err != nil || closed {
		t.Fatalf("closing with nothing open = %v, %v; want false", closed, err)
	}
	list, err := s.ConnectionPeriods(ctx, 0)
	if err != nil || len(list) != 2 || list[0].ID != a || list[1].EndedAt != 400 || list[1].State != "disconnected" || list[0].CauseClass != "dns" {
		t.Fatalf("periods = %+v, %v", list, err)
	}
	if list, err = s.ConnectionPeriods(ctx, 200); err != nil || len(list) != 1 || list[0].ID != b {
		t.Fatalf("periods since 200 = %+v, %v; want only the second", list, err)
	}
}

func TestConnectionPeriodErrorsAreCounted(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if _, err := s.OpenConnectionPeriod(ctx, store.ConnectionPeriod{State: "reconnecting", StartedAt: 1}); err != nil {
		t.Fatal(err)
	}
	for _, msg := range []string{"dial tcp: timeout", "tls: handshake failure"} {
		if err := s.NoteConnectionError(ctx, msg); err != nil {
			t.Fatal(err)
		}
	}
	if got := text(t, s, "SELECT error_count||':'||last_error FROM connection_periods"); got != "2:tls: handshake failure" {
		t.Fatalf("period = %q", got)
	}
	// With no open period there is nothing to note, and that is not an error.
	_, _ = s.CloseConnectionPeriod(ctx, 5)
	if err := s.NoteConnectionError(ctx, "late"); err != nil {
		t.Fatal(err)
	}
}

// R17: the process-down period is inserted already closed.
func TestClosedConnectionPeriodForProcessDowntime(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if err := s.InsertClosedConnectionPeriod(ctx, store.ConnectionPeriod{State: "not_running", CauseClass: "not_running", StartedAt: 10, EndedAt: 70}); err != nil {
		t.Fatal(err)
	}
	if n := count(t, s, "SELECT COUNT(*) FROM connection_periods WHERE ended_at=70 AND state='not_running'"); n != 1 {
		t.Fatalf("closed period rows = %d", n)
	}
}

// R6: the connection vocabularies are Go lists, not CHECKs.
func TestConnectionPeriodRejectsUnknownStateAndCause(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	for _, p := range []store.ConnectionPeriod{{State: "napping", StartedAt: 1}, {State: "degraded", CauseClass: "vibes", StartedAt: 1}} {
		if _, err := s.OpenConnectionPeriod(ctx, p); !errors.Is(err, store.ErrConstraint) {
			t.Errorf("%+v: err = %v, want a constraint error", p, err)
		}
	}
	if err := s.InsertClosedConnectionPeriod(ctx, store.ConnectionPeriod{State: "napping", StartedAt: 1, EndedAt: 2}); !errors.Is(err, store.ErrConstraint) {
		t.Errorf("closed period: err = %v", err)
	}
	if n := count(t, s, "SELECT COUNT(*) FROM connection_periods"); n != 0 {
		t.Fatalf("%d periods stored", n)
	}
}

// The alert outbox: stored first, then due, then sent (or skipped, or retried later).
func TestAlertOutbox(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	id1, err := s.EnqueueAlert(ctx, store.Alert{Key: "disconnected_after", Kind: "raised", Title: "Disconnected", Body: "for 20 min", RaisedAt: 100})
	if err != nil || id1 == 0 {
		t.Fatalf("enqueue = %d, %v", id1, err)
	}
	id2, err := s.EnqueueAlert(ctx, store.Alert{Key: "disk", Kind: "test", Title: "Test", RaisedAt: 101, NextAttemptAt: 500})
	if err != nil {
		t.Fatal(err)
	}
	due, err := s.DueAlerts(ctx, 200, 10)
	if err != nil || len(due) != 1 || due[0].ID != id1 || due[0].State != "queued" || due[0].Title != "Disconnected" {
		t.Fatalf("due at 200 = %+v, %v", due, err)
	}
	// A failed send stays queued, counts the attempt, and waits until the retry time.
	if err = s.AlertFailed(ctx, id1, "dns: no such host", 400); err != nil {
		t.Fatal(err)
	}
	if due, err = s.DueAlerts(ctx, 300, 10); err != nil || len(due) != 0 {
		t.Fatalf("due at 300 = %+v, %v; want none (retry at 400)", due, err)
	}
	if due, err = s.DueAlerts(ctx, 600, 10); err != nil || len(due) != 2 || due[0].ID != id1 || due[0].Attempts != 1 || due[0].LastError != "dns: no such host" || due[1].ID != id2 {
		t.Fatalf("due at 600 = %+v, %v", due, err)
	}
	if err = s.AlertSent(ctx, id1, 610); err != nil {
		t.Fatal(err)
	}
	if err = s.AlertSkipped(ctx, id2); err != nil {
		t.Fatal(err)
	}
	if due, err = s.DueAlerts(ctx, 9999, 10); err != nil || len(due) != 0 {
		t.Fatalf("due after sending = %+v, %v", due, err)
	}
	if got := text(t, s, "SELECT state||':'||sent_at FROM alerts WHERE id=?", id1); got != "sent:610" {
		t.Fatalf("alert 1 = %q", got)
	}
	if got := text(t, s, "SELECT state FROM alerts WHERE id=?", id2); got != "skipped" {
		t.Fatalf("alert 2 = %q", got)
	}
}

func TestAlertKindIsAClosedVocabulary(t *testing.T) {
	s := open(t)
	if _, err := s.EnqueueAlert(context.Background(), store.Alert{Key: "k", Kind: "panic", Title: "t", RaisedAt: 1}); !errors.Is(err, store.ErrConstraint) {
		t.Fatalf("err = %v, want a constraint error", err)
	}
}
