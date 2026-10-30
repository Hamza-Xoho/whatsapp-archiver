package web

import (
	"strings"
	"testing"
	"time"

	"whatsapp-archiver/internal/activity"
)

func TestLoggingInitialFeedFiltersBeforeTwentyMatchLimit(t *testing.T) {
	d := newDash(t)
	d.bus.Publish(activity.Event{At: dashNow.Add(-time.Minute), Kind: "message", Chat: "family@g.us", Detail: "matching-older-event"})
	for i := range 20 {
		d.bus.Publish(activity.Event{At: dashNow.Add(time.Duration(i) * time.Second), Kind: "message", Chat: "work@g.us", Detail: "newer-event"})
	}

	body := d.page(t, "/logging/feed?chat=family%40g.us&kind=message")
	if !strings.Contains(body, "matching-older-event") {
		t.Fatalf("matching event outside the newest global 20 was discarded: %s", body)
	}
	if strings.Contains(body, "newer-event") {
		t.Fatalf("initial filtered feed leaked nonmatching events: %s", body)
	}
}
