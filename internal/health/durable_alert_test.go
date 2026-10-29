package health

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/testkit"
)

func TestNtfyDownTwoHoursDeliversAlertThenRecoveryInOrder(t *testing.T) {
	clock := &clock{time.Unix(1_700_000_000, 0).UTC()}
	note := &recoveringNotifier{}
	store := testkit.Store(t)
	m := NewMonitor(&config.Config{DataDir: t.TempDir(), Alerts: config.AlertConfig{DisconnectedAfter: 5 * time.Minute}}, note, clock, nil, func() string { return "v" }).
		WithAlertStore(store)

	m.SetState("connected", "")
	m.SetState("disconnected", "network")
	clock.now = clock.now.Add(6 * time.Minute)
	m.check() // durable raised alert
	if err := m.sendDueAlerts(context.Background()); err == nil {
		t.Fatal("first send succeeded while ntfy was down")
	}

	clock.now = clock.now.Add(2 * time.Hour)
	m.SetState("connected", "") // durable recovery, queued behind the raised alert
	note.up = true
	if err := m.sendDueAlerts(context.Background()); err != nil {
		t.Fatal(err)
	}

	if len(note.bodies) != 2 {
		t.Fatalf("delivered=%q", note.bodies)
	}
	if !strings.Contains(note.bodies[0], "Disconnected for") || !strings.Contains(strings.ToLower(note.bodies[0]), "delivered late") {
		t.Fatalf("first delivery=%q", note.bodies[0])
	}
	if !strings.Contains(note.bodies[1], "Recovered") {
		t.Fatalf("second delivery=%q", note.bodies[1])
	}
}

type recoveringNotifier struct {
	up     bool
	bodies []string
}

func (n *recoveringNotifier) Notify(_ context.Context, _, body string) error {
	if !n.up {
		return errors.New("ntfy unavailable")
	}
	n.bodies = append(n.bodies, body)
	return nil
}
