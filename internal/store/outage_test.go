package store_test

import (
	"context"
	"testing"

	"whatsapp-archiver/internal/store"
)

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
