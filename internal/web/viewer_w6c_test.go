package web

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"whatsapp-archiver/internal/activity"
	"whatsapp-archiver/internal/ingest"
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
	for _, want := range []string{`dir="auto"`, "مرحبا بالعالم", "View once", "24 hours", "28 October 2025", "Riyadh", "Central", "Open map", `rel="noopener noreferrer"`, "Aisha", "Show vCard", "Lunch?", "Pizza", "1 vote", "1 archived vote", "Results may be incomplete"} {
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
	u.typedMessage(t, store.Message{ID: "missing-reply", Kind: "text", Text: "answer two", QuotedID: "not-archived", QuotedSender: "9@lid", Timestamp: 103})
	raw, _, _ = get(t, u.handler(), "/chats/g@g.us")
	body = string(raw)
	if !strings.Contains(body, "Original message is not archived") || strings.Contains(body, `focus=not-archived`) {
		t.Fatalf("missing quote must be inert rather than a broken jump: %s", body)
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
	body, code, _ = get(t, u.handler(), "/chats/g@g.us?date=2025-10-27")
	if code != http.StatusOK || !strings.Contains(string(body), "No message is archived on that date") {
		t.Fatalf("missing-date jump = %d %s", code, body)
	}
	body, code, _ = get(t, u.handler(), "/chats/g@g.us?before=1761609706&before_id=m106")
	if code != http.StatusOK || !strings.Contains(string(body), "Load newer messages") || !strings.Contains(string(body), "Load older messages") {
		t.Fatalf("middle page has no two-way controls: %d %s", code, body)
	}
	if strings.Count(string(body), `hx-trigger="revealed"`) < 2 || !strings.Contains(string(body), `id="new-message-stream"`) || !strings.Contains(string(body), "new messages") {
		t.Fatalf("middle page lacks two-way revealed loading or the away-from-edge counter: %s", body)
	}
	body, code, _ = get(t, u.handler(), "/chats/g@g.us?after=1761609700&after_id=m100")
	if code != http.StatusOK || !strings.Contains(string(body), "body-101") || strings.Contains(string(body), "body-100") {
		t.Fatalf("forward page skipped its nearest boundary message: %d %s", code, body)
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
	if !strings.Contains(got, `<mark>مرحبا`) || strings.Contains(got, `<mark>before`) {
		t.Fatalf("search must mark only the folded match: %s", got)
	}
	if !regexp.MustCompile(`id="message-[ab]"[^>]*tabindex="-1"`).MatchString(got) {
		t.Fatalf("current search heading is not programmatically focusable: %s", got)
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
	u.typedMessage(t, store.Message{ID: "pending", Kind: "image", Timestamp: 200})
	if err := testkit.InsertMedia(t.Context(), u.st, store.Media{ChatJID: "g@g.us", MessageID: "pending", Type: "image", State: "pending"}); err != nil {
		t.Fatal(err)
	}
	raw, code, _ := get(t, u.handler(), "/chats/g@g.us")
	if code != http.StatusOK {
		t.Fatalf("viewer status %d", code)
	}
	body := string(raw)
	for _, want := range []string{`src="/media/thumb?chat=g%40g.us&amp;id=image"`, `data-original="/media?chat=g%40g.us&amp;id=image"`, `loading="lazy"`, `<video`, `<audio`, `preload="metadata"`, `download`, "document.safe", "42.0 B", "Waiting to download", `/chats/g@g.us/m/pending`} {
		if !strings.Contains(body, want) {
			t.Errorf("viewer lacks %q: %s", want, body)
		}
	}
	w := getMediaPath(t, u.handler(), "/media/thumb?chat=g%40g.us&id=image")
	if w.Code != http.StatusOK || w.Body.String() != "thumb" || w.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("thumbnail = %d %q %q", w.Code, w.Body.String(), w.Header().Get("Content-Type"))
	}
}

func TestViewerSSEUsesAStableLiveCursorAndResumesWithoutDuplicates(t *testing.T) {
	u := newChatsUI(t)
	u.typedMessage(t, store.Message{ID: "z", Kind: "text", Text: "initial", Timestamp: 100})
	bus := activity.NewBus(500)
	s := New(Deps{Store: u.st, Rules: u.rules, Config: u.cfg, Activity: bus})
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)

	page, err := srv.Client().Get(srv.URL + "/chats/g@g.us")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(page.Body)
	_ = page.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`sse-connect="([^"]+)"`).FindStringSubmatch(string(raw))
	if m == nil {
		t.Fatalf("viewer has no SSE URL: %s", raw)
	}
	stream, cancel := openViewerStream(t, srv, m[1], nil)
	u.typedMessage(t, store.Message{ID: "a", Kind: "text", Text: "same-second lower id", Timestamp: 100})
	bus.Publish(activity.Event{Kind: "message", Chat: "g@g.us", Detail: "text"})
	first := next(t, stream, "viewer", 2*time.Second)
	cancel()
	if first.id == "" || !strings.Contains(first.data, "same-second lower id") {
		t.Fatalf("first live event = %#v", first)
	}

	u.typedMessage(t, store.Message{ID: "b", Kind: "text", Text: "after reconnect", Timestamp: 100})
	resumed, stop := openViewerStream(t, srv, m[1], http.Header{"Last-Event-ID": {first.id}})
	defer stop()
	got := next(t, resumed, "viewer", 2*time.Second)
	if !strings.Contains(got.data, "after reconnect") || strings.Contains(got.data, "same-second lower id") {
		t.Fatalf("resumed live event = %#v", got)
	}
}

