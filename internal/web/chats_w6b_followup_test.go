package web

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/store"
)

func TestFutureCommunityChildInheritsOnTheNextChatsRead(t *testing.T) {
	u := newChatsUI(t)
	namedChat(t, u, "neighbours@g.us", "Neighbours", "community", "", dashNow)
	if _, code := postForm(t, u.handler(), "/chats/rules", url.Values{"jid[]": {"neighbours@g.us"}, "logging": {"off"}}); code != http.StatusOK {
		t.Fatalf("community rule status %d", code)
	}
	namedChat(t, u, "future@g.us", "Future child", "group", "neighbours@g.us", dashNow.Add(time.Minute))
	if u.rules.Excluded("future@g.us") {
		t.Fatal("test precondition: a child unknown at rule time was already in the live rule set")
	}
	page := visibleText(rawChatsPage(t, u, "/chats"))
	if !u.rules.Excluded("future@g.us") || !strings.Contains(page, "Inherited from Neighbours") || !strings.Contains(page, "Not logging") {
		t.Fatalf("future child did not inherit on discovery/read: excluded=%v page=%s", u.rules.Excluded("future@g.us"), page)
	}
}

func TestFilteredPreviewKeepsTheSelectionServerOwned(t *testing.T) {
	u := newChatsUI(t)
	for i := range 3 {
		namedChat(t, u, fmt.Sprintf("team-%d@g.us", i), fmt.Sprintf("Team %d", i), "group", "", dashNow.Add(time.Duration(i)*time.Minute))
	}
	page := rawChatsPage(t, u, "/chats?q=Team&type=group")
	snapshot := regexp.MustCompile(`name="snapshot" value="([^"]+)"`).FindStringSubmatch(page)[1]
	form := url.Values{"scope": {"filtered"}, "snapshot": {snapshot}, "q": {"Team"}, "type": {"group"}, "logging": {"off"}}
	preview, code := postForm(t, u.handler(), "/chats/rules/preview", form)
	if code != http.StatusOK {
		t.Fatalf("preview: %d: %s", code, preview)
	}
	text := visibleText(preview)
	if !strings.Contains(text, "3 chats") || !strings.Contains(text, "Projected per week") || strings.Contains(preview, `name="jid[]"`) {
		t.Fatalf("filtered preview must carry only its predicate and snapshot, not a browser JID list: %s", preview)
	}
	if body, status := postForm(t, u.handler(), "/chats/rules", form); status != http.StatusOK {
		t.Fatalf("apply previewed predicate: %d: %s", status, body)
	}
}

func TestChangedFilteredSetReturnsANewCountAndEstimate(t *testing.T) {
	u := newChatsUI(t)
	for i := range 3 {
		namedChat(t, u, fmt.Sprintf("team-%d@g.us", i), fmt.Sprintf("Team %d", i), "group", "", dashNow.Add(time.Duration(i)*time.Minute))
	}
	page := rawChatsPage(t, u, "/chats?q=Team&type=group")
	snapshot := regexp.MustCompile(`name="snapshot" value="([^"]+)"`).FindStringSubmatch(page)[1]
	namedChat(t, u, "team-new@g.us", "Team new", "group", "", dashNow.Add(time.Hour))
	body, code := postForm(t, u.handler(), "/chats/rules", url.Values{"scope": {"filtered"}, "snapshot": {snapshot}, "q": {"Team"}, "type": {"group"}, "logging": {"off"}})
	text := visibleText(body)
	if code != http.StatusConflict || !strings.Contains(text, "4 chats") || !strings.Contains(text, "Projected per week") {
		t.Fatalf("changed set response = %d, %s", code, text)
	}
	if u.rules.Excluded("team-new@g.us") {
		t.Fatal("changed set was applied without confirmation")
	}
}

func TestRowSwapEstimateUnavailableAndPageSelectionContracts(t *testing.T) {
	u := newChatsUI(t)
	namedChat(t, u, "one@g.us", "One", "group", "", dashNow)
	page := rawChatsPage(t, u, "/chats")
	for _, want := range []string{`id="select-page"`, "Select page", "Select all 1 filtered", "1 chats shown"} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	preview, code := postForm(t, u.handler(), "/chats/rules/preview", url.Values{"jid[]": {"one@g.us"}, "logging": {"on"}})
	if code != http.StatusOK || strings.Count(preview, "Estimate unavailable") < 2 {
		t.Fatalf("one-message estimate = %d, %s", code, visibleText(preview))
	}
	row, code := postForm(t, u.handler(), "/chats/rules", url.Values{"jid[]": {"one@g.us"}, "logging": {"off"}})
	if code != http.StatusOK || strings.Count(row, `<tr id="chat-`) != 1 || strings.Contains(row, "<!doctype") || strings.Contains(row, "<table") {
		t.Fatalf("row action did not return exactly one row: %d, %s", code, row)
	}
}

