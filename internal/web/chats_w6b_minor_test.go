package web

import (
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestActiveFilterChipsRemoveOnlyTheirFilter(t *testing.T) {
	u := newChatsUI(t)
	values := url.Values{"q": {"Aisha"}, "type": {"direct"}, "logging": {"on"}, "new": {"1"}, "gaps": {"1"}, "failed": {"1"}}
	page := rawChatsPage(t, u, "/chats?"+values.Encode())
	detailsEnd := strings.Index(page, "</details>")
	chipsStart := strings.Index(page, `<nav class="filter-chips"`)
	if chipsStart < detailsEnd || chipsStart < 0 {
		t.Fatalf("active filter chips are not outside the collapsible filter panel: %s", page)
	}
	for _, key := range []string{"q", "type", "logging", "new", "gaps", "failed"} {
		remaining := url.Values{}
		for k, list := range values {
			if k != key {
				remaining[k] = append([]string(nil), list...)
			}
		}
		want := `data-filter="` + key + `" href="` + html.EscapeString("/chats?"+remaining.Encode()) + `"`
		if !strings.Contains(page[chipsStart:], want) {
			t.Errorf("%s chip does not remove only itself; want %s", key, want)
		}
	}
}

func TestPlainPreviewRendersShellAndCanSubmitConfirmation(t *testing.T) {
	u := newChatsUI(t)
	namedChat(t, u, "family@g.us", "Family", "group", "", dashNow)
	form := url.Values{"jid[]": {"family@g.us"}, "logging": {"off"}}
	body, code, _ := plainPost(t, u.handler(), "/chats/rules/preview", form)
	if code != http.StatusOK {
		t.Fatalf("plain preview: %d: %s", code, body)
	}
	for _, want := range []string{"<!doctype html>", `<link rel="stylesheet" href="/static/app.css">`, `id="preview-title"`, `action="/chats/rules"`, `name="jid[]" value="family@g.us"`} {
		if !strings.Contains(body, want) {
			t.Errorf("plain preview lacks %q: %s", want, body)
		}
	}
	_, code, location := plainPost(t, u.handler(), "/chats/rules", form)
	if code != http.StatusSeeOther || location != "/chats" {
		t.Fatalf("plain confirmation = %d Location %q; want 303 /chats", code, location)
	}
	if !u.rules.Excluded("family@g.us") {
		t.Fatal("plain confirmation did not apply the previewed rule")
	}
}

func plainPost(t *testing.T, h http.Handler, path string, form url.Values) (string, int, string) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	r.Host = "localhost:8080"
	r.Header.Set("Origin", "http://localhost:8080")
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	body, err := io.ReadAll(w.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body), w.Code, w.Header().Get("Location")
}
