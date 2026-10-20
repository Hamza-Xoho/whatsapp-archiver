package web

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

// chatsUI is a server over a real store and a real RuleSet, shared with a capture pipeline when a test needs one.
type chatsUI struct {
	st    *store.Store
	rules *ingest.RuleSet
	cfg   *config.Config
}

func newChatsUI(t *testing.T) *chatsUI {
	t.Helper()
	return &chatsUI{st: testkit.Store(t), rules: ingest.NewRuleSet(nil), cfg: &config.Config{DataDir: t.TempDir()}}
}

func (u *chatsUI) handler() http.Handler {
	return New(Deps{Store: u.st, Rules: u.rules, Config: u.cfg, Now: func() time.Time { return dashNow }}).Handler()
}

func (u *chatsUI) message(t *testing.T, chat, id string) {
	t.Helper()
	if _, err := u.st.InsertMessage(t.Context(), testkit.Message(chat, id, dashNow.Add(-time.Hour))); err != nil {
		t.Fatal(err)
	}
}

func (u *chatsUI) page(t *testing.T, path string) string {
	t.Helper()
	body, code, _ := get(t, u.handler(), path)
	if code != http.StatusOK {
		t.Fatalf("GET %s: status %d: %s", path, code, body)
	}
	return visibleText(string(body))
}

// postForm sends a same-origin htmx form POST, as the chats page does.
func postForm(t *testing.T, h http.Handler, path string, form url.Values) (string, int) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	r.Host = "localhost:8080"
	r.Header.Set("Origin", "http://localhost:8080")
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	body, err := io.ReadAll(w.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body), w.Code
}

func TestChatListUsesParticipantNameForDirectChats(t *testing.T) {
	u := newChatsUI(t)
	u.message(t, "966500000001@s.whatsapp.net", "a")
	if err := u.st.UpsertParticipant(t.Context(), store.Participant{JID: "966500000001@s.whatsapp.net", DisplayName: "Aisha"}); err != nil {
		t.Fatal(err)
	}
	got := u.page(t, "/chats")
	if !strings.Contains(got, "Aisha") || !strings.Contains(got, "966500000001@s.whatsapp.net") {
		t.Fatalf("chats page lacks the participant name or the secondary JID: %s", got)
	}
	if !strings.Contains(got, "Logging") {
		t.Fatalf("chats page does not say the chat is logging: %s", got)
	}
}

func TestChatListFilter(t *testing.T) {
	u := newChatsUI(t)
	u.message(t, "1@g.us", "a")
	u.message(t, "2@g.us", "b")
	for jid, name := range map[string]string{"1@g.us": "Family", "2@g.us": "Work"} {
		if err := u.st.UpsertChat(t.Context(), store.Chat{JID: jid, Name: name, Type: "group"}); err != nil {
			t.Fatal(err)
		}
	}
	got := u.page(t, "/chats?q=fAm")
	if !strings.Contains(got, "Family") || strings.Contains(got, "Work") {
		t.Fatalf("filter fam: %s", got)
	}
}

// FR-18: one POST turns logging off for ten chats, and the live rules see all ten at once.
func TestBulkRuleAppliesToAllSelected(t *testing.T) {
	u := newChatsUI(t)
	form := url.Values{"logging": {"off"}}
	for i := range 10 {
		jid := fmt.Sprintf("12036300000%d@g.us", i)
		u.message(t, jid, "m")
		form.Add("jid[]", jid)
	}
	body, code := postForm(t, u.handler(), "/chats/rules", form)
	if code != http.StatusOK {
		t.Fatalf("status %d: %s", code, body)
	}
	for _, jid := range form["jid[]"] {
		if !u.rules.Excluded(jid) {
			t.Errorf("%s still logged by the live rules", jid)
		}
	}
	if n := strings.Count(visibleText(body), "Not logging"); n != 10 {
		t.Fatalf("%d updated rows say Not logging, want 10: %s", n, body)
	}
	rules, err := u.st.ChatRules(t.Context())
	if err != nil || len(rules) != 10 {
		t.Fatalf("%d stored rules (err %v), want 10", len(rules), err)
	}
}

