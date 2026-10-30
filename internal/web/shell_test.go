package web

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/testkit"
)

func TestRootRedirectsToChats(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Host = "localhost:8080"
	w := httptest.NewRecorder()
	New(Deps{}).Handler().ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/chats" {
		t.Fatalf("GET / = %d Location %q, want 303 /chats", w.Code, w.Header().Get("Location"))
	}
}

func TestPrimaryNavigationHasOnlyThreeAreasAndCurrentPage(t *testing.T) {
	h := New(Deps{}).Handler()
	for path, current := range map[string]string{"/chats": "Chats", "/logging": "Logging", "/pair": "Pairing"} {
		body, code, _ := get(t, h, path)
		if code != http.StatusOK {
			t.Fatalf("GET %s = %d", path, code)
		}
		page := string(body)
		for _, label := range []string{"Chats", "Logging", "Pairing"} {
			want := `>` + label + `</a>`
			if strings.Count(page, want) != 1 {
				t.Errorf("GET %s: %q count = %d", path, want, strings.Count(page, want))
			}
		}
		if !strings.Contains(page, `aria-current="page">`+current+`</a>`) {
			t.Errorf("GET %s: %s is not current", path, current)
		}
		for _, old := range []string{`>Dashboard</a>`, `>Storage</a>`, `>Backups</a>`, `>Settings</a>`} {
			if strings.Contains(page, old) {
				t.Errorf("GET %s retains primary link %s", path, old)
			}
		}
	}
}

func TestSSEIsScopedToLivePagesAndFeedExistsOnlyOnLogging(t *testing.T) {
	h := New(Deps{}).Handler()
	for _, path := range []string{"/chats", "/settings", "/storage", "/backups"} {
		body, code, _ := get(t, h, path)
		if code != http.StatusOK {
			t.Fatalf("GET %s = %d", path, code)
		}
		if strings.Contains(string(body), `sse-connect="/events"`) || strings.Contains(string(body), `id="feed"`) {
			t.Errorf("GET %s opens SSE or contains the live feed", path)
		}
	}
	body, code, _ := get(t, h, "/logging")
	if code != http.StatusOK || strings.Count(string(body), `sse-connect="/events"`) != 1 || strings.Count(string(body), `id="feed"`) != 1 {
		t.Fatalf("Logging SSE/feed scope is wrong: status %d", code)
	}
}

func TestShellHasThemeSkipLinkAndGlobalHTMXErrorToast(t *testing.T) {
	body, code, _ := get(t, New(Deps{}).Handler(), "/chats")
	if code != http.StatusOK {
		t.Fatalf("GET /chats = %d", code)
	}
	page := string(body)
	for _, want := range []string{`class="skip-link"`, `id="main"`, `name="theme"`, `value="system"`, `value="light"`, `value="dark"`, `id="toast"`, `htmx:responseError`, "That change did not go through.", "localStorage"} {
		if !strings.Contains(page, want) {
			t.Errorf("shell lacks %q", want)
		}
	}
}

func TestEveryChatPathEscapesJIDsAndUnknownChatIs404(t *testing.T) {
	u := newChatsUI(t)
	jid := "odd/name?part#end@g.us"
	u.message(t, jid, "id/with?parts")
	body, code, _ := get(t, u.handler(), "/chats")
	if code != http.StatusOK {
		t.Fatalf("GET /chats = %d", code)
	}
	escaped := url.PathEscape(jid)
	if !strings.Contains(string(body), `/chats/`+escaped) || strings.Contains(string(body), `/chats/`+jid) {
		t.Fatalf("chat JID was not path escaped:\n%s", body)
	}
	if _, code, _ := get(t, u.handler(), "/chats/not-known%40g.us"); code != http.StatusNotFound {
		t.Fatalf("unknown chat = %d, want 404", code)
	}
}

func TestLoggingContainsLegacyOperationalContentAndSettings(t *testing.T) {
	h := New(Deps{Store: testkit.Store(t), Rules: ingest.NewRuleSet(nil)}).Handler()
	body, code, _ := get(t, h, "/logging")
	if code != http.StatusOK {
		t.Fatalf("GET /logging = %d: %s", code, body)
	}
	text := visibleText(string(body))
	for _, want := range []string{"Connection", "Archive", "Storage & backups", "Space", "Nightly backup", "Settings", "Media rules", "Alert thresholds", "Backup schedule", "Live feed"} {
		if !strings.Contains(text, want) {
			t.Errorf("Logging lacks folded content %q", want)
		}
	}
}

func TestMiddlewareRecoversAndLogsOnlySafeRouteFacts(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	h := recoverRequests(logger)(timeRequests(logger)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("secret form value")
	})))
	r := httptest.NewRequest(http.MethodGet, "/chats/private%40g.us?token=secret", nil)
	r.Pattern = "GET /chats/{jid}"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusInternalServerError || strings.TrimSpace(w.Body.String()) != "internal server error" {
		t.Fatalf("panic response = %d %q", w.Code, w.Body.String())
	}
	got := logs.String()
	if !strings.Contains(got, "request panic") || !strings.Contains(got, "GET /chats/{jid}") || strings.Contains(got, "private") || strings.Contains(got, "secret") {
		t.Fatalf("unsafe or incomplete recovery log:\n%s", got)
	}
}

func TestResponsiveThemeCSSDoesNotHideTableData(t *testing.T) {
	body, code, _ := get(t, New(Deps{}).Handler(), "/static/app.css")
	if code != http.StatusOK {
		t.Fatalf("GET app.css = %d", code)
	}
	css := string(body)
	for _, want := range []string{"[data-theme=\"light\"]", "[data-theme=\"dark\"]", "@media (max-width: 40rem)", "min-height: 44px", "prefers-reduced-motion"} {
		if !strings.Contains(css, want) {
			t.Errorf("CSS lacks %q", want)
		}
	}
	if strings.Contains(css, "nth-child") {
		t.Fatal("mobile CSS hides table data with nth-child")
	}
}
