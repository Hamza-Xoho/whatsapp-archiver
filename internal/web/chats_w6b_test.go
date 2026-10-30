package web

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

func namedChat(t *testing.T, u *chatsUI, jid, name, typ, parent string, at time.Time) {
	t.Helper()
	m := testkit.Message(jid, "m-"+jid, at)
	if _, err := u.st.InsertMessage(t.Context(), m); err != nil {
		t.Fatal(err)
	}
	if err := u.st.UpsertChat(t.Context(), store.Chat{JID: jid, Name: name, Type: typ, ParentJID: parent}); err != nil {
		t.Fatal(err)
	}
}

func rawChatsPage(t *testing.T, u *chatsUI, path string) string {
	t.Helper()
	body, code, _ := get(t, u.handler(), path)
	if code != http.StatusOK {
		t.Fatalf("GET %s: %d: %s", path, code, body)
	}
	return string(body)
}

func TestChatsListUsesStableFiftyRowKeysetPages(t *testing.T) {
	u := newChatsUI(t)
	for i := range 55 {
		namedChat(t, u, fmt.Sprintf("%02d@g.us", i), fmt.Sprintf("Chat %02d", i), "group", "", dashNow.Add(time.Duration(i)*time.Minute))
	}
	first := rawChatsPage(t, u, "/chats")
	if got := strings.Count(first, `<tr id="chat-`); got != 50 {
		t.Fatalf("first page has %d rows, want 50", got)
	}
	if !strings.Contains(first, "55 chats shown") || strings.Contains(first, "Previous") {
		t.Fatalf("first page lacks count or has a previous link: %s", visibleText(first))
	}
	next := regexp.MustCompile(`href="(/chats\?cursor=[^"]+)"[^>]*>Next`).FindStringSubmatch(first)
	if len(next) != 2 {
		t.Fatalf("first page lacks a cursor Next link: %s", first)
	}
	second := rawChatsPage(t, u, strings.ReplaceAll(next[1], "&amp;", "&"))
	if got := strings.Count(second, `<tr id="chat-`); got != 5 {
		t.Fatalf("second page has %d rows, want 5", got)
	}
	if !strings.Contains(second, ">Previous</a>") || strings.Contains(second, ">Next</a>") {
		t.Fatalf("second page has wrong paging links: %s", second)
	}
	for i := 50; i < 55; i++ { // newest five are page one, oldest five are page two
		if !strings.Contains(first, fmt.Sprintf("Chat %02d", i)) || strings.Contains(second, fmt.Sprintf("Chat %02d", i)) {
			t.Fatalf("chat %02d crossed the stable page boundary", i)
		}
	}
}