// The live rule set is replaced with every rule in the archive, not only the ones this POST changed.
func TestRuleChangeKeepsOtherRules(t *testing.T) {
	u := newChatsUI(t)
	u.message(t, "1@g.us", "a")
	u.message(t, "2@g.us", "b")
	if err := u.st.SetChatRules(t.Context(), []store.ChatRule{{JID: "1@g.us", Logging: false}}); err != nil {
		t.Fatal(err)
	}
	u.rules.Replace([]store.ChatRule{{JID: "1@g.us", Logging: false}})
	if _, code := postForm(t, u.handler(), "/chats/rules", url.Values{"jid[]": {"2@g.us"}, "logging": {"off"}}); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if !u.rules.Excluded("1@g.us") || !u.rules.Excluded("2@g.us") {
		t.Fatal("the live rules lost a rule the POST did not touch")
	}
}

// Media choices: text only downloads nothing, custom keeps the chosen types and cap, default removes the chat's own
// rules. Changing media keeps the logging setting, and changing logging keeps the media rules.
func TestMediaRuleChoices(t *testing.T) {
	u := newChatsUI(t)
	u.message(t, "1@g.us", "a")
	post := func(form url.Values) {
		t.Helper()
		form.Set("jid[]", "1@g.us")
		if body, code := postForm(t, u.handler(), "/chats/rules", form); code != http.StatusOK {
			t.Fatalf("status %d: %s", code, body)
		}
	}
	post(url.Values{"media": {"text-only"}})
	if m := u.rules.Media("1@g.us"); m == nil || len(m.Download) != 0 {
		t.Fatalf("text-only media rules %+v", m)
	}
	post(url.Values{"logging": {"off"}})
	if m := u.rules.Media("1@g.us"); m == nil || !u.rules.Excluded("1@g.us") {
		t.Fatalf("logging change dropped the media rules (%+v) or did not apply", m)
	}
	post(url.Values{"media": {"custom"}, "download": {"image", "audio"}, "video_max_mb": {"25"}})
	m := u.rules.Media("1@g.us")
	if m == nil || strings.Join(m.Download, ",") != "image,audio" || m.VideoMaxMB != 25 || !u.rules.Excluded("1@g.us") {
		t.Fatalf("custom media rules %+v, excluded %v", m, u.rules.Excluded("1@g.us"))
	}
	post(url.Values{"media": {"default"}})
	if m := u.rules.Media("1@g.us"); m != nil {
		t.Fatalf("default left chat media rules %+v", m)
	}
}

func TestRuleFormRejectsBadInput(t *testing.T) {
	u := newChatsUI(t)
	for _, form := range []url.Values{
		{"logging": {"off"}},                                          // no chat
		{"jid[]": {"1@g.us"}, "logging": {"maybe"}},                   // unknown logging value
		{"jid[]": {"1@g.us"}, "media": {"all"}},                       // unknown media value
		{"jid[]": {"1@g.us"}, "media": {"custom"}, "download": {"x"}}, // unknown media type
		{"jid[]": {"1@g.us"}, "media": {"custom"}, "video_max_mb": {"-1"}},
		{"jid[]": {"1@g.us"}}, // nothing to change
	} {
		if _, code := postForm(t, u.handler(), "/chats/rules", form); code != http.StatusBadRequest {
			t.Errorf("%v: status %d, want 400", form, code)
		}
	}
	if rules, _ := u.st.ChatRules(t.Context()); len(rules) != 0 {
		t.Fatalf("a rejected form wrote rules: %+v", rules)
	}
}

type nopQueue struct{}

func (nopQueue) Enqueue(context.Context, store.Media) error  { return nil }
func (nopQueue) OnRetry(context.Context, *events.MediaRetry) {}

type nopHealth struct{}

func (nopHealth) Seen(time.Time, bool)    {}
func (nopHealth) SetState(string, string) {}
func (nopHealth) WriteFailed(error)       {}
func (nopHealth) Panic(string)            {}

func liveText(chat, id, body string) *events.Message {
	c, err := types.ParseJID(chat)
	if err != nil {
		panic(err)
	}
	msg := &waE2E.Message{Conversation: proto.String(body)}
	return &events.Message{Info: types.MessageInfo{MessageSource: types.MessageSource{Chat: c, Sender: types.NewJID("2", types.HiddenUserServer)},
		ID: types.MessageID(id), Timestamp: dashNow}, Message: msg, RawMessage: msg}
}

