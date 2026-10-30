package web

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

func TestLoggingOutagesSummarizeTodayAndKeepUnknownEvidence(t *testing.T) {
	d := newDash(t)
	preview, completed := int64(12), int64(10)
	for _, row := range []store.ConnectionPeriod{
		{State: "disconnected", CauseClass: "dns", StartedAt: dashNow.Add(-90 * time.Minute).Unix(), EndedAt: dashNow.Add(-60 * time.Minute).Unix()},
		{State: "reconnecting", CauseClass: "tcp", StartedAt: dashNow.Add(-50 * time.Minute).Unix(), EndedAt: dashNow.Add(-10 * time.Minute).Unix(), OfflinePreviewMsgs: &preview, OfflineCompletedCount: &completed},
	} {
		if err := d.st.InsertClosedConnectionPeriod(t.Context(), row); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.st.OpenConnectionPeriod(t.Context(), store.ConnectionPeriod{State: "degraded", CauseClass: "ws", StartedAt: dashNow.Add(-5 * time.Minute).Unix()}); err != nil {
		t.Fatal(err)
	}
	text := d.page(t, "/logging/outages")
	for _, want := range []string{"Today: 75 minutes", "Longest: 40 minutes", "In progress", "Offline preview Unknown", "Completed backlog Unknown", "Offline preview 12", "Completed backlog 10"} {
		if !strings.Contains(text, want) {
			t.Errorf("outages lack %q: %s", want, text)
		}
	}
}

func TestLoggingGapsGroupCauseAndMediaSuccessIsNotFailure(t *testing.T) {
	d := newDash(t)
	d.message(t, "family@g.us", "known")
	for i, id := range []string{"lost-a", "lost-b"} {
		if _, err := d.st.InsertEvent(t.Context(), store.MessageEvent{ChatJID: "family@g.us", TargetID: id, Kind: "undecryptable",
			EventID: "event-" + id, Cause: "no_sender_key", ReceivedAt: dashNow.Add(time.Duration(-i) * time.Minute).Unix()}); err != nil {
			t.Fatal(err)
		}
	}
	if err := testkit.InsertMedia(t.Context(), d.st, store.Media{ChatJID: "family@g.us", MessageID: "known", Source: "live", Type: "image", State: "done"}); err != nil {
		t.Fatal(err)
	}
	body, code, _ := get(t, d.handler(), "/logging/gaps-media")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	text := visibleText(string(body))
	for _, want := range []string{"No sender key", "2 gaps", "Oldest", "No media failures"} {
		if !strings.Contains(text, want) {
			t.Errorf("gaps/media lack %q: %s", want, text)
		}
	}
	if !strings.Contains(string(body), `/chats/family@g.us/m/lost-`) {
		t.Errorf("group has no usable safe trace link: %s", body)
	}
}

func TestLoggingHistoryShowsSyncRangeAndBoundedChatStates(t *testing.T) {
	d := newDash(t)
	received := store.HistoryChunk{ID: "full-1", Stage: store.StageReceived, SyncType: "FULL", Progress: 60,
		MessageCount: 25, Oldest: dashNow.Add(-60 * 24 * time.Hour).Unix(), Newest: dashNow.Add(-time.Hour).Unix(), ReceivedAt: dashNow.Add(-49 * time.Hour).Unix()}
	if err := d.st.InsertHistoryChunk(t.Context(), received); err != nil {
		t.Fatal(err)
	}
	history := testkit.Message("history@g.us", "history-1", dashNow.Add(-30*24*time.Hour))
	history.Source = "history"
	if _, err := d.st.InsertMessage(t.Context(), history); err != nil {
		t.Fatal(err)
	}
	d.message(t, "live@g.us", "live-1")
	text := d.page(t, "/logging/history")
	for _, want := range []string{"Received 1", "Imported 0", "60%", "Oldest", "Newest", "Spool backlog", "errors", "Stalled", "History received back to", "No history received"} {
		if !strings.Contains(text, want) {
			t.Errorf("history lacks %q: %s", want, text)
		}
	}
}

func TestLoggingAlertsShowRaisedDeliveredAndLateFacts(t *testing.T) {
	d := newDash(t)
	id, err := d.st.EnqueueAlert(t.Context(), store.Alert{Key: "disk", Kind: "raised", Title: "Disk space low", RaisedAt: dashNow.Add(-10 * time.Minute).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	if err = d.st.AlertSent(t.Context(), id, dashNow.Add(-7*time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}
	text := d.page(t, "/logging/alerts")
	for _, want := range []string{"Raised", "Delivered at", "3 minutes late", "Sent", "attempts 1"} {
		if !strings.Contains(text, want) {
			t.Errorf("alerts lack %q: %s", want, text)
		}
	}
}

func TestLoggingStorageReusesFullOperationalProjection(t *testing.T) {
	d := newDash(t)
	d.cfg.DataDir = t.TempDir()
	d.cfg.Backup.Destination = t.TempDir()
	d.cfg.Backup.Time, d.cfg.Backup.Keep = "03:30", 7
	d.message(t, "family@g.us", "media")
	if err := testkit.InsertMedia(t.Context(), d.st, store.Media{ChatJID: "family@g.us", MessageID: "media", Source: "live", Type: "image", State: "done", DeclaredSize: 4096}); err != nil {
		t.Fatal(err)
	}
	text := d.page(t, "/logging/storage")
	for _, want := range []string{"Backup folder", "Disk full in", "Media by type", "image", "Chats using the most media", "family@g.us", "every day at 03:30, keeping 7 snapshots", "No backup has run yet", "Destination"} {
		if !strings.Contains(text, want) {
			t.Errorf("storage lacks %q: %s", want, text)
		}
	}
}
