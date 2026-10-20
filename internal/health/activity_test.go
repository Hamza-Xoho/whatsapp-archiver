package health

import (
	"testing"
	"time"

	"whatsapp-archiver/internal/activity"
	"whatsapp-archiver/internal/config"
)

func TestMonitorPublishesOnTransitionOnly(t *testing.T) {
	bus := activity.NewBus(500)
	m := NewMonitor(&config.Config{DataDir: t.TempDir()}, nil, &clock{time.Unix(1_700_000_000, 0)}, nil, func() string { return "v" }).WithActivity(bus)
	m.SetState("connected", "")
	m.SetState("connected", "") // a repeat is not a transition
	m.Seen(time.Unix(1_700_000_001, 0), false)
	m.SetState("disconnected", "401 secret@s.whatsapp.net")
	m.SetState("connected", "")
	got := bus.Recent()
	var details []string
	for _, e := range got {
		if e.Kind != "state" || e.Chat != "" {
			t.Fatalf("event=%+v", e)
		}
		details = append(details, e.Detail)
	}
	want := []string{"connected", "disconnected", "connected"}
	if len(details) != len(want) {
		t.Fatalf("details=%v want %v", details, want)
	}
	for i := range want {
		if details[i] != want[i] {
			t.Fatalf("details=%v want %v", details, want)
		}
	}
}
