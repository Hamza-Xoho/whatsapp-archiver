package web

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"whatsapp-archiver/internal/activity"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

func TestLoggingOverviewIsConciseAndShowsOperationalSummary(t *testing.T) {
	d := newDash(t)
	d.message(t, "family@g.us", "one")
	d.message(t, "work@g.us", "two")
	if err := testkit.InsertMedia(t.Context(), d.st, store.Media{ChatJID: "family@g.us", MessageID: "failed",
		Source: "live", Type: "image", State: "failed", ErrorClass: "network", Reason: "dns"}); err != nil {
		t.Fatal(err)
	}
	if err := d.st.InsertHistoryChunk(t.Context(), store.HistoryChunk{ID: "full", SyncType: "FULL", Progress: 75,
		ReceivedAt: dashNow.Add(-time.Hour).Unix(), Oldest: dashNow.Add(-30 * 24 * time.Hour).Unix(), Newest: dashNow.Unix()}); err != nil {
		t.Fatal(err)
	}
	if err := d.st.InsertClosedConnectionPeriod(t.Context(), store.ConnectionPeriod{State: "disconnected", CauseClass: "dns",
		StartedAt: dashNow.Add(-30 * time.Minute).Unix(), EndedAt: dashNow.Add(-20 * time.Minute).Unix()}); err != nil {
		t.Fatal(err)
	}
	d.snap.LastBackup = dashNow.Add(-2 * time.Hour)
	d.bus.Publish(activity.Event{At: dashNow, Kind: "message", Chat: "family@g.us", Detail: "OVERVIEW_MUST_NOT_RENDER_FEED"})
	body, code, _ := get(t, d.handler(), "/logging")
	if code != http.StatusOK {
		t.Fatalf("status %d: %s", code, body)
	}
	page, text := string(body), visibleText(string(body))
	for _, want := range []string{"Connection", "Archive", "2 chats", "2 messages", "Failed media: 1", "history FULL", "Media done within 5 min", "Longest outage", "Space", "Nightly backup", "Media rules", "Alert thresholds", "Backup schedule", "Live feed"} {
		if !strings.Contains(text, want) {
			t.Errorf("overview lacks %q: %s", want, text)
		}
	}
	for _, want := range []string{`href="/logging/storage"`, `href="/logging/settings#media"`, `href="/logging/feed"`, `sse-connect="/events"`} {
		if !strings.Contains(page, want) {
			t.Errorf("overview lacks %q", want)
		}
	}
	for _, unwanted := range []string{`<form`, `id="feed"`, "OVERVIEW_MUST_NOT_RENDER_FEED", "Disk full in", `name="video_max_mb"`} {
		if strings.Contains(page, unwanted) {
			t.Errorf("overview still renders legacy content %q", unwanted)
		}
	}
}

func TestLoggingSettingsUsesSharedOfflineShell(t *testing.T) {
	body, code, _ := get(t, settingsUI(t).handler(), "/logging/settings")
	if code != http.StatusOK {
		t.Fatalf("status %d: %s", code, body)
	}
	page := string(body)
	for _, want := range []string{`id="offline-banner"`, `id="toast"`, "/static/htmx.min.js", "writesDisabled", "Live updates paused", "htmx:sendError"} {
		if !strings.Contains(page, want) {
			t.Errorf("settings shared shell lacks %q", want)
		}
	}
	if strings.Contains(page, `sse-connect="/events"`) {
		t.Fatal("settings opens SSE")
	}
}

func TestLoggingLoadingCopyIsIdleHiddenAndRequestDelayed(t *testing.T) {
	body, code, _ := get(t, newDash(t).handler(), "/logging/outages")
	if code != http.StatusOK {
		t.Fatalf("status %d: %s", code, body)
	}
	page := string(body)
	for _, want := range []string{`class="loading-holder"`, `aria-hidden="true"`, `class="visually-hidden loading-copy">Loading…</span>`, "htmx:beforeRequest", "loadingDelay", "300", `aria-busy="false"`} {
		if !strings.Contains(page, want) {
			t.Errorf("loading contract lacks %q", want)
		}
	}
}
