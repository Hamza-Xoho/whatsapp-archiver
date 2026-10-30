package web

import (
	"net/http"
	"strings"
	"testing"
)

func TestGlobalSearchIsReachableAndRetainsResultsDuringRequests(t *testing.T) {
	u := newChatsUI(t)
	u.message(t, "g@g.us", "needle")

	chats, code, _ := get(t, u.handler(), "/chats")
	if code != http.StatusOK || !strings.Contains(string(chats), `href="/search"`) {
		t.Fatalf("chats page does not link to global search: status=%d body=%s", code, chats)
	}
	search, code, _ := get(t, u.handler(), "/search?q=needle")
	if code != http.StatusOK {
		t.Fatalf("search status=%d body=%s", code, search)
	}
	page := string(search)
	for _, want := range []string{`hx-get="/search"`, `hx-target="#main"`, `hx-select="#main"`, `hx-push-url="true"`} {
		if !strings.Contains(page, want) {
			t.Errorf("search form lacks %s: %s", want, page)
		}
	}
}

func TestGlobalSearchFailureUsesSpecifiedRecoverableCopy(t *testing.T) {
	u := newChatsUI(t)
	if err := u.st.Close(); err != nil {
		t.Fatal(err)
	}
	body, code, _ := get(t, u.handler(), "/search?q=needle")
	if code != http.StatusInternalServerError || strings.TrimSpace(string(body)) != "Search is unavailable right now. Try again." {
		t.Fatalf("search failure=%d %q", code, body)
	}
}
