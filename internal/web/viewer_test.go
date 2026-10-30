package web

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"whatsapp-archiver/internal/store"
)

func (u *chatsUI) msg(t *testing.T, chat, id, sender string, ts int64, text string) {
	t.Helper()
	m := store.Message{ChatJID: chat, ID: id, SenderJID: sender, Timestamp: ts, ReceivedAt: ts, Source: "live", Text: text, RawProto: []byte(id)}
	if _, err := u.st.InsertMessage(t.Context(), m); err != nil {
		t.Fatal(err)
	}
}

func (u *chatsUI) event(t *testing.T, e store.MessageEvent) {
	t.Helper()
	e.ReceivedAt = e.Timestamp
	if _, err := u.st.InsertEvent(t.Context(), e); err != nil {
		t.Fatal(err)
	}
}

func TestViewerShowsEditedTextAndKeepsOriginal(t *testing.T) {
	u := newChatsUI(t)
	u.msg(t, "g@g.us", "a", "1@lid", 1760000000, "ORIGINAL-WORDS")
	u.event(t, store.MessageEvent{ChatJID: "g@g.us", TargetID: "a", Kind: "edit", EventID: "e1", NewText: "EDITED-WORDS", Timestamp: 1760000060})
	got := u.page(t, "/chats/g@g.us")
	if !strings.Contains(got, "EDITED-WORDS") || !strings.Contains(got, "ORIGINAL-WORDS") || !strings.Contains(got, "Edited") {
		t.Fatalf("viewer must show the edit, the original and say it was edited: %s", got)
	}
	if strings.Index(got, "EDITED-WORDS") > strings.Index(got, "ORIGINAL-WORDS") {
		t.Fatal("the current (edited) text should come before the original")
	}
}

func TestViewerMarksDeletedMessage(t *testing.T) {
	u := newChatsUI(t)
	u.msg(t, "g@g.us", "a", "1@lid", 1760000000, "KEPT-AFTER-DELETE")
	u.event(t, store.MessageEvent{ChatJID: "g@g.us", TargetID: "a", Kind: "revoke", EventID: "r1", Timestamp: 1760000060})
	got := u.page(t, "/chats/g@g.us")
	if !strings.Contains(got, "Deleted by the sender") || !strings.Contains(got, "KEPT-AFTER-DELETE") {
		t.Fatalf("viewer must mark the deletion and keep the text: %s", got)
	}
}

func TestViewerListsReactions(t *testing.T) {
	u := newChatsUI(t)
	u.msg(t, "g@g.us", "a", "1@lid", 1760000000, "hello")
	u.event(t, store.MessageEvent{ChatJID: "g@g.us", TargetID: "a", Kind: "reaction", EventID: "x1", SenderJID: "p1@lid", Value: "👍", Timestamp: 1760000010})
	u.event(t, store.MessageEvent{ChatJID: "g@g.us", TargetID: "a", Kind: "reaction", EventID: "x2", SenderJID: "p2@lid", Value: "❤️", Timestamp: 1760000020})
	got := u.page(t, "/chats/g@g.us")
	if !strings.Contains(got, "👍") || !strings.Contains(got, "❤️") {
		t.Fatalf("viewer lacks the reactions: %s", got)
	}
}

func TestViewerShowsSenderNameAndChatName(t *testing.T) {
	u := newChatsUI(t)
	u.msg(t, "g@g.us", "a", "5551@s.whatsapp.net", 1760000000, "hello")
	if err := u.st.UpsertParticipant(t.Context(), store.Participant{JID: "5551@s.whatsapp.net", PushName: "Aisha"}); err != nil {
		t.Fatal(err)
	}
	if err := u.st.UpsertChat(t.Context(), store.Chat{JID: "g@g.us", Name: "Family", Type: "group"}); err != nil {
		t.Fatal(err)
	}
	got := u.page(t, "/chats/g@g.us")
	if !strings.Contains(got, "Aisha") || !strings.Contains(got, "Family") {
		t.Fatalf("viewer lacks the sender or chat name: %s", got)
	}
}

var olderRe = regexp.MustCompile(`href="/chats/g@g\.us\?before=(\d+)&amp;before_id=([^"&]+)"`)

