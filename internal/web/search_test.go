package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"whatsapp-archiver/internal/store"
)

func webSearchMessage(chat, id, sender, kind, text string, ts int64) store.Message {
	return store.Message{ChatJID: chat, ID: id, SenderJID: sender, Kind: kind, Text: text,
		Source: "live", Timestamp: ts, ReceivedAt: ts, RawProto: []byte(id)}
}

func addWebSearchMessage(t *testing.T, st *store.Store, m store.Message) {
	t.Helper()
	if _, err := st.InsertMessage(t.Context(), m); err != nil {
		t.Fatal(err)
	}
}

func TestGlobalSearchPageStatesGroupingBadgesAndSafeJumps(t *testing.T) {
	u := newChatsUI(t)
	ctx := t.Context()
	for _, c := range []store.Chat{{JID: "a@g.us", Name: "Family"}, {JID: "b@g.us", Name: "Work"}} {
		if err := u.st.UpsertChat(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	for _, m := range []store.Message{
		webSearchMessage("a@g.us", "odd/id?x", "1@lid", "text", "needle <script>alert(1)</script>", 100),
		webSearchMessage("a@g.us", "a2", "1@lid", "text", "another needle", 90),
		webSearchMessage("b@g.us", "b1", "2@lid", "text", "work needle", 80),
	} {
		addWebSearchMessage(t, u.st, m)
	}
	if _, err := u.st.InsertEvent(ctx, store.MessageEvent{ChatJID: "a@g.us", TargetID: "odd/id?x", Kind: "revoke", EventID: "r", Timestamp: 101}); err != nil {
		t.Fatal(err)
	}
	if _, err := u.st.InsertEvent(ctx, store.MessageEvent{ChatJID: "a@g.us", TargetID: "a2", Kind: "edit", EventID: "e", NewText: "edited", Timestamp: 91}); err != nil {
		t.Fatal(err)
	}

	body, code, _ := get(t, u.handler(), "/search")
	if code != http.StatusOK {
		t.Fatalf("initial search = %d: %s", code, body)
	}
	page := string(body)
	for _, want := range []string{"Search archived messages", "Words or part of a word", "Search text and captions across all logged chats."} {
		if !strings.Contains(visibleText(page), want) && !strings.Contains(page, want) {
			t.Errorf("initial search lacks %q", want)
		}
	}

	body, code, _ = get(t, u.handler(), "/search?q=needle")
	if code != http.StatusOK {
		t.Fatalf("search = %d: %s", code, body)
	}
	page = string(body)
	text := visibleText(page)
	if strings.Count(text, "Family") != 1 || strings.Count(text, "Work") != 1 {
		t.Errorf("results are not grouped once per chat: %s", text)
	}
	for _, want := range []string{"3 results in 2 chats", "Deleted", "Edited", "Open message"} {
		if !strings.Contains(text, want) {
			t.Errorf("results lack %q: %s", want, text)
		}
	}
	wantJump := "/chats/a@g.us/m/" + url.PathEscape("odd/id?x")
	if !strings.Contains(page, wantJump) {
		t.Errorf("message jump is not path escaped; want %q in %s", wantJump, page)
	}
	if strings.Contains(page, "<script>alert(1)</script>") || !strings.Contains(page, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Errorf("search context was not escaped: %s", page)
	}
	if !strings.Contains(page, `dir="auto"`) {
		t.Error("search excerpts do not use dir=auto")
	}
}

func TestGlobalSearchNoResultAndFilterValidation(t *testing.T) {
	u := newChatsUI(t)
	u.message(t, "a@g.us", "one")
	body, code, _ := get(t, u.handler(), "/search?q=absent")
	if code != http.StatusOK || !strings.Contains(visibleText(string(body)), "No archived messages match absent") {
		t.Fatalf("no result = %d: %s", code, body)
	}
	for _, path := range []string{"/search?q=one&from=bad", "/search?q=one&to=-1", "/search?q=one&limit=100"} {
		if body, code, _ := get(t, u.handler(), path); code != http.StatusBadRequest {
			t.Errorf("GET %s = %d, want 400: %s", path, code, body)
		}
	}
}
