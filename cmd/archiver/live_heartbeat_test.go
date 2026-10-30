package main

import (
	"context"
	"testing"

	"whatsapp-archiver/internal/config"
)

func TestLiveHeartbeatUsesArchiveThenConfigFallback(t *testing.T) {
	st := liveStore(t)
	live := newLive(st, nil, storeAliases(st))
	live.cfg.Alerts.HeartbeatURL = "https://config.invalid/fallback"
	if got := live.heartbeatURL(); got != "https://config.invalid/fallback" {
		t.Fatalf("fallback=%q", got)
	}
	if err := st.SetHeartbeatURL(context.Background(), "https://stored.invalid/live"); err != nil {
		t.Fatal(err)
	}
	if got := live.heartbeatURL(); got != "https://stored.invalid/live" {
		t.Fatalf("stored=%q", got)
	}
	if err := st.RemoveHeartbeatURL(context.Background()); err != nil {
		t.Fatal(err)
	}
	live.cfg = &config.Config{}
	if got := live.heartbeatURL(); got != "" {
		t.Fatalf("removed=%q", got)
	}
}
