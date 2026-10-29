package wa

import (
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types/events"
)

// Regression for the 25 Oct outage: whatsmeow's keepalive path dispatches a
// KeepAliveTimeout, disconnects the socket itself, and starts auto-reconnect
// without dispatching Disconnected. The independent connectivity poll must
// still move health to reconnecting, so disconnected_after can alert.
func TestKeepAliveDisconnectWithoutDisconnectedEventAlerts(t *testing.T) {
	now := time.Date(2026, 10, 25, 18, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	socket := &connectivityFake{connected: true}
	health := &thresholdHealthFake{now: clock, threshold: 5 * time.Minute}
	monitor := newConnectionMonitor(socket, health, clock)

	monitor.handle(&events.Connected{})
	socket.connected = false
	monitor.handle(&events.KeepAliveTimeout{ErrorCount: 1, LastSuccess: now.Add(-30 * time.Second)})
	if health.state != "degraded" {
		t.Fatalf("after keepalive timeout state=%q, want degraded", health.state)
	}

	now = now.Add(61 * time.Second)
	monitor.poll()
	if health.state != "reconnecting" {
		t.Fatalf("after silent socket disconnect state=%q, want reconnecting", health.state)
	}

	now = now.Add(5*time.Minute + time.Second)
	health.check()
	if health.alerts != 1 {
		t.Fatalf("alerts=%d, want 1 after disconnected_after", health.alerts)
	}
}

type connectivityFake struct{ connected bool }

func (f *connectivityFake) IsConnected() bool { return f.connected }

type thresholdHealthFake struct {
	now       func() time.Time
	threshold time.Duration
	state     string
	downSince time.Time
	alerts    int
}

func (h *thresholdHealthFake) SetState(state, _ string) {
	if state == "connected" {
		h.downSince = time.Time{}
	} else if h.downSince.IsZero() {
		h.downSince = h.now()
	}
	h.state = state
}

func (h *thresholdHealthFake) check() {
	if (h.state == "degraded" || h.state == "reconnecting") && h.now().Sub(h.downSince) > h.threshold {
		h.alerts++
	}
}
