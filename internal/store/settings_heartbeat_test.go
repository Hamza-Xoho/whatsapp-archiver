package store_test

import (
	"context"
	"testing"

	"whatsapp-archiver/internal/testkit"
)

func TestHeartbeatURLSettingIsExplicitAndRemovable(t *testing.T) {
	s := testkit.Store(t)
	if _, ok, err := s.HeartbeatURL(t.Context()); err != nil || ok {
		t.Fatalf("initial heartbeat ok=%v err=%v", ok, err)
	}
	const secret = "https://heartbeat.example/private/token/ping-secret"
	if err := s.SetHeartbeatURL(t.Context(), secret); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := s.HeartbeatURL(t.Context()); err != nil || !ok || got != secret {
		t.Fatalf("heartbeat=%q ok=%v err=%v", got, ok, err)
	}
	if err := s.RemoveHeartbeatURL(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.HeartbeatURL(t.Context()); err != nil || ok {
		t.Fatalf("removed heartbeat ok=%v err=%v", ok, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := s.HeartbeatURL(ctx); err == nil {
		t.Fatal("cancelled heartbeat read succeeded")
	}
}
