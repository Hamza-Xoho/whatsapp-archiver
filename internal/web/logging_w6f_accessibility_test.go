package web

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"whatsapp-archiver/internal/activity"
)

func TestLoggingInactivityWarningHasStableDedicatedLiveRegion(t *testing.T) {
	d := newDash(t)
	d.cfg.Alerts.PhoneQuietAfter = time.Hour
	d.snap.PhoneLastActive = dashNow.Add(-3 * time.Hour)
	body, code, _ := get(t, d.handler(), "/logging")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	page := string(body)
	health := elementWithID(t, page, "health")
	if strings.Contains(health, "aria-live") || strings.Contains(health, `role="status"`) {
		t.Fatalf("health panel is a live region: %s", health)
	}
	warning := elementWithID(t, page, "inactivity-warning")
	for _, want := range []string{`aria-live="polite"`, `data-severity="phone-quiet"`, `hx-get="/partials/inactivity"`, "not been active for 3 hours"} {
		if !strings.Contains(warning, want) {
			t.Errorf("warning lacks %q: %s", want, warning)
		}
	}
	if !strings.Contains(page, "inactivity-warning") || !strings.Contains(page, "shouldSwap = false") {
		t.Fatal("shell does not suppress an unchanged inactivity announcement")
	}
	partial, partialCode, _ := get(t, d.handler(), "/partials/inactivity")
	if partialCode != http.StatusOK || strings.Count(string(partial), `id="inactivity-warning"`) != 1 {
		t.Fatalf("inactivity partial = %d %s", partialCode, partial)
	}
}

func TestLoggingFeedFiltersMetadataAndPauseIsKeyboardOperable(t *testing.T) {
	d := newDash(t)
	for i, event := range []activity.Event{
		{Kind: "message", Chat: "family@g.us", Detail: "image"},
		{Kind: "media", Chat: "family@g.us", Detail: "done"},
		{Kind: "message", Chat: "work@g.us", Detail: "text"},
	} {
		event.At = dashNow.Add(time.Duration(i) * time.Second)
		d.bus.Publish(event)
	}
	body, code, _ := get(t, d.handler(), "/logging/feed?chat=family%40g.us&kind=message")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	page := string(body)
	for _, want := range []string{`name="chat"`, `value="family@g.us"`, `name="kind"`, `value="message"`, `type="button"`, `id="pause-feed"`, `aria-pressed="false"`, "Pause updates", "family@g.us", "image"} {
		if !strings.Contains(page, want) {
			t.Errorf("feed lacks %q", want)
		}
	}
	for _, not := range []string{"work@g.us", ">media<", ">done<"} {
		if strings.Contains(page, not) {
			t.Errorf("filtered feed contains %q: %s", not, page)
		}
	}
	if !strings.Contains(page, "feedPaused") || !strings.Contains(page, "preventDefault") {
		t.Fatal("pause control does not stop streamed feed swaps")
	}
}

func TestLoggingFeedUsesBidiIsolationAndKeepsBoundedMetadataOnly(t *testing.T) {
	d := newDash(t)
	for i := range 25 {
		d.bus.Publish(activity.Event{At: dashNow.Add(time.Duration(i) * time.Second), Kind: "message", Chat: "مجموعة@g.us", Detail: fmt.Sprintf("kind-%02d", i)})
	}
	body, _, _ := get(t, d.handler(), "/logging/feed")
	page := string(body)
	for _, want := range []string{`role="log"`, `aria-live="polite"`, `aria-relevant="additions"`, `data-max="200"`, `<bdi class="chat">مجموعة@g.us</bdi>`, `dir="auto"`} {
		if !strings.Contains(page, want) {
			t.Errorf("feed lacks %q", want)
		}
	}
	if strings.Count(page, `class="feed-item"`) != 20 {
		t.Fatalf("initial feed has %d items, want bounded newest 20", strings.Count(page, `class="feed-item"`))
	}
}

func TestLoggingMobileCardsThemeAndOfflineContract(t *testing.T) {
	h := newDash(t).handler()
	body, _, _ := get(t, h, "/logging/outages")
	page := string(body)
	for _, want := range []string{`class="logging-cards"`, `id="offline-banner"`, "writesDisabled", "Live updates paused", "prefers-color-scheme"} {
		if !strings.Contains(page, want) {
			t.Errorf("logging page lacks %q", want)
		}
	}
	css, code, _ := get(t, h, "/static/app.css")
	if code != http.StatusOK {
		t.Fatalf("CSS status %d", code)
	}
	styles := string(css)
	for _, want := range []string{".logging-cards", "@media (max-width: 40rem)", ".logging-cards article", "prefers-reduced-motion"} {
		if !strings.Contains(styles, want) {
			t.Errorf("CSS lacks %q", want)
		}
	}
}

func elementWithID(t *testing.T, page, id string) string {
	t.Helper()
	needle := `id="` + id + `"`
	start := strings.Index(page, needle)
	if start < 0 {
		t.Fatalf("page lacks %s", needle)
	}
	open := strings.LastIndex(page[:start], "<")
	end := strings.Index(page[start:], "</")
	if open < 0 || end < 0 {
		t.Fatalf("cannot isolate %s", id)
	}
	return page[open : start+end]
}