// SC-2 end to end: logging turned off in the UI stops capture of the chat's next message, with no restart.
func TestRuleChangeStopsCaptureEndToEnd(t *testing.T) {
	u := newChatsUI(t)
	p := ingest.NewPipeline(u.st, ingest.NewFilter(u.rules, func(jid string) ([]string, error) { return u.st.Aliases(context.Background(), jid) }),
		nopQueue{}, nopHealth{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.Handle(liveText("120363000000000009@g.us", "BEFORE", "kept"))
	if _, code := postForm(t, u.handler(), "/chats/rules", url.Values{"jid[]": {"120363000000000009@g.us"}, "logging": {"off"}}); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	p.Handle(liveText("120363000000000009@g.us", "AFTER", "not kept"))
	page, err := u.st.ChatPage(t.Context(), "120363000000000009@g.us", 0, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].ID != "BEFORE" {
		t.Fatalf("stored %+v, want only BEFORE", page)
	}
}

// A chat is flagged New on the list until it is acknowledged; a GET never acknowledges it.
func TestNewChatsFlaggedUntilAcknowledged(t *testing.T) {
	u := newChatsUI(t)
	u.message(t, "1@g.us", "a")
	u.message(t, "2@g.us", "b")
	if _, err := u.st.MarkChatSeen(t.Context(), "1@g.us", dashNow.Unix()); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if got := u.page(t, "/chats"); strings.Count(got, "New") != 1 {
			t.Fatalf("want exactly one chat flagged New: %s", got)
		}
	}
	body, code := postForm(t, u.handler(), "/chats/ack", url.Values{"jid[]": {"1@g.us"}})
	if code != http.StatusOK {
		t.Fatalf("status %d: %s", code, body)
	}
	if got := u.page(t, "/chats"); strings.Contains(got, "New") {
		t.Fatalf("still flagged New after acknowledging: %s", got)
	}
}

// Acknowledging the chat under its canonical (LID) form also clears a sighting under its phone form.
func TestAcknowledgeCoversAliases(t *testing.T) {
	u := newChatsUI(t)
	if _, err := u.st.InsertIDMapping(t.Context(), store.IDMapping{LID: "77@lid", PN: "9665@s.whatsapp.net", Source: "test", FirstSeen: time.Unix(1, 0)}); err != nil {
		t.Fatal(err)
	}
	u.message(t, "9665@s.whatsapp.net", "a")
	if _, err := u.st.MarkChatSeen(t.Context(), "9665@s.whatsapp.net", 1); err != nil {
		t.Fatal(err)
	}
	if _, code := postForm(t, u.handler(), "/chats/ack", url.Values{"jid[]": {"77@lid"}}); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	seen, err := u.st.SeenChats(t.Context())
	if err != nil || len(seen) != 1 || !seen[0].Acknowledged {
		t.Fatalf("seen %+v (err %v): the phone-form sighting is not acknowledged", seen, err)
	}
}

func TestChatsPageLinkedInNav(t *testing.T) {
	u := newChatsUI(t)
	body, _, _ := get(t, u.handler(), "/")
	if !strings.Contains(string(body), `href="/chats"`) {
		t.Fatal("no Chats link in the navigation")
	}
}

// A chat excluded under its phone form (config.yaml's exclude) is turned back on from its row, which uses the LID
// form: the alias's rule changes too, so neither the list nor capture keeps the old exclusion.
func TestStartLoggingClearsRuleOnAlias(t *testing.T) {
	u := newChatsUI(t)
	if _, err := u.st.InsertIDMapping(t.Context(), store.IDMapping{LID: "77@lid", PN: "9665@s.whatsapp.net", Source: "test", FirstSeen: time.Unix(1, 0)}); err != nil {
		t.Fatal(err)
	}
	u.message(t, "77@lid", "a")
	off := []store.ChatRule{{JID: "9665@s.whatsapp.net", Logging: false}}
	if err := u.st.SetChatRules(t.Context(), off); err != nil {
		t.Fatal(err)
	}
	u.rules.Replace(off)
	body, code := postForm(t, u.handler(), "/chats/rules", url.Values{"jid[]": {"77@lid"}, "logging": {"on"}})
	if code != http.StatusOK {
		t.Fatalf("status %d: %s", code, body)
	}
	if u.rules.Excluded("9665@s.whatsapp.net") || u.rules.Excluded("77@lid") {
		t.Fatal("the phone-form rule still excludes the chat")
	}
	if got := visibleText(body); strings.Contains(got, "Not logging") || !strings.Contains(got, "Logging") {
		t.Fatalf("updated row: %s", got)
	}
}

// Without JavaScript the forms still work: a plain POST is answered with a redirect back to the list.
func TestPlainFormPostRedirectsToList(t *testing.T) {
	u := newChatsUI(t)
	u.message(t, "1@g.us", "a")
	r := httptest.NewRequest(http.MethodPost, "/chats/rules", strings.NewReader("jid%5B%5D=1%40g.us&logging=off"))
	r.Host = "localhost:8080"
	r.Header.Set("Origin", "http://localhost:8080")
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	u.handler().ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/chats" || !u.rules.Excluded("1@g.us") {
		t.Fatalf("status %d, Location %q, excluded %v", w.Code, w.Header().Get("Location"), u.rules.Excluded("1@g.us"))
	}
}

