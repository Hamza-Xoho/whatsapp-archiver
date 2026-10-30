package store_test

import (
	"context"
	"testing"
	"time"

	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

func TestLoggingOutagesAreBoundedNewestFirstAndCancellable(t *testing.T) {
	s := testkit.Store(t)
	for i := range 4 {
		preview, completed := int64(i+10), int64(i+20)
		if err := s.InsertClosedConnectionPeriod(t.Context(), store.ConnectionPeriod{
			State: "reconnecting", CauseClass: "dns", StartedAt: int64(100 + i*20), EndedAt: int64(110 + i*20),
			ErrorCount: i, LastError: "safe", OfflinePreviewMsgs: &preview, OfflineCompletedCount: &completed,
		}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.LoggingOutages(t.Context(), 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].StartedAt != 160 || got[2].StartedAt != 120 || *got[0].OfflineCompletedCount != 23 {
		t.Fatalf("LoggingOutages = %+v", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = s.LoggingOutages(ctx, 3); err == nil {
		t.Fatal("cancelled outage read succeeded")
	}
}

func TestLoggingGapsExposeSafeTraceFactsAndBoundTheResult(t *testing.T) {
	s := testkit.Store(t)
	if err := s.InsertClosedConnectionPeriod(t.Context(), store.ConnectionPeriod{State: "disconnected", CauseClass: "dns", StartedAt: 90, EndedAt: 100}); err != nil {
		t.Fatal(err)
	}
	for i, target := range []string{"old", "new"} {
		if _, err := s.InsertEvent(t.Context(), store.MessageEvent{ChatJID: "family@g.us", TargetID: target,
			Kind: "undecryptable", EventID: "gap-" + target, Cause: "no_sender_key", ReceivedAt: int64(101 + i)}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.LoggingGaps(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].CanonicalJID != "family@g.us" || got[0].TargetID != "new" || !got[0].PostOutage || got[0].Cause != "no_sender_key" {
		t.Fatalf("LoggingGaps = %+v", got)
	}
}

func TestLoggingMediaGroupsStateClassAndSafeReason(t *testing.T) {
	s := testkit.Store(t)
	rows := []store.Media{
		{ChatJID: "g@g.us", MessageID: "a", Source: "live", Type: "image", State: "failed", ErrorClass: "network", Reason: "dns unavailable"},
		{ChatJID: "g@g.us", MessageID: "b", Source: "live", Type: "image", State: "failed", ErrorClass: "network", Reason: "dns unavailable"},
		{ChatJID: "g@g.us", MessageID: "c", Source: "live", Type: "video", State: "pending"},
	}
	for _, row := range rows {
		if err := testkit.InsertMedia(t.Context(), s, row); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.LoggingMedia(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if countNamed(got.States, "failed") != 2 || countNamed(got.States, "pending") != 1 || countNamed(got.ErrorClasses, "network") != 2 {
		t.Fatalf("LoggingMedia = %+v", got)
	}
	if len(got.Reasons) != 1 || got.Reasons[0].Reason != "DNS lookup failed" || got.Reasons[0].Count != 2 {
		t.Fatalf("reason groups = %+v", got.Reasons)
	}
}

func TestLoggingHistoryCombinesLedgerSpoolAndPerChatReach(t *testing.T) {
	s := testkit.Store(t)
	spoolID, _, err := s.SpoolReceive(t.Context(), store.Spool{ChunkID: "full-1", SyncType: "FULL", Progress: 60,
		ReceivedAt: 100, BlobFormat: store.BlobRaw, Blob: []byte("synthetic")}, store.HistoryChunk{ID: "full-1", SyncType: "FULL", Progress: 60, ReceivedAt: 100, Oldest: 50, Newest: 90})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SpoolFailed(t.Context(), spoolID, "synthetic decode failure"); err != nil {
		t.Fatal(err)
	}
	m := testkit.Message("family@g.us", "history-1", time.Unix(40, 0))
	m.Source = "history"
	if _, err = s.InsertMessage(t.Context(), m); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoggingHistory(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Syncs) != 1 || got.Syncs[0].ReceivedChunks != 1 || got.Syncs[0].ImportedChunks != 0 || got.SpoolBacklog != 1 || got.SpoolErrors != 1 {
		t.Fatalf("LoggingHistory = %+v", got)
	}
	if len(got.Chats) != 1 || got.Chats[0].CanonicalJID != "family@g.us" || got.Chats[0].OldestHistory != 40 {
		t.Fatalf("history chats = %+v", got.Chats)
	}
}

func TestLoggingAlertsListsAllStatesWithoutRawErrorsOrBodies(t *testing.T) {
	s := testkit.Store(t)
	id, err := s.EnqueueAlert(t.Context(), store.Alert{Key: "disk", Kind: "raised", Title: "Disk space low", Body: "secret body", RaisedAt: 100})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AlertFailed(t.Context(), id, "https://secret.invalid/topic failed", 200); err != nil {
		t.Fatal(err)
	}
	id2, err := s.EnqueueAlert(t.Context(), store.Alert{Key: "disk", Kind: "recovered", Title: "Disk recovered", RaisedAt: 300})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AlertSent(t.Context(), id2, 420); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoggingAlerts(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Title != "Disk recovered" || got[0].SentAt != 420 || got[1].State != "queued" || got[1].Attempts != 1 {
		t.Fatalf("LoggingAlerts = %+v", got)
	}
}

func countNamed(rows []store.LoggingCount, name string) int {
	for _, row := range rows {
		if row.Name == name {
			return row.Count
		}
	}
	return 0
}