func TestChatsListFiltersCombineWithANDAndStayInURL(t *testing.T) {
	u := newChatsUI(t)
	namedChat(t, u, "direct@s.whatsapp.net", "Aisha", "direct", "", dashNow)
	namedChat(t, u, "family@g.us", "Family", "group", "", dashNow.Add(-time.Minute))
	namedChat(t, u, "work@g.us", "Work", "group", "", dashNow.Add(-2*time.Minute))
	namedChat(t, u, "community@g.us", "Neighbourhood", "community", "", dashNow.Add(-3*time.Minute))
	namedChat(t, u, "quiet@g.us", "Quiet", "group", "", dashNow.Add(-4*time.Minute))
	if err := u.st.SetChatRules(t.Context(), []store.ChatRule{{JID: "work@g.us", Logging: false}}); err != nil {
		t.Fatal(err)
	}
	if _, err := u.st.MarkChatSeen(t.Context(), "family@g.us", dashNow.Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := u.st.InsertEvent(t.Context(), store.MessageEvent{ChatJID: "family@g.us", TargetID: "missing", Kind: "undecryptable", EventID: "gap", Cause: "other", Source: "live", Timestamp: dashNow.Unix(), ReceivedAt: dashNow.Unix()}); err != nil {
		t.Fatal(err)
	}
	u.mediaRow(t, "family@g.us", "m-family@g.us", "failed", "image/jpeg", "")

	for _, tc := range []struct {
		query string
		want  string
		not   []string
	}{
		{"type=direct", "Aisha", []string{"Family", "Neighbourhood"}},
		{"type=community", "Neighbourhood", []string{"Family", "Aisha"}},
		{"logging=off", "Work", []string{"Family", "Quiet"}},
		{"new=1", "Family", []string{"Work", "Quiet"}},
		{"gaps=1", "Family", []string{"Work", "Quiet"}},
		{"failed=1", "Family", []string{"Work", "Quiet"}},
		{"q=fam&type=group&logging=on&new=1&gaps=1&failed=1", "Family", []string{"Work", "Quiet", "Aisha"}},
	} {
		body := rawChatsPage(t, u, "/chats?"+tc.query)
		text := visibleText(body)
		if !strings.Contains(text, tc.want) {
			t.Errorf("%s lacks %q: %s", tc.query, tc.want, text)
		}
		for _, unwanted := range tc.not {
			if strings.Contains(text, unwanted) {
				t.Errorf("%s unexpectedly contains %q: %s", tc.query, unwanted, text)
			}
		}
		for _, pair := range strings.Split(tc.query, "&") {
			bits := strings.SplitN(pair, "=", 2)
			if !strings.Contains(body, `name="`+bits[0]+`"`) {
				t.Errorf("%s is not represented by a labelled GET control", bits[0])
			}
		}
	}
}

func TestChatsListWarningsAndEmptyStates(t *testing.T) {
	u := newChatsUI(t)
	empty := visibleText(rawChatsPage(t, u, "/chats"))
	if !strings.Contains(empty, "No chats yet. Pair WhatsApp to begin") || !strings.Contains(empty, "Open Pairing") {
		t.Fatalf("empty archive copy: %s", empty)
	}
	namedChat(t, u, "family@g.us", "Family", "group", "", dashNow)
	for i := range 2 {
		if _, err := u.st.InsertEvent(t.Context(), store.MessageEvent{ChatJID: "family@g.us", TargetID: fmt.Sprintf("missing-%d", i), Kind: "undecryptable", EventID: fmt.Sprintf("gap-%d", i), Cause: "other", Source: "live"}); err != nil {
			t.Fatal(err)
		}
	}
	u.mediaRow(t, "family@g.us", "m-family@g.us", "failed", "image/jpeg", "")
	row := visibleText(rawChatsPage(t, u, "/chats"))
	for _, want := range []string{"2 gaps", "1 failed", "Default"} {
		if !strings.Contains(row, want) {
			t.Errorf("row lacks %q: %s", want, row)
		}
	}
	filtered := visibleText(rawChatsPage(t, u, "/chats?q=does-not-exist"))
	if !strings.Contains(filtered, "No chats match these filters") || !strings.Contains(filtered, "Clear filters") {
		t.Fatalf("filtered empty copy: %s", filtered)
	}
}

func TestStartingLoggingAndBulkChangesPreviewAnEstimate(t *testing.T) {
	u := newChatsUI(t)
	namedChat(t, u, "family@g.us", "Family", "group", "", dashNow)
	if err := u.st.SetChatRules(t.Context(), []store.ChatRule{{JID: "family@g.us", Logging: false}}); err != nil {
		t.Fatal(err)
	}
	page := rawChatsPage(t, u, "/chats")
	if !strings.Contains(page, ">Keep logging</button>") || !strings.Contains(page, `action="/chats/rules/preview"`) {
		t.Fatalf("off row does not start with an estimate preview: %s", page)
	}
	body, code := postForm(t, u.handler(), "/chats/rules/preview", url.Values{"jid[]": {"family@g.us"}, "logging": {"on"}})
	if code != http.StatusOK {
		t.Fatalf("preview status %d: %s", code, body)
	}
	text := visibleText(body)
	for _, want := range []string{"Keep logging Family?", "Projected per week", "Text", "Media", "Estimate based on", "Keep logging"} {
		if !strings.Contains(text, want) {
			t.Errorf("preview lacks %q: %s", want, text)
		}
	}
	if u.rules.Excluded("family@g.us") == false {
		t.Fatal("preview changed the live rule")
	}
}

func TestAllFilteredBulkActionUsesSnapshotNotBrowserJIDList(t *testing.T) {
	u := newChatsUI(t)
	for i := range 3 {
		namedChat(t, u, fmt.Sprintf("team-%d@g.us", i), fmt.Sprintf("Team %d", i), "group", "", dashNow.Add(time.Duration(i)*time.Minute))
	}
	page := rawChatsPage(t, u, "/chats?q=Team&type=group")
	match := regexp.MustCompile(`name="snapshot" value="([^"]+)"`).FindStringSubmatch(page)
	if len(match) != 2 || !strings.Contains(page, "Select all 3 filtered") {
		t.Fatalf("all-filtered controls lack a server snapshot: %s", page)
	}
	form := url.Values{"scope": {"filtered"}, "snapshot": {match[1]}, "q": {"Team"}, "type": {"group"}, "logging": {"off"}}
	body, code := postForm(t, u.handler(), "/chats/rules", form)
	if code != http.StatusOK {
		t.Fatalf("all-filtered apply: %d: %s", code, body)
	}
	for i := range 3 {
		if !u.rules.Excluded(fmt.Sprintf("team-%d@g.us", i)) {
			t.Errorf("team %d was not changed", i)
		}
	}

	page = rawChatsPage(t, u, "/chats?q=Team&type=group")
	match = regexp.MustCompile(`name="snapshot" value="([^"]+)"`).FindStringSubmatch(page)
	namedChat(t, u, "team-new@g.us", "Team new", "group", "", dashNow.Add(time.Hour))
	form.Set("snapshot", match[1])
	body, code = postForm(t, u.handler(), "/chats/rules", form)
	if code != http.StatusConflict || !strings.Contains(visibleText(body), "filtered set changed") || u.rules.Excluded("team-new@g.us") {
		t.Fatalf("stale snapshot = %d, %s; new chat excluded=%v", code, visibleText(body), u.rules.Excluded("team-new@g.us"))
	}
}

func TestCommunityRuleInheritanceAndExplicitChildOverride(t *testing.T) {
	u := newChatsUI(t)
	namedChat(t, u, "neighbours@g.us", "Neighbours", "community", "", dashNow)
	namedChat(t, u, "announcements@g.us", "Announcements", "group", "neighbours@g.us", dashNow.Add(-time.Minute))
	namedChat(t, u, "chat@g.us", "Chat", "group", "neighbours@g.us", dashNow.Add(-2*time.Minute))

	if body, code := postForm(t, u.handler(), "/chats/rules", url.Values{"jid[]": {"neighbours@g.us"}, "logging": {"off"}}); code != http.StatusOK {
		t.Fatalf("community off: %d: %s", code, body)
	}
	for _, jid := range []string{"neighbours@g.us", "announcements@g.us", "chat@g.us"} {
		if !u.rules.Excluded(jid) {
			t.Errorf("%s did not inherit community stop", jid)
		}
	}
	page := visibleText(rawChatsPage(t, u, "/chats"))
	if !strings.Contains(page, "2 child chats") || strings.Count(page, "Inherited from Neighbours") != 2 {
		t.Fatalf("community disclosure/inheritance labels: %s", page)
	}
	if body, code := postForm(t, u.handler(), "/chats/rules", url.Values{"jid[]": {"chat@g.us"}, "logging": {"on"}}); code != http.StatusOK {
		t.Fatalf("child override: %d: %s", code, body)
	}
	page = visibleText(rawChatsPage(t, u, "/chats"))
	if !strings.Contains(page, "Own rule") || !strings.Contains(page, "Use community rule") || u.rules.Excluded("chat@g.us") {
		t.Fatalf("explicit child override was not preserved: %s", page)
	}
	if body, code := postForm(t, u.handler(), "/chats/rules/inherit", url.Values{"jid[]": {"chat@g.us"}}); code != http.StatusOK {
		t.Fatalf("restore inheritance: %d: %s", code, body)
	}
	if !u.rules.Excluded("chat@g.us") {
		t.Fatal("restored child did not inherit the stopped community")
	}
}
