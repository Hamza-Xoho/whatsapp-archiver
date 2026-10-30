package web

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"whatsapp-archiver/internal/store"
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

func TestW6IAsyncRegionsAndRepeatedReconnectAnnouncements(t *testing.T) {
	u := settingsUI(t)
	search, code, _ := get(t, u.handler(), "/search?q=archive")
	if code != http.StatusOK {
		t.Fatalf("search status = %d", code)
	}
	for _, want := range []string{
		`id="search-form"`, `data-request-region="search-results-region"`,
		`id="search-results-region"`, `class="async-region"`, `aria-busy="false"`,
		`class="request-loading"`, `class="request-error"`, "Search is unavailable right now. Try again.",
	} {
		if !strings.Contains(string(search), want) {
			t.Errorf("search async contract lacks %q", want)
		}
	}

	viewer := newChatsUI(t)
	for i := 0; i < 101; i++ {
		viewer.typedMessage(t, store.Message{ID: fmt.Sprintf("m%03d", i), Kind: "text", Text: "one", Timestamp: int64(i + 1)})
	}
	page, code, _ := get(t, viewer.handler(), "/chats/g@g.us")
	if code != http.StatusOK {
		t.Fatalf("viewer status = %d", code)
	}
	for _, want := range []string{`class="edge-loader async-region"`, `aria-busy="false"`, `class="request-loading"`, `class="request-error"`} {
		if !strings.Contains(string(page), want) {
			t.Errorf("viewer async edge contract lacks %q", want)
		}
	}

	shell := string(search)
	for _, want := range []string{"requestRegion", "beginAsyncRequest", "failAsyncRequest", "300", `status.textContent = ""`} {
		if !strings.Contains(shell, want) {
			t.Errorf("shared async/reconnect script lacks %q", want)
		}
	}
}