// Review Focus: a large chat pages, newest 100 first, with an "Older" link carrying the 100th message's time.
func TestViewerPagesLargeChat(t *testing.T) {
	u := newChatsUI(t)
	const base = 1760000000
	for i := 1; i <= 1000; i++ {
		u.msg(t, "g@g.us", fmt.Sprintf("m%04d", i), "1@lid", base+int64(i), fmt.Sprintf("body-%04d", i))
	}
	body, code, _ := get(t, u.handler(), "/chats/g@g.us")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if n := strings.Count(string(body), "body-"); n != 100 {
		t.Fatalf("first page shows %d messages, want 100", n)
	}
	if !strings.Contains(string(body), "body-1000") || !strings.Contains(string(body), "body-0901") || strings.Contains(string(body), "body-0900") {
		t.Fatal("first page is not the newest 100")
	}
	m := olderRe.FindStringSubmatch(string(body))
	if m == nil || m[1] != fmt.Sprint(base+901) || m[2] != "m0901" {
		t.Fatalf("Older link %v, want before=%d&before_id=m0901", m, base+901)
	}
	if !strings.Contains(visibleText(string(body)), "Older") {
		t.Fatal("no Older link text")
	}
	body, code, _ = get(t, u.handler(), "/chats/g@g.us?before="+m[1]+"&before_id="+m[2])
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if n := strings.Count(string(body), "body-"); n != 100 || !strings.Contains(string(body), "body-0900") || strings.Contains(string(body), "body-0901") || !strings.Contains(string(body), "body-0801") {
		t.Fatalf("second page is not messages 801-900 (%d shown)", n)
	}
}

func TestViewerLastPageHasNoOlderLink(t *testing.T) {
	u := newChatsUI(t)
	for i := 1; i <= 100; i++ {
		u.msg(t, "g@g.us", fmt.Sprintf("m%04d", i), "1@lid", 1760000000+int64(i), "x")
	}
	body, _, _ := get(t, u.handler(), "/chats/g@g.us")
	if olderRe.Match(body) {
		t.Fatal("an Older link on a chat with exactly one page")
	}
}

func TestViewerRejectsBadBefore(t *testing.T) {
	u := newChatsUI(t)
	if _, code, _ := get(t, u.handler(), "/chats/g@g.us?before=abc"); code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", code)
	}
}

// Message text and names come from any contact: they are escaped, never markup.
func TestViewerEscapesHostileText(t *testing.T) {
	u := newChatsUI(t)
	const hostile = "<script>alert(1)</script>"
	u.msg(t, "g@g.us", "a", "5551@s.whatsapp.net", 1760000000, hostile)
	if err := u.st.UpsertParticipant(t.Context(), store.Participant{JID: "5551@s.whatsapp.net", PushName: hostile}); err != nil {
		t.Fatal(err)
	}
	if err := u.st.UpsertChat(t.Context(), store.Chat{JID: "g@g.us", Name: hostile, Type: "group"}); err != nil {
		t.Fatal(err)
	}
	u.event(t, store.MessageEvent{ChatJID: "g@g.us", TargetID: "a", Kind: "edit", EventID: "e1", NewText: hostile, Timestamp: 1760000001})
	u.event(t, store.MessageEvent{ChatJID: "g@g.us", TargetID: "a", Kind: "reaction", EventID: "x1", SenderJID: "p@lid", Value: hostile, Timestamp: 1760000002})
	for _, path := range []string{"/chats/g@g.us", "/chats/g@g.us/m/a", "/chats"} {
		body, code, _ := get(t, u.handler(), path)
		if code != http.StatusOK {
			t.Fatalf("%s: status %d", path, code)
		}
		if strings.Contains(string(body), hostile) || strings.Contains(string(body), "<script>alert") {
			t.Errorf("%s: hostile text rendered as markup", path)
		}
		if !strings.Contains(string(body), "&lt;script&gt;alert(1)&lt;/script&gt;") {
			t.Errorf("%s: hostile text not shown escaped", path)
		}
	}
}

func TestTraceShowsMessageAndEvents(t *testing.T) {
	u := newChatsUI(t)
	u.msg(t, "g@g.us", "a", "1@lid", 1760000000, "hello")
	u.event(t, store.MessageEvent{ChatJID: "g@g.us", TargetID: "a", Kind: "edit", EventID: "e1", NewText: "hi there", Timestamp: 1760000060})
	got := u.page(t, "/chats/g@g.us/m/a")
	for _, want := range []string{"hello", "edit", "hi there", "live"} {
		if !strings.Contains(got, want) {
			t.Errorf("trace lacks %q: %s", want, got)
		}
	}
	page := u.page(t, "/chats/g@g.us")
	if !strings.Contains(page, "Details") {
		t.Fatal("viewer has no link to the message trace")
	}
}

