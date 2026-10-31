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

func TestW6IPairingQRRenewalAndOfflinePresentation(t *testing.T) {
	f := newFakePairer(t, false)
	h := newPairUI(t, f).Handler()
	if response := samePost(t, h, "/pair/start", true); response.StatusCode != http.StatusSeeOther {
		t.Fatalf("start pairing status = %d", response.StatusCode)
	}
	f.send(PairUpdate{QR: "2@first-safe-fake"})
	first := waitPage(t, h, `data-qr-seq="1"`)
	for _, want := range []string{
		`id="qr-status"`, `role="status"`, `aria-live="polite"`, `aria-atomic="true"`,
		"A new QR code is ready.", `class="qr-frame"`, `class="qr-expired"`,
	} {
		if !strings.Contains(first, want) {
			t.Errorf("first fake QR lacks %q", want)
		}
	}
	if count := strings.Count(first, `id="qr-status"`); count != 1 {
		t.Errorf("QR page has %d renewal status nodes, want 1", count)
	}

	f.send(PairUpdate{QR: "2@second-safe-fake"})
	second := waitPage(t, h, `data-qr-seq="2"`)
	if !strings.Contains(second, "A new QR code is ready.") || strings.Count(second, `id="qr-status"`) != 1 {
		t.Errorf("renewed QR does not keep one stable polite status node")
	}
	for _, want := range []string{"expirePairingQR", "pair-offline", "QR code expired while live updates are paused"} {
		if !strings.Contains(second, want) {
			t.Errorf("pairing offline contract lacks %q", want)
		}
	}
}

func TestW6IMobileTraceTargetsAndBidiIsolation(t *testing.T) {
	u := newChatsUI(t)
	const chat = "arabic@g.us"
	if err := u.st.UpsertChat(t.Context(), store.Chat{JID: chat, Name: "عائلة الاختبار", Type: "group"}); err != nil {
		t.Fatal(err)
	}
	u.msg(t, chat, "message-one", "1@lid", 1760000000, "مرحبا بالعالم")
	u.event(t, store.MessageEvent{ChatJID: chat, TargetID: "message-one", Kind: "edit", EventID: "edit-one", SenderJID: "2@lid", NewText: "النص المعدل", Timestamp: 1760000060})
	h := u.handler()

	trace, code, _ := get(t, h, "/chats/"+chat+"/m/message-one")
	if code != http.StatusOK {
		t.Fatalf("trace status = %d", code)
	}
	for _, want := range []string{
		`class="trace-events"`, `data-label="When"`, `data-label="What"`, `data-label="By"`, `data-label="Details"`,
		`class="text" dir="auto"`, `<td data-label="Details" dir="auto">`,
	} {
		if !strings.Contains(string(trace), want) {
			t.Errorf("mobile/bidi trace lacks %q", want)
		}
	}

	preview, status := postForm(t, h, "/chats/rules/preview", url.Values{"jid[]": {chat}, "logging": {"on"}})
	if status != http.StatusOK || !strings.Contains(preview, `<bdi dir="auto">عائلة الاختبار?</bdi>`) {
		t.Errorf("Arabic rule confirmation is not isolated: status=%d %s", status, preview)
	}
	export, code, _ := get(t, h, "/chats/"+chat+"/export")
	if code != http.StatusOK || !strings.Contains(string(export), `Export <bdi dir="auto">عائلة الاختبار</bdi>`) {
		t.Errorf("Arabic export heading is not isolated: status=%d", code)
	}

	css, code, _ := get(t, h, "/static/app.css")
	if code != http.StatusOK {
		t.Fatalf("CSS status = %d", code)
	}
	for _, want := range []string{".viewer-actions a", ".page-controls a", "min-height: 44px", ".trace-events td::before"} {
		if !strings.Contains(string(css), want) {
			t.Errorf("mobile control/card CSS lacks %q", want)
		}
	}
}
