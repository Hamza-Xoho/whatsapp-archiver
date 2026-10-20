package media

import (
	"errors"
	"strings"
	"testing"

	"whatsapp-archiver/internal/activity"
	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

func TestQueuePublishesDoneWithoutPathsOrURLs(t *testing.T) {
	st := testkit.Store(t)
	bus := activity.NewBus(500)
	d := &fakeDown{}
	q := newTestQueue(t, st, d, config.MediaRules{Download: []string{"image"}, Workers: 1}, newClock(), nil).WithActivity(bus)
	enqueue(t, q, store.Media{ChatJID: "g@g.us", MessageID: "A", Type: "image", MIME: "image/jpeg"})
	stop := start(t, q)
	eventually(t, "image done", func() bool { return row(t, st, "g@g.us", "A").State == "done" })
	stop()
	got := bus.Recent()
	if len(got) != 1 || got[0].Kind != "media" || got[0].Chat != "g@g.us" || got[0].Detail != "image done" {
		t.Fatalf("events=%+v", got)
	}
	if strings.Contains(got[0].Detail, "/") || strings.Contains(got[0].Detail, "http") {
		t.Fatalf("event leaks a path: %+v", got[0])
	}
}

func TestQueuePublishesAFailedItem(t *testing.T) {
	st := testkit.Store(t)
	bus := activity.NewBus(500)
	d := &fakeDown{download: func([]byte, string) ([]byte, error) { return nil, errors.New("https://mmg.whatsapp.net/x?token=abc: boom") }}
	q := newTestQueue(t, st, d, config.MediaRules{Download: []string{"image"}, Workers: 1}, newClock(), nil).WithActivity(bus)
	enqueue(t, q, store.Media{ChatJID: "g@g.us", MessageID: "B", Type: "image", MIME: "image/jpeg", Attempts: maxAttempts})
	stop := start(t, q)
	eventually(t, "image failed", func() bool { return row(t, st, "g@g.us", "B").State == "failed" })
	stop()
	got := bus.Recent()
	if len(got) != 1 || got[0].Kind != "media" || got[0].Detail != "image failed" || strings.Contains(got[0].Detail, "token") {
		t.Fatalf("events=%+v", got)
	}
}