// I3: a chat excluded since its first sighting has no messages; it is still listed and can be switched back on.
func TestRuleOnlyChatListedAndSwitchedOn(t *testing.T) {
	u := newChatsUI(t)
	if _, err := u.st.InsertIDMapping(t.Context(), store.IDMapping{LID: "77@lid", PN: "9665@s.whatsapp.net", Source: "test", FirstSeen: time.Unix(1, 0)}); err != nil {
		t.Fatal(err)
	}
	off := []store.ChatRule{{JID: "9665@s.whatsapp.net", Logging: false}, {JID: "77@lid", Logging: false}}
	if err := u.st.SetChatRules(t.Context(), off); err != nil {
		t.Fatal(err)
	}
	u.rules.Replace(off)
	got := u.page(t, "/chats")
	if strings.Count(got, "Not logging") != 1 || strings.Count(got, "77@lid") != 1 || strings.Contains(got, "9665@s.whatsapp.net") {
		t.Fatalf("want the chat listed once, under its canonical JID, as Not logging: %s", got)
	}
	body, code := postForm(t, u.handler(), "/chats/rules", url.Values{"jid[]": {"77@lid"}, "logging": {"on"}})
	if code != http.StatusOK || strings.Contains(visibleText(body), "Not logging") || !strings.Contains(visibleText(body), "Logging") {
		t.Fatalf("status %d, updated row %s", code, visibleText(body))
	}
	if u.rules.Excluded("77@lid") || u.rules.Excluded("9665@s.whatsapp.net") {
		t.Fatal("still excluded after switching on")
	}
}

// I2: concurrent rule changes (two tabs, a double click) never leave the live rules behind the archive.
func TestConcurrentRulePostsKeepLiveRulesEqualToStore(t *testing.T) {
	u := newChatsUI(t)
	h := u.handler()
	const n = 20
	for i := range n {
		u.message(t, fmt.Sprintf("c%02d@g.us", i), "m")
	}
	for round := range 300 {
		var wg sync.WaitGroup
		for i := range n {
			wg.Add(1)
			go func() {
				defer wg.Done()
				logging := "off"
				if (i+round)%2 == 0 {
					logging = "on"
				}
				r := httptest.NewRequest(http.MethodPost, "/chats/rules", strings.NewReader(url.Values{"jid[]": {fmt.Sprintf("c%02d@g.us", i)}, "logging": {logging}}.Encode()))
				r.Host = "localhost:8080"
				r.Header.Set("Origin", "http://localhost:8080")
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				h.ServeHTTP(httptest.NewRecorder(), r)
			}()
		}
		wg.Wait()
		stored, err := u.st.ChatRules(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if len(stored) != n {
			t.Fatalf("round %d: %d stored rules, want %d", round, len(stored), n)
		}
		for _, rule := range stored {
			if u.rules.Excluded(rule.JID) == rule.Logging {
				t.Fatalf("round %d: %s stored logging=%v but the live rules say excluded=%v", round, rule.JID, rule.Logging, u.rules.Excluded(rule.JID))
			}
		}
	}
}
