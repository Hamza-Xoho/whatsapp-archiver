package web

import (
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"whatsapp-archiver/internal/activity"
	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/health"
	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

var dashNow = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

// dash is a dashboard under test: a seeded store, a fake health snapshot and a bus.
type dash struct {
	st   *store.Store
	bus  *activity.Bus
	snap health.Snapshot
	cfg  *config.Config
	rule []store.ChatRule
}

func newDash(t *testing.T) *dash {
	t.Helper()
	return &dash{
		st:  testkit.Store(t),
		bus: activity.NewBus(500),
		snap: health.Snapshot{State: "connected", Version: "2.3000.1050849967", VersionReason: "latest",
			PhoneLastActive: dashNow.Add(-time.Hour), LastEvent: dashNow.Add(-time.Minute)},
		cfg: &config.Config{Alerts: config.AlertConfig{PhoneQuietAfter: 240 * time.Hour}},
	}
}

func (d *dash) handler() http.Handler {
	return New(Deps{Store: d.st, Rules: ingest.NewRuleSet(d.rule), Health: func() health.Snapshot { return d.snap },
		Activity: d.bus, Config: d.cfg, Now: func() time.Time { return dashNow }}).Handler()
}

// page fetches path and returns the visible text: tags dropped, entities decoded, white space collapsed.
func (d *dash) page(t *testing.T, path string) string {
	t.Helper()
	body, code, _ := get(t, d.handler(), path)
	if code != http.StatusOK {
		t.Fatalf("GET %s: status %d", path, code)
	}
	return visibleText(string(body))
}

var (
	tagRe   = regexp.MustCompile(`<[^>]*>`)
	spaceRe = regexp.MustCompile(`\s+`)
)

func visibleText(s string) string {
	return strings.TrimSpace(spaceRe.ReplaceAllString(html.UnescapeString(tagRe.ReplaceAllString(s, " ")), " "))
}

func (d *dash) message(t *testing.T, chat, id string) {
	t.Helper()
	if _, err := d.st.InsertMessage(t.Context(), testkit.Message(chat, id, dashNow.Add(-time.Hour))); err != nil {
		t.Fatal(err)
	}
}

func TestDashboardShowsStateVersionAndHistoryLine(t *testing.T) {
	d := newDash(t)
	if err := d.st.InsertHistoryChunk(t.Context(), store.HistoryChunk{ID: "c1", SyncType: "FULL", Progress: 40, ReceivedAt: dashNow.Add(-time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}
	got := d.page(t, "/")
	for _, want := range []string{"Connected", "2.3000.1050849967 (latest)", "history: in progress (40%)"} {
		if !strings.Contains(got, want) {
			t.Errorf("dashboard lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Open WhatsApp on your phone") {
		t.Error("phone warning shown for a phone active an hour ago")
	}
}

func TestDashboardShowsDisconnectedWithDetail(t *testing.T) {
	d := newDash(t)
	d.snap.State, d.snap.Detail = "disconnected", "stream error"
	got := d.page(t, "/")
	if !strings.Contains(got, "Disconnected") || !strings.Contains(got, "stream error") {
		t.Fatalf("dashboard: %s", got)
	}
}

// The threshold is the live alert setting, not config.yaml: 72 hours is quiet at 48 hours and not at 240.
func TestDashboardWarnsWhenPhoneQuiet(t *testing.T) {
	d := newDash(t)
	d.snap.PhoneLastActive = dashNow.Add(-72 * time.Hour)
	if got := d.page(t, "/"); strings.Contains(got, "Open WhatsApp on your phone soon") {
		t.Fatalf("warned at the 240 hour config threshold: %s", got)
	}
	if err := d.st.SetAlertThresholds(t.Context(), config.AlertConfig{PhoneQuietAfter: 48 * time.Hour}); err != nil {
		t.Fatal(err)
	}
	if got := d.page(t, "/"); !strings.Contains(got, "Open WhatsApp on your phone soon") || !strings.Contains(got, "not been active for 3 days") {
		t.Fatalf("no warning at the 48 hour live threshold: %s", got)
	}
}

func TestDashboardPhoneQuietFallsBackToConfig(t *testing.T) {
	d := newDash(t)
	d.cfg.Alerts.PhoneQuietAfter = 24 * time.Hour
	d.snap.PhoneLastActive = dashNow.Add(-30 * time.Hour)
	if got := d.page(t, "/"); !strings.Contains(got, "Open WhatsApp on your phone soon") || !strings.Contains(got, "not been active for 30 hours") {
		t.Fatalf("no warning past the config threshold: %s", got)
	}
}

func TestDashboardPhoneUnknownIsNotAWarning(t *testing.T) {
	d := newDash(t)
	d.snap.PhoneLastActive = time.Time{}
	if got := d.page(t, "/"); strings.Contains(got, "Open WhatsApp on your phone") {
		t.Fatalf("warned with no phone activity recorded yet: %s", got)
	}
}

func TestDashboardLists20RecentEvents(t *testing.T) {
	d := newDash(t)
	for i := range 25 {
		d.bus.Publish(activity.Event{At: dashNow.Add(time.Duration(i) * time.Second), Kind: "message", Chat: "c@g.us", Detail: fmt.Sprintf("item%02d", i)})
	}
	got := d.page(t, "/")
	if n := strings.Count(got, "item"); n != 20 {
		t.Fatalf("%d events listed, want 20", n)
	}
	if strings.Contains(got, "item04") || !strings.Contains(got, "item05") || !strings.Contains(got, "item24") {
		t.Fatalf("not the newest 20: %s", got)
	}
	if strings.Index(got, "item24") > strings.Index(got, "item23") {
		t.Fatal("newest event is not first")
	}
}

func TestDashboardCountsLoggingAndNotLoggingChats(t *testing.T) {
	d := newDash(t)
	d.message(t, "1@s.whatsapp.net", "a")
	d.message(t, "2@g.us", "b")
	d.message(t, "3@g.us", "c")
	d.rule = []store.ChatRule{{JID: "3@g.us", Logging: false}}
	got := d.page(t, "/")
	for _, want := range []string{"Logging: 2 chats", "Not logging: 1 chat "} {
		if !strings.Contains(got, want) {
			t.Errorf("dashboard lacks %q:\n%s", want, got)
		}
	}
}

func TestDashboardShowsOpenGapsCount(t *testing.T) {
	d := newDash(t)
	d.message(t, "1@g.us", "a")
	for i := range 2 {
		ev := store.MessageEvent{ChatJID: "1@g.us", TargetID: fmt.Sprint("lost", i), Kind: "undecryptable", EventID: fmt.Sprint("u", i), SenderJID: "s@lid", Timestamp: 1, ReceivedAt: 1}
		if _, err := d.st.InsertEvent(t.Context(), ev); err != nil {
			t.Fatal(err)
		}
	}
	if got := d.page(t, "/"); !strings.Contains(got, "Open gaps: 2") {
		t.Fatalf("dashboard: %s", got)
	}
}

func TestDashboardShowsLiveMediaDoneWithinFiveMinutes(t *testing.T) {
	d := newDash(t)
	for i, elapsed := range []time.Duration{4 * time.Minute, 6 * time.Minute, 0} {
		state, doneAt := "done", int64(0)
		if elapsed == 0 {
			state = "pending"
		} else {
			doneAt = dashNow.Add(-time.Hour).Add(elapsed).Unix()
		}
		if err := testkit.InsertMedia(t.Context(), d.st, store.Media{ChatJID: "g@g.us", MessageID: fmt.Sprint("m", i), Source: "live", Type: "image",
			State: state, EnqueuedAt: dashNow.Add(-time.Hour).Unix(), DoneAt: doneAt}); err != nil {
			t.Fatal(err)
		}
	}
	if err := testkit.InsertMedia(t.Context(), d.st, store.Media{ChatJID: "g@g.us", MessageID: "week", Source: "live", Type: "image",
		State: "done", EnqueuedAt: dashNow.Add(-48 * time.Hour).Unix(), DoneAt: dashNow.Add(-48*time.Hour + 4*time.Minute).Unix()}); err != nil {
		t.Fatal(err)
	}
	got := d.page(t, "/")
	for _, want := range []string{"Media done within 5 min (24 hours): 33.3% (1/3)", "Media done within 5 min (7 days): 50.0% (2/4)"} {
		if !strings.Contains(got, want) {
			t.Errorf("dashboard lacks %q: %s", want, got)
		}
	}
}

func TestDashboardShowsOutageMinutesAndLongestOutage(t *testing.T) {
	d := newDash(t)
	if err := d.st.InsertClosedConnectionPeriod(t.Context(), store.ConnectionPeriod{
		State: "not_running", CauseClass: "not_running",
		StartedAt: dashNow.Add(-90 * time.Minute).Unix(), EndedAt: dashNow.Add(-30 * time.Minute).Unix(),
	}); err != nil {
		t.Fatal(err)
	}
	got := d.page(t, "/")
	for _, want := range []string{"Outage minutes by day", "2026-10-20: 60 minutes", "Longest outage: 60 minutes"} {
		if !strings.Contains(got, want) {
			t.Errorf("dashboard lacks %q:\n%s", want, got)
		}
	}
}

// The panel alone is what a health event swaps in; the page includes the same panel and listens for the event.
func TestHealthPartialIsThePanelOnly(t *testing.T) {
	d := newDash(t)
	body, code, _ := get(t, d.handler(), "/partials/health")
	if code != http.StatusOK || strings.Contains(string(body), "<html") || !strings.Contains(visibleText(string(body)), "Connected") {
		t.Fatalf("status %d, body %s", code, body)
	}
	page, _, _ := get(t, d.handler(), "/")
	for _, want := range []string{`hx-get="/partials/health"`, `hx-trigger="sse:health"`} {
		if !strings.Contains(string(page), want) {
			t.Errorf("dashboard lacks %s", want)
		}
	}
}

// The dashboard and its feed show counts and kinds, never message text.
func TestDashboardShowsNoMessageText(t *testing.T) {
	d := newDash(t)
	m := testkit.Message("1@g.us", "a", dashNow.Add(-time.Hour))
	m.Text = "SECRET-BODY-TEXT"
	if _, err := d.st.InsertMessage(t.Context(), m); err != nil {
		t.Fatal(err)
	}
	d.bus.Publish(activity.Event{At: dashNow, Kind: "message", Chat: "1@g.us", Detail: "text"})
	if got := d.page(t, "/"); strings.Contains(got, "SECRET-BODY-TEXT") {
		t.Fatal("message text on the dashboard")
	}
}

// A chat shows by name, with its JID only as secondary text.
func TestDashboardEventShowsChatName(t *testing.T) {
	d := newDash(t)
	d.message(t, "1@g.us", "a")
	if err := d.st.UpsertChat(t.Context(), store.Chat{JID: "1@g.us", Name: "Family"}); err != nil {
		t.Fatal(err)
	}
	d.bus.Publish(activity.Event{At: dashNow, Kind: "message", Chat: "1@g.us", Detail: "text"})
	got := d.page(t, "/")
	if !strings.Contains(got, "Family") {
		t.Fatalf("no chat name: %s", got)
	}
}

// A page with no store or monitor behind it (an unwired server) still renders.
func TestDashboardRendersWithoutDeps(t *testing.T) {
	body, code, _ := get(t, New(Deps{}).Handler(), "/")
	if code != http.StatusOK || !strings.Contains(string(body), "Unknown") {
		t.Fatalf("status %d: %s", code, body)
	}
}
