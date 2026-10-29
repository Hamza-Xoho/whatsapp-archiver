package store_test

import (
	"context"
	"testing"
	"time"

	"whatsapp-archiver/internal/store"
)

func TestOutageSummarySplitsMinutesByLocalDayAndIncludesOpenPeriod(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	loc := time.FixedZone("UTC+3", 3*60*60)
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, loc)
	unix := func(day int, hour, minute int) int64 {
		return time.Date(2026, 10, day, hour, minute, 0, 0, loc).Unix()
	}
	for _, p := range []store.ConnectionPeriod{
		{State: "reconnecting", CauseClass: "tcp", StartedAt: unix(27, 23, 50), EndedAt: unix(28, 0, 20)},
		{State: "not_running", CauseClass: "not_running", StartedAt: unix(28, 12, 0), EndedAt: unix(28, 13, 30)},
	} {
		if err := s.InsertClosedConnectionPeriod(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.OpenConnectionPeriod(ctx, store.ConnectionPeriod{State: "degraded", CauseClass: "ws", StartedAt: unix(29, 11, 15)}); err != nil {
		t.Fatal(err)
	}

	got, err := s.OutageSummary(ctx, now, loc, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Days) != 3 || got.Days[0].Date != "2026-10-27" || got.Days[0].Minutes != 10 || got.Days[1].Minutes != 110 || got.Days[2].Minutes != 45 {
		t.Fatalf("days = %+v", got.Days)
	}
	if got.Longest != 90*time.Minute {
		t.Fatalf("longest = %v, want 90m", got.Longest)
	}
}

func TestOfflineCountsAttachToJustClosedConnectionPeriod(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if _, err := s.OpenConnectionPeriod(ctx, store.ConnectionPeriod{State: "reconnecting", StartedAt: 100}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CloseConnectionPeriod(ctx, 200); err != nil {
		t.Fatal(err)
	}
	preview, completed := int64(23), int64(21)
	if err := s.NoteOfflineSync(ctx, &preview, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.NoteOfflineSync(ctx, nil, &completed); err != nil {
		t.Fatal(err)
	}
	periods, err := s.ConnectionPeriods(ctx, 0)
	if err != nil || len(periods) != 1 || periods[0].OfflinePreviewMsgs == nil || *periods[0].OfflinePreviewMsgs != 23 || periods[0].OfflineCompletedCount == nil || *periods[0].OfflineCompletedCount != 21 {
		t.Fatalf("periods=%+v err=%v", periods, err)
	}
}

func TestOpenGapIsTaggedForTwoMinutesAfterReconnect(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if err := s.InsertClosedConnectionPeriod(ctx, store.ConnectionPeriod{State: "reconnecting", StartedAt: 10, EndedAt: 100}); err != nil {
		t.Fatal(err)
	}
	inside := gap("g@g.us", "IN")
	inside.ReceivedAt = 220
	outside := gap("g@g.us", "OUT")
	outside.ReceivedAt = 221
	applyEvent(t, s, inside)
	applyEvent(t, s, outside)
	rows, err := readDB(t, s).Query("SELECT target_id,post_outage FROM open_gaps ORDER BY target_id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	want := map[string]bool{"IN": true, "OUT": false}
	for rows.Next() {
		var id string
		var post bool
		if err := rows.Scan(&id, &post); err != nil {
			t.Fatal(err)
		}
		if post != want[id] {
			t.Errorf("%s post_outage=%v, want %v", id, post, want[id])
		}
		delete(want, id)
	}
	if err := rows.Err(); err != nil || len(want) != 0 {
		t.Fatalf("rows err=%v missing=%v", err, want)
	}
}
