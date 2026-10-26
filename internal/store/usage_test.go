package store_test

import (
	"testing"
	"time"

	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

var usageNow = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

func doneMedia(chat, id, typ string, size int64, at time.Time) store.Media {
	return store.Media{ChatJID: chat, MessageID: id, Type: typ, DeclaredSize: size, State: "done", UpdatedAt: at.Unix(), Source: "live"}
}

func TestUsageSumsDoneMediaByChatAndType(t *testing.T) {
	s := open(t)
	ctx := t.Context()
	day := 24 * time.Hour
	for _, m := range []store.Media{
		doneMedia("a@g.us", "1", "image", 100, usageNow.Add(-day)),
		doneMedia("a@g.us", "2", "video", 1000, usageNow.Add(-2*day)),
		doneMedia("b@g.us", "3", "image", 50, usageNow.Add(-day)),
		doneMedia("a@g.us", "4", "image", 7, usageNow.Add(-40*day)), // old: counts in totals, not in growth
		{ChatJID: "a@g.us", MessageID: "5", Type: "image", DeclaredSize: 5000, State: "pending", UpdatedAt: usageNow.Unix()},
		{ChatJID: "a@g.us", MessageID: "6", Type: "video", DeclaredSize: 6000, State: "skipped", UpdatedAt: usageNow.Unix()},
	} {
		if err := testkit.InsertMedia(ctx, s, m); err != nil {
			t.Fatal(err)
		}
	}
	u, err := s.Usage(ctx, usageNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(u.ByChat) != 2 || u.ByChat[0] != (store.ChatBytes{ChatJID: "a@g.us", Bytes: 1107}) || u.ByChat[1] != (store.ChatBytes{ChatJID: "b@g.us", Bytes: 50}) {
		t.Fatalf("ByChat = %+v, want a@g.us 1107 then b@g.us 50 (done rows only, largest first)", u.ByChat)
	}
	if len(u.ByMediaType) != 2 || u.ByMediaType["image"] != 157 || u.ByMediaType["video"] != 1000 {
		t.Fatalf("ByMediaType = %v", u.ByMediaType)
	}
	// 100+1000+50 arrived in the last 28 days: 1150 bytes over 4 weeks.
	if u.WeeklyGrowth != 287 {
		t.Fatalf("WeeklyGrowth = %d, want 287", u.WeeklyGrowth)
	}
}

func TestUsageGrowthAddsDatabaseGrowthFromRecentMessages(t *testing.T) {
	s := open(t)
	ctx := t.Context()
	if err := testkit.InsertMedia(ctx, s, doneMedia("a@g.us", "1", "image", 400, usageNow.Add(-time.Hour))); err != nil {
		t.Fatal(err)
	}
	// Four weeks of messages older than the window add nothing.
	for i, id := range []string{"o1", "o2", "o3"} {
		if _, err := s.InsertMessage(ctx, testkit.Message("a@g.us", id, usageNow.Add(-time.Duration(30+i)*24*time.Hour))); err != nil {
			t.Fatal(err)
		}
	}
	u, err := s.Usage(ctx, usageNow)
	if err != nil {
		t.Fatal(err)
	}
	if u.WeeklyGrowth != 100 {
		t.Fatalf("WeeklyGrowth = %d, want 100 (media only: messages older than 28 days do not count)", u.WeeklyGrowth)
	}
	for i, id := range []string{"n1", "n2", "n3", "n4"} {
		if _, err = s.InsertMessage(ctx, testkit.Message("a@g.us", id, usageNow.Add(-time.Duration(i+1)*24*time.Hour))); err != nil {
			t.Fatal(err)
		}
	}
	u, err = s.Usage(ctx, usageNow)
	if err != nil {
		t.Fatal(err)
	}
	if u.WeeklyGrowth <= 100 {
		t.Fatalf("WeeklyGrowth = %d, want more than the media's 100 once messages arrived in the window", u.WeeklyGrowth)
	}
}

func TestUsageOfEmptyArchive(t *testing.T) {
	u, err := open(t).Usage(t.Context(), usageNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(u.ByChat) != 0 || len(u.ByMediaType) != 0 || u.WeeklyGrowth != 0 {
		t.Fatalf("usage = %+v", u)
	}
}
