package store_test

import (
	"testing"
	"time"

	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

func TestMediaTimelinessCountsEnabledLiveRowsInEachWindow(t *testing.T) {
	s := testkit.Store(t)
	now := time.Unix(1_800_000_000, 0)
	row := func(id, source, state string, enqueued time.Time, elapsed time.Duration) {
		doneAt := int64(0)
		if state == "done" {
			doneAt = enqueued.Add(elapsed).Unix()
		}
		if err := testkit.InsertMedia(t.Context(), s, store.Media{ChatJID: "g@g.us", MessageID: id, Source: source, Type: "image",
			State: state, EnqueuedAt: enqueued.Unix(), DoneAt: doneAt}); err != nil {
			t.Fatal(err)
		}
	}
	row("fast-day", "live", "done", now.Add(-time.Hour), 5*time.Minute)
	row("slow-day", "live", "done", now.Add(-2*time.Hour), 5*time.Minute+time.Second)
	row("pending-day", "live", "pending", now.Add(-3*time.Hour), 0)
	row("skipped", "live", "skipped", now.Add(-time.Hour), 0)
	row("history", "history", "done", now.Add(-time.Hour), time.Minute)
	row("fast-week", "live", "done", now.Add(-48*time.Hour), 4*time.Minute)
	row("too-old", "live", "done", now.Add(-8*24*time.Hour), time.Minute)
	row("unknown-enqueue", "live", "done", time.Unix(0, 0), time.Minute)

	got, err := s.MediaTimeliness(t.Context(), now)
	if err != nil {
		t.Fatal(err)
	}
	if got.DayDone != 1 || got.DayTotal != 3 || got.WeekDone != 2 || got.WeekTotal != 4 {
		t.Fatalf("timeliness = %+v, want day 1/3 and week 2/4", got)
	}
}
