package health

import (
	"context"
	"net/http"
	"testing"
	"time"

	"whatsapp-archiver/internal/config"
)

func TestHeartbeatURLProviderReceivesRunContext(t *testing.T) {
	c := &clock{time.Unix(1_700_000_000, 0).UTC()}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("cancelled heartbeat made a request")
		return nil, nil
	})}
	var sawCancellation bool
	m := NewMonitor(&config.Config{DataDir: t.TempDir()}, nil, c, nil, func() string { return "v" }).
		WithHeartbeatClient(client).
		WithHeartbeatURLContext(func(ctx context.Context) string {
			sawCancellation = ctx.Err() != nil
			return ""
		})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.pingHeartbeat(ctx); err != nil {
		t.Fatal(err)
	}
	if !sawCancellation {
		t.Fatal("heartbeat provider did not receive the cancelled run context")
	}
}
