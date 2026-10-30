package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

func (u *chatsUI) typedMessage(t *testing.T, m store.Message) {
	t.Helper()
	if m.ChatJID == "" {
		m.ChatJID = "g@g.us"
	}
	if m.SenderJID == "" {
		m.SenderJID = "1@lid"
	}
	if m.Source == "" {
		m.Source = "live"
	}
	if m.ReceivedAt == 0 {
		m.ReceivedAt = m.Timestamp
	}
	if m.RawProto == nil {
		m.RawProto = []byte(m.ID)
	}
	if _, err := u.st.InsertMessage(t.Context(), m); err != nil {
		t.Fatal(err)
	}
}

func TestViewerRendersTypedMessagesEventsAndBidiSafely(t *testing.T) {
	u := newChatsUI(t)
	u.typedMessage(t, store.Message{ID: "text", Kind: "text", Text: "مرحبا بالعالم", Timestamp: 1761609600, ViewOnce: true, EphemeralSecs: 86400})
	u.typedMessage(t, store.Message{ID: "location", Kind: "location", Timestamp: 1761609660, Details: `{"lat":24.7136,"lon":46.6753,"name":"Riyadh","address":"Central"}`})
	u.typedMessage(t, store.Message{ID: "contact", Kind: "contact", Text: "Aisha", Timestamp: 1761609720, Details: `{"display_name":"Aisha","vcard":"BEGIN:VCARD\\nFN:Aisha\\nEND:VCARD"}`})
	u.typedMessage(t, store.Message{ID: "poll", Kind: "poll", Text: "Lunch?", Timestamp: 1761609780, Details: `{"question":"Lunch?","options":["Rice","Pizza"],"max":1}`})
	u.event(t, store.MessageEvent{ChatJID: "g@g.us", TargetID: "poll", Kind: "poll_vote", EventID: "v1", SenderJID: "2@lid", Value: `{"options":["Pizza"]}`, Timestamp: 1761609790})
	u.event(t, store.MessageEvent{ChatJID: "g@g.us", TargetID: "poll", Kind: "poll_vote", EventID: "v2", SenderJID: "3@lid", Value: "", Timestamp: 1761609791})

	raw, code, _ := get(t, u.handler(), "/chats/g@g.us")
	if code != http.StatusOK {
		t.Fatalf("viewer status %d", code)
	}
	body := string(raw)
	for _, want := range []string{`dir="auto"`, "مرحبا بالعالم", "View once", "24 hours", "28 October 2025", "Riyadh", "Central", "Open map", `rel="noopener noreferrer"`, "Aisha", "Show vCard", "Lunch?", "Pizza", "1 vote", "Results may be incomplete"} {
		if !strings.Contains(body, want) {
			t.Errorf("viewer lacks %q: %s", want, body)
		}
	}
	if strings.Contains(body, `<iframe`) || strings.Contains(body, `href="javascript:`) {
		t.Fatalf("viewer rendered active third-party content: %s", body)
	}
}

func TestViewerQuotedJumpReactionCountsAndDeletedDefault(t *testing.T) {
	u := newChatsUI(t)
	u.typedMessage(t, store.Message{ID: "original", Kind: "text", Text: "the original words", SenderJID: "2@lid", Timestamp: 100})
	u.typedMessage(t, store.Message{ID: "reply", Kind: "text", Text: "answer", QuotedID: "original", QuotedSender: "2@lid", Timestamp: 101})
	u.typedMessage(t, store.Message{ID: "gone", Kind: "text", Text: "archived secret", Timestamp: 102})
	for i, sender := range []string{"2@lid", "3@lid", "4@lid"} {
		u.event(t, store.MessageEvent{ChatJID: "g@g.us", TargetID: "reply", Kind: "reaction", EventID: fmt.Sprintf("r%d", i), SenderJID: sender, Value: "👍", Timestamp: int64(110 + i)})
	}
	u.event(t, store.MessageEvent{ChatJID: "g@g.us", TargetID: "gone", Kind: "revoke", EventID: "delete", Timestamp: 120})

	raw, code, _ := get(t, u.handler(), "/chats/g@g.us")
	if code != http.StatusOK {
		t.Fatalf("viewer status %d", code)
	}
	body := string(raw)
	for _, want := range []string{`href="/chats/g@g.us?focus=original#message-original"`, "the original words", "👍", "3", "This message was deleted", "Show deleted text"} {
		if !strings.Contains(body, want) {
			t.Errorf("viewer lacks %q: %s", want, body)
		}
	}
	if body, code, _ := get(t, u.handler(), "/chats/g@g.us?focus=missing"); code != http.StatusNotFound || !strings.Contains(string(body), "404") {
		t.Fatalf("foreign/missing focus = %d %s, want 404", code, body)
	}
}

