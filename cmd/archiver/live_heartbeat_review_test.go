package main

import (
	"context"
	"testing"

	"whatsapp-archiver/internal/config"
)

func TestLiveHeartbeatContextFailsClosedAfterRuntimeOwnership(t *testing.T) {
	st := liveStore(t)
	cfg := &config.Config{Alerts: config.AlertConfig{HeartbeatURL: "https://config.invalid/must-not-revive"}}
	if _, err := st.ImportConfigOnce(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	if err := st.RemoveHeartbeatURL(t.Context()); err != nil {
		t.Fatal(err)
	}
	live := newLive(st, nil, storeAliases(st))
	live.cfg = cfg
	if got := live.heartbeatURLContext(t.Context()); got != "" {
		t.Fatalf("removed heartbeat revived as %q", got)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if got := live.heartbeatURLContext(context.Background()); got != "" {
		t.Fatalf("failed read revived heartbeat as %q", got)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if got := live.heartbeatURLContext(cancelled); got != "" {
		t.Fatalf("cancelled read returned %q", got)
	}
}
