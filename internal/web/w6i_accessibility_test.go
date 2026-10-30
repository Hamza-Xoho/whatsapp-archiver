package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestW6IHTMXFocusRepairAndScopedValidationSwapContract(t *testing.T) {
	u := settingsUI(t)
	page, code, _ := get(t, u.handler(), "/logging/settings")
	if code != http.StatusOK {
		t.Fatalf("settings status = %d", code)
	}
	for _, want := range []string{
		"rememberSwapFocus", "restoreSwapFocus", "htmx:afterSettle",
		"validationFragment", "event.detail.shouldSwap = true", "event.detail.isError = false",
	} {
		if !strings.Contains(string(page), want) {
			t.Errorf("shared shell lacks %q", want)
		}
	}

	body, status, header := loggingSettingsPost(t, u.handler(), "/settings/media", url.Values{
		"download": {"image"}, "video_max_mb": {"not-a-number"}, "workers": {"2"},
		"retry_requests_per_minute": {"10"},
	}, true)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("invalid htmx settings status = %d", status)
	}
	if got := header.Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Fatalf("invalid htmx settings content type = %q", got)
	}
	for _, want := range []string{`<section class="panel" id="media">`, `value="not-a-number"`, "must be a whole number"} {
		if !strings.Contains(body, want) {
			t.Errorf("safe validation fragment lacks %q: %s", want, body)
		}
	}
	for _, forbidden := range []string{"<!doctype", `id="offline-banner"`, "<script"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("safe validation fragment unexpectedly contains %q", forbidden)
		}
	}
}
