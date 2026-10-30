package web

import (
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"whatsapp-archiver/internal/store"
)

func TestGlobalSearchHighlightsFoldedMatchesWithEscapedTextSegments(t *testing.T) {
	u := newChatsUI(t)
	addWebSearchMessage(t, u.st, webSearchMessage("g@g.us", "arabic", "1@lid", "text", "قبل المَدْرَسَة <script>alert(1)</script> بعد", 20))
	addWebSearchMessage(t, u.st, webSearchMessage("g@g.us", "ordinary", "1@lid", "text", "before ordinary needle after", 10))

	for query, wantMark := range map[string]string{"مدرسه": "المَدْرَسَة", "ordinary needle": "ordinary needle"} {
		body, code, _ := get(t, u.handler(), "/search?q="+url.QueryEscape(query))
		if code != http.StatusOK {
			t.Fatalf("Search(%q) = %d: %s", query, code, body)
		}
		page := string(body)
		if !strings.Contains(page, `<p class="search-context" dir="auto">`) || !strings.Contains(page, "<mark>"+wantMark+"</mark>") {
			t.Errorf("Search(%q) does not safely highlight %q with dir=auto: %s", query, wantMark, page)
		}
		if strings.Contains(page, "<script>alert(1)</script>") {
			t.Fatalf("Search(%q) emitted active user markup: %s", query, page)
		}
	}

	segments := highlightSearch(`x <img src=x onerror=alert(1)> ordinary`, "ordinary")
	if len(segments) == 0 || reflect.TypeOf(segments[0].Text).Kind() != reflect.String {
		t.Fatalf("highlight segments are not plain strings: %#v", segments)
	}
}

func TestGlobalSearchEditOnlyResultJumpsToTargetWithEditContext(t *testing.T) {
	u := newChatsUI(t)
	addWebSearchMessage(t, u.st, webSearchMessage("g@g.us", "target/id", "1@lid", "text", "old words", 10))
	if _, err := u.st.InsertEvent(t.Context(), store.MessageEvent{ChatJID: "g@g.us", TargetID: "target/id", Kind: "edit", EventID: "e", NewText: "unique replacement", Timestamp: 20, ReceivedAt: 20}); err != nil {
		t.Fatal(err)
	}
	body, code, _ := get(t, u.handler(), "/search?q=unique+replacement")
	if code != http.StatusOK {
		t.Fatalf("edit Search = %d: %s", code, body)
	}
	page := string(body)
	wantJump := "/chats/g@g.us?at=" + url.QueryEscape("target/id") + "#message-" + url.PathEscape("target/id")
	for _, want := range []string{"Edited", "<mark>unique replacement</mark>", wantJump} {
		if !strings.Contains(page, want) {
			t.Errorf("edit Search lacks %q: %s", want, page)
		}
	}
	viewer, code, _ := get(t, u.handler(), "/chats/g@g.us?at="+url.QueryEscape("target/id"))
	if code != http.StatusOK {
		t.Fatalf("message jump = %d: %s", code, viewer)
	}
	for _, want := range []string{`id="message-target/id"`, `tabindex="-1"`, "autofocus", "old words"} {
		if !strings.Contains(string(viewer), want) {
			t.Errorf("focused viewer lacks %q: %s", want, viewer)
		}
	}
}