func TestW6IPipelineMessageReachesViewerSSEWithinTwoSecondsAndLargePayloadRefetches(t *testing.T) {
	u := newChatsUI(t)
	u.typedMessage(t, store.Message{ID: "initial", Kind: "text", Text: "initial", Timestamp: 100})
	bus := activity.NewBus(20)
	pipe := ingest.NewPipeline(u.st, ingest.NewFilter(u.rules, func(jid string) ([]string, error) {
		return u.st.Aliases(context.Background(), jid)
	}), nopQueue{}, nopHealth{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	pipe = pipe.WithActivity(bus)
	s := New(Deps{Store: u.st, Rules: u.rules, Config: u.cfg, Activity: bus})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	page, err := srv.Client().Get(srv.URL + "/chats/g@g.us")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(page.Body)
	_ = page.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	path := regexp.MustCompile(`sse-connect="([^"]+)"`).FindStringSubmatch(string(raw))
	if len(path) != 2 {
		t.Fatalf("viewer has no SSE path: %s", raw)
	}
	stream, stop := openViewerStream(t, srv, path[1], nil)
	defer stop()
	started := time.Now()
	pipe.Handle(liveText("g@g.us", "normal-live", "visible synthetic live message"))
	normal := next(t, stream, "viewer", 2*time.Second)
	if time.Since(started) >= 2*time.Second || !strings.Contains(normal.data, "visible synthetic live message") {
		t.Fatalf("normal live event late/missing: elapsed=%s event=%#v", time.Since(started), normal)
	}
	largeText := strings.Repeat("archived-large-payload-", 4096)
	pipe.Handle(liveText("g@g.us", "large-live", largeText))
	large := next(t, stream, "viewer-refetch", 2*time.Second)
	if len(large.data) >= 16<<10 || !strings.Contains(large.data, "/chats/g@g.us/m/large-live") || strings.Contains(large.data, largeText[:1024]) {
		t.Fatalf("large live event is not a bounded refetch contract: bytes=%d event=%#v", len(large.data), large)
	}
	detail, err := srv.Client().Get(srv.URL + "/chats/g@g.us/m/large-live")
	if err != nil {
		t.Fatal(err)
	}
	detailBody, err := io.ReadAll(detail.Body)
	_ = detail.Body.Close()
	if err != nil || detail.StatusCode != http.StatusOK || !bytes.Contains(detailBody, []byte(largeText[:1024])) {
		t.Fatalf("full archived content unavailable after refetch marker: status=%d err=%v", detail.StatusCode, err)
	}
}

func openViewerStream(t *testing.T, srv *httptest.Server, path string, header http.Header) (<-chan sseEvent, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+path, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	for key, values := range header {
		req.Header[key] = values
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		cancel()
		_ = resp.Body.Close()
		t.Fatalf("viewer stream status %d", resp.StatusCode)
	}
	t.Cleanup(func() { cancel(); _ = resp.Body.Close() })
	return readEvents(ctx, resp.Body), cancel
}

func getMediaPath(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Host = "localhost:8080"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
