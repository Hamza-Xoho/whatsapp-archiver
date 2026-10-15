package health

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
	"whatsapp-archiver/internal/config"
)

type note struct{ titles, bodies []string }

func (n *note) Notify(_ context.Context, title, body string) error {
	n.titles = append(n.titles, title)
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
	stop := running(t, m)
	m.SetState("client_outdated", "")
	m.SetState("client_outdated", "")
	stop()
	if len(n.bodies) != 1 || m.Status().State != "client_outdated" {
		t.Fatalf("notes=%v status=%+v", n.bodies, m.Status())
	}
}

func TestSnapshotRecordsClientVersionAndReason(t *testing.T) {
	dir := t.TempDir()
	c := &clock{time.Unix(1_700_000_000, 0)}
	m := NewMonitor(&config.Config{DataDir: dir}, nil, c, nil, func() string { return "2.3000.1" }).WithVersionReason(func() string { return "latest" })
	m.SetState("connected", "")
	data, err := os.ReadFile(filepath.Join(dir, "health.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err = json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got["version"] != "2.3000.1" || got["version_reason"] != "latest" {
		t.Fatalf("health.json=%s", data)
	}
}

func TestSnapshotWithoutVersionReasonLeavesItEmpty(t *testing.T) {
	m := NewMonitor(&config.Config{DataDir: t.TempDir()}, nil, &clock{time.Unix(1_700_000_000, 0)}, nil, func() string { return "2.3000.1" })
	m.SetState("connected", "")
	if s := m.Status(); s.Version != "2.3000.1" || s.VersionReason != "" {
		t.Fatalf("status=%+v", s)
	}
}