func TestAllFilteredMarkSeenUsesTheServerSnapshot(t *testing.T) {
	u := newChatsUI(t)
	for i := range 3 {
		jid := fmt.Sprintf("team-%d@g.us", i)
		namedChat(t, u, jid, fmt.Sprintf("Team %d", i), "group", "", dashNow.Add(time.Duration(i)*time.Minute))
		if _, err := u.st.MarkChatSeen(t.Context(), jid, dashNow.Unix()); err != nil {
			t.Fatal(err)
		}
	}
	page := rawChatsPage(t, u, "/chats?q=Team&new=1")
	snapshot := regexp.MustCompile(`name="snapshot" value="([^"]+)"`).FindStringSubmatch(page)
	if len(snapshot) != 2 {
		t.Fatalf("all-filtered snapshot missing: %s", page)
	}
	body, code := postForm(t, u.handler(), "/chats/ack", url.Values{
		"scope": {"filtered"}, "snapshot": {snapshot[1]}, "q": {"Team"}, "new": {"1"},
	})
	if code != http.StatusOK || strings.Count(body, `hx-swap-oob="true"`) != 3 {
		t.Fatalf("all-filtered mark seen = %d, %s", code, body)
	}
	if page = rawChatsPage(t, u, "/chats?q=Team&new=1"); !strings.Contains(page, "0 chats shown") {
		t.Fatalf("marked chats still match New: %s", visibleText(page))
	}
}

func TestInheritedCommunityNamesDoNotDependOnTheCurrentPage(t *testing.T) {
	u := newChatsUI(t)
	namedChat(t, u, "neighbours@g.us", "Neighbours", "community", "", dashNow.Add(-2*time.Hour))
	namedChat(t, u, "announcements@g.us", "Announcements", "group", "neighbours@g.us", dashNow.Add(2*time.Hour))
	for i := range 55 {
		namedChat(t, u, fmt.Sprintf("filler-%02d@g.us", i), fmt.Sprintf("Filler %02d", i), "group", "", dashNow.Add(time.Duration(i)*time.Minute))
	}
	if err := u.st.SetChatRules(t.Context(), []store.ChatRule{{JID: "neighbours@g.us", Logging: false}}); err != nil {
		t.Fatal(err)
	}
	page := rawChatsPage(t, u, "/chats")
	start := strings.Index(page, `href="/chats/announcements@g.us"`)
	if start < 0 {
		t.Fatalf("first page lacks child chat: %s", visibleText(page))
	}
	end := strings.Index(page[start:], "</tr>")
	row := page[start : start+end]
	if !strings.Contains(row, "Inherited from Neighbours") {
		t.Fatalf("off-page community name missing from child rule label: %s", visibleText(row))
	}
}

// Set W6B_DEVSEED_DATA to a disposable archive created by:
//
//	archiver devseed --data "$W6B_DEVSEED_DATA" --chats 250 --messages 300000
//
// The harness follows ui.md: release/no-race process, warm cache, 5 warm-ups and 30 measured local requests.
func TestChatsW6BAtScale(t *testing.T) {
	data := os.Getenv("W6B_DEVSEED_DATA")
	if data == "" {
		t.Skip("set W6B_DEVSEED_DATA to the disposable 300k/250 devseed archive")
	}
	st, err := store.Open(filepath.Join(data, "archive.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	rules, err := st.ChatRules(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	h := New(Deps{Store: st, Rules: ingest.NewRuleSet(rules), Config: &config.Config{DataDir: data}, Now: func() time.Time { return dashNow }}).Handler()

	timed := func(name string, limit time.Duration, request func() int) {
		t.Helper()
		var samples []time.Duration
		for i := range 35 {
			begin := time.Now()
			status := request()
			took := time.Since(begin)
			if status != http.StatusOK {
				t.Fatalf("%s request %d: status %d", name, i, status)
			}
			if i >= 5 {
				samples = append(samples, took)
			}
		}
		slices.Sort(samples)
		p50, p95 := samples[14], samples[28]
		t.Logf("%s: p50=%v p95=%v max=%v (slowest measured operation; query timing is included in server time)", name, p50, p95, samples[29])
		if p95 >= limit {
			t.Fatalf("%s p95 %v, want under %v", name, p95, limit)
		}
	}
	getStatus := func(path string) func() int {
		return func() int { _, code, _ := get(t, h, path); return code }
	}
	timed("chats first page", 150*time.Millisecond, getStatus("/chats"))
	timed("chats filtered page", 150*time.Millisecond, getStatus("/chats?q=Synthetic&type=direct&gaps=1"))

	body, _, _ := get(t, h, "/chats")
	jid := regexp.MustCompile(`name="jid\[\]" value="([^"]+)"`).FindStringSubmatch(string(body))[1]
	toggle := false
	timed("one row rule swap", 50*time.Millisecond, func() int {
		toggle = !toggle
		value := "off"
		if toggle {
			value = "on"
		}
		_, code := postForm(t, h, "/chats/rules", url.Values{"jid[]": {jid}, "logging": {value}})
		return code
	})

	body, _, _ = get(t, h, "/chats?type=direct")
	snapshot := regexp.MustCompile(`name="snapshot" value="([^"]+)"`).FindStringSubmatch(string(body))[1]
	timed("bulk preview", 150*time.Millisecond, func() int {
		_, code := postForm(t, h, "/chats/rules/preview", url.Values{"scope": {"filtered"}, "snapshot": {snapshot}, "type": {"direct"}, "logging": {"off"}})
		return code
	})
}
