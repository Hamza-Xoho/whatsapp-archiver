package health

import (
	"net/http"
	"testing"
	"time"

	"whatsapp-archiver/internal/config"
)

func TestHeartbeatReadsLiveURLWithoutMutatingConfig(t *testing.T) {
	c := &clock{time.Unix(1_700_000_000, 0).UTC()}
	var liveURL string
	var requested []string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requested = append(requested, r.URL.String())
		return &http.Response{StatusCode: http.StatusNoContent, Status: "204 No Content", Body: http.NoBody}, nil
	})}
	cfg := &config.Config{DataDir: t.TempDir(), Alerts: config.AlertConfig{NoEventsAfter: 10 * time.Minute}}
	m := NewMonitor(cfg, nil, c, nil, func() string { return "v" }).WithHeartbeatClient(client).WithHeartbeatURL(func() string { return liveURL })
	m.SetState("connected", "")
	m.Seen(c.now, false)
	if err := m.pingHeartbeat(t.Context()); err != nil {
		t.Fatal(err)
	}
	liveURL = "https://first.invalid/ping"
	if err := m.pingHeartbeat(t.Context()); err != nil {
		t.Fatal(err)
	}
	liveURL = "https://second.invalid/ping"
	if err := m.pingHeartbeat(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(requested) != 2 || requested[0] != "https://first.invalid/ping" || requested[1] != "https://second.invalid/ping" {
		t.Fatalf("requests=%v", requested)
	}
	if cfg.Alerts.HeartbeatURL != "" {
		t.Fatalf("shared config mutated to %q", cfg.Alerts.HeartbeatURL)
	}
}
