package health

import (
	"context"
	"testing"
	"time"
	"whatsapp-archiver/internal/config"
)

type note struct{ bodies []string }

func (n *note) Notify(_ context.Context, _, body string) error {
	n.bodies = append(n.bodies, body)
	return nil
}

type clock struct{ now time.Time }

func (c *clock) Now() time.Time { return c.now }
func TestImmediateStateAlertsAreGated(t *testing.T) {
	dir := t.TempDir()
	n := &note{}
	c := &clock{time.Unix(1, 0)}
	m := NewMonitor(&config.Config{DataDir: dir, Alerts: config.AlertConfig{}}, n, c, func(string) (uint64, error) { return 99 << 30, nil }, func() string { return "2.3000.1" })
	m.SetState("client_outdated", "")
	m.SetState("client_outdated", "")
	if len(n.bodies) != 1 || m.Status().State != "client_outdated" {
		t.Fatalf("notes=%v status=%+v", n.bodies, m.Status())
	}
}