func TestViewerFiltersDateJumpAndBothDirectionCursors(t *testing.T) {
	u := newChatsUI(t)
	for i := 1; i <= 205; i++ {
		kind, sender := "text", "1@lid"
		if i%2 == 0 {
			kind, sender = "image", "2@lid"
		}
		u.typedMessage(t, store.Message{ID: fmt.Sprintf("m%03d", i), Kind: kind, Text: fmt.Sprintf("body-%03d", i), SenderJID: sender, Timestamp: 1761609600 + int64(i)})
	}

	q := url.Values{"sender": {"2@lid"}, "media": {"image"}, "from": {"2025-10-28"}, "to": {"2025-10-28"}}
	body, code, _ := get(t, u.handler(), "/chats/g@g.us?"+q.Encode())
	if code != http.StatusOK || strings.Contains(string(body), "body-205") || !strings.Contains(string(body), "body-204") {
		t.Fatalf("filtered viewer = %d %s", code, body)
	}
	body, code, _ = get(t, u.handler(), "/chats/g@g.us?date=2025-10-28")
	if code != http.StatusOK || !strings.Contains(string(body), `id="message-`) || !strings.Contains(string(body), "nearest archived message") {
		t.Fatalf("date jump = %d %s", code, body)
	}
	body, code, _ = get(t, u.handler(), "/chats/g@g.us?before=1761609706&before_id=m106")
	if code != http.StatusOK || !strings.Contains(string(body), "Load newer messages") || !strings.Contains(string(body), "Load older messages") {
		t.Fatalf("middle page has no two-way controls: %d %s", code, body)
	}
}

func TestViewerInChatSearchHighlightsAndNavigatesSafely(t *testing.T) {
	u := newChatsUI(t)
	u.typedMessage(t, store.Message{ID: "a", Kind: "text", Text: `before <script> مرحباً after`, Timestamp: 100})
	u.typedMessage(t, store.Message{ID: "b", Kind: "text", Text: "مرحبا again", Timestamp: 101})
	u.typedMessage(t, store.Message{ID: "c", Kind: "text", Text: "unrelated", Timestamp: 102})
	body, code, _ := get(t, u.handler(), "/chats/g@g.us?q="+url.QueryEscape("مرحبا"))
	got := string(body)
	if code != http.StatusOK || !strings.Contains(got, "1 of 2") || !strings.Contains(got, "Next match") || !strings.Contains(got, `<mark>`) || !strings.Contains(got, `aria-current="true"`) {
		t.Fatalf("search navigation = %d %s", code, got)
	}
	if strings.Contains(got, "<script>alert") || !strings.Contains(got, "&lt;script&gt;") {
		t.Fatalf("highlight injected unescaped content: %s", got)
	}
}

func TestViewerUsesStoredThumbnailAndNativeMediaControls(t *testing.T) {
	u := newChatsUI(t)
	for i, kind := range []string{"image", "video", "audio", "document"} {
		id := kind
		u.typedMessage(t, store.Message{ID: id, Kind: kind, Caption: kind + " caption", Timestamp: int64(100 + i)})
		if err := testkit.InsertMedia(t.Context(), u.st, store.Media{ChatJID: "g@g.us", MessageID: id, Type: kind, MIME: map[string]string{"image": "image/png", "video": "video/mp4", "audio": "audio/ogg", "document": "application/pdf"}[kind], Filename: id + ".safe", DeclaredSize: 42, State: "done", Thumb: []byte("thumb")}); err != nil {
			t.Fatal(err)
		}
	}
	raw, code, _ := get(t, u.handler(), "/chats/g@g.us")
	if code != http.StatusOK {
		t.Fatalf("viewer status %d", code)
	}
	body := string(raw)
	for _, want := range []string{`src="/media/thumb?chat=g%40g.us&amp;id=image"`, `loading="lazy"`, `<video`, `<audio`, `preload="metadata"`, `download`, "document.safe", "42.0 B"} {
		if !strings.Contains(body, want) {
			t.Errorf("viewer lacks %q: %s", want, body)
		}
	}
	w := getMediaPath(t, u.handler(), "/media/thumb?chat=g%40g.us&id=image")
	if w.Code != http.StatusOK || w.Body.String() != "thumb" || w.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("thumbnail = %d %q %q", w.Code, w.Body.String(), w.Header().Get("Content-Type"))
	}
}

func getMediaPath(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Host = "localhost:8080"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