func TestTraceShowsGapForUndecryptable(t *testing.T) {
	u := newChatsUI(t)
	u.msg(t, "g@g.us", "a", "1@lid", 1760000000, "hello")
	u.event(t, store.MessageEvent{ChatJID: "g@g.us", TargetID: "lost", Kind: "undecryptable", EventID: "u1", SenderJID: "1@lid", Timestamp: 1760000060})
	got := u.page(t, "/chats/g@g.us/m/lost")
	if !strings.Contains(got, "could not be decrypted") {
		t.Fatalf("trace does not show the gap: %s", got)
	}
	if _, code, _ := get(t, u.handler(), "/chats/g@g.us/m/none"); code != http.StatusNotFound {
		t.Fatalf("unknown message: status %d, want 404", code)
	}
}

// I1: when the 100th and 101st newest messages share a second, the 101st is on the second page.
func TestViewerOlderPageKeepsMessageSharingBoundarySecond(t *testing.T) {
	u := newChatsUI(t)
	const base = 1760000000
	for i := 1; i <= 101; i++ {
		ts := base + int64(i)
		if i == 1 {
			ts = base + 2 // m0001 (101st newest) shares the second of m0002 (100th newest)
		}
		u.msg(t, "g@g.us", fmt.Sprintf("m%04d", i), "1@lid", ts, fmt.Sprintf("body-%04d", i))
	}
	body, _, _ := get(t, u.handler(), "/chats/g@g.us")
	m := olderRe.FindStringSubmatch(string(body))
	if m == nil || strings.Contains(string(body), "body-0001") {
		t.Fatalf("first page: Older link %v", m)
	}
	body, code, _ := get(t, u.handler(), "/chats/g@g.us?before="+m[1]+"&before_id="+m[2])
	if code != http.StatusOK || !strings.Contains(string(body), "body-0001") || strings.Contains(string(body), "body-0002") {
		t.Fatalf("second page (status %d) lacks message 101 or repeats 100: %s", code, visibleText(string(body)))
	}
}

// Message ids come from other phones: a "?" or "/" in one is escaped in links, and the link still opens the trace.
func TestMessageIDIsPathEscapedInLinks(t *testing.T) {
	u := newChatsUI(t)
	u.msg(t, "g@g.us", "a?b/c", "1@lid", 1760000000, "odd id")
	m := store.Message{ChatJID: "g@g.us", ID: "r", SenderJID: "1@lid", Timestamp: 1760000001, ReceivedAt: 1760000001, Source: "live", Text: "reply", QuotedID: "a?b/c", RawProto: []byte("r")}
	if _, err := u.st.InsertMessage(t.Context(), m); err != nil {
		t.Fatal(err)
	}
	body, _, _ := get(t, u.handler(), "/chats/g@g.us")
	if !strings.Contains(string(body), `href="/chats/g@g.us/m/a%3Fb%2Fc"`) {
		t.Fatalf("viewer link not path-escaped: %s", body)
	}
	trace, code, _ := get(t, u.handler(), "/chats/g@g.us/m/r")
	if code != http.StatusOK || !strings.Contains(string(trace), `href="/chats/g@g.us/m/a%3Fb%2Fc"`) {
		t.Fatalf("trace reply link not path-escaped (status %d): %s", code, trace)
	}
	got, code, _ := get(t, u.handler(), "/chats/g@g.us/m/a%3Fb%2Fc")
	if code != http.StatusOK || !strings.Contains(string(got), "odd id") {
		t.Fatalf("escaped link does not open the trace: status %d", code)
	}
}

// A message with no body this build knows (kind unknown) or no file (location) shows no media line in the viewer:
// before V1 its media_type was empty and nothing appeared under it.
func TestViewerShowsNoMediaLineForKindsWithoutAFile(t *testing.T) {
	u := newChatsUI(t)
	for i, kind := range []string{"unknown", "location"} {
		m := store.Message{ChatJID: "g@g.us", ID: fmt.Sprint("k", i), SenderJID: "1@lid", Timestamp: int64(1760000000 + i), ReceivedAt: 1, Source: "live", Kind: kind, RawProto: []byte("x")}
		if _, err := u.st.InsertMessage(t.Context(), m); err != nil {
			t.Fatal(err)
		}
	}
	got := u.page(t, "/chats/g@g.us")
	for _, bad := range []string{"unknown", "location", "not downloaded"} {
		if strings.Contains(got, bad) {
			t.Fatalf("the viewer shows %q for a message without a file: %s", bad, got)
		}
	}
}
