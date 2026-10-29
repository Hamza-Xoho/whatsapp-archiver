package health

import (
	"net/http"
	"testing"
	"time"

	"whatsapp-archiver/internal/config"
)

func TestHeartbeatOnlyPingsWhileConnectedWithFreshEvents(t *testing.T) {
	c := &clock{time.Unix(1_700_000_000, 0).UTC()}
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: http.StatusNoContent, Status: "204 No Content", Body: http.NoBody}, nil
	})}
	cfg := &config.Config{DataDir: t.TempDir(), Alerts: config.AlertConfig{HeartbeatURL: "https://heartbeat.invalid/ping", NoEventsAfter: 10 * time.Minute}}
	m := NewMonitor(cfg, nil, c, nil, func() string { return "v" }).WithHeartbeatClient(client)

	m.SetState("connected", "")
	if err := m.pingHeartbeat(t.Context()); err != nil {
		t.Fatal(err)
	}
	m.Seen(c.now, false)
	if err := m.pingHeartbeat(t.Context()); err != nil {
		t.Fatal(err)
	}
	m.SetState("reconnecting", "")
	if err := m.pingHeartbeat(t.Context()); err != nil {
		t.Fatal(err)
	}
	m.SetState("connected", "")
	c.now = c.now.Add(11 * time.Minute)
	if err := m.pingHeartbeat(t.Context()); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("heartbeat requests=%d, want 1", requests)
	}
}

func TestEmptyHeartbeatURLIsOff(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody}, nil
	})}
	m := NewMonitor(&config.Config{DataDir: t.TempDir()}, nil, &clock{time.Now()}, nil, func() string { return "v" }).WithHeartbeatClient(client)
	m.SetState("connected", "")
	m.Seen(time.Now(), false)
	if err := m.pingHeartbeat(t.Context()); err != nil || calls != 0 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}
