package web

import (
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"whatsapp-archiver/internal/store"
)

func TestChatsUsesBoundedStorePageAndDerivedEstimate(t *testing.T) {
	listSource, err := os.ReadFile("chats.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(listSource)
	if !strings.Contains(text, ".ChatListPage(") || strings.Contains(text, ".ChatList(ctx") {
		t.Fatalf("chats handler must use the store's LIMIT 51 keyset page query, not load ChatList in Go")
	}
	estimateSource, err := os.ReadFile("../store/chat_controls.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(estimateSource), "FROM media") {
		t.Fatal("chat estimate must use chat_stats.media_bytes without scanning raw media")
	}
}

func TestRulePreviewNamesChosenMediaRule(t *testing.T) {
	u := newChatsUI(t)
	namedChat(t, u, "family@g.us", "Family", "group", "", dashNow)
	body, code := postForm(t, u.handler(), "/chats/rules/preview", url.Values{
		"jid[]": {"family@g.us"}, "media": {"custom"}, "download": {"image", "video"}, "video_max_mb": {"25"},
	})
	if code != http.StatusOK {
		t.Fatalf("preview: %d: %s", code, body)
	}
	text := visibleText(body)
	for _, want := range []string{"Custom media", "image", "video", "25 MB"} {
		if !strings.Contains(text, want) {
			t.Errorf("preview does not state %q in chosen rule: %s", want, text)
		}
	}
}

func TestFilteredSnapshotDetectsSameCountMembershipChange(t *testing.T) {
	u := newChatsUI(t)
	namedChat(t, u, "one@s.whatsapp.net", "One", "direct", "", dashNow)
	namedChat(t, u, "two@s.whatsapp.net", "Two", "group", "", dashNow.Add(-1))
	page := rawChatsPage(t, u, "/chats?type=direct")
	snapshot := regexp.MustCompile(`name="snapshot" value="([^"]+)"`).FindStringSubmatch(page)
	if len(snapshot) != 2 {
		t.Fatalf("snapshot missing: %s", page)
	}
	if err := u.st.UpsertChat(t.Context(), store.Chat{JID: "one@s.whatsapp.net", Name: "One", Type: "group"}); err != nil {
		t.Fatal(err)
	}
	if err := u.st.UpsertChat(t.Context(), store.Chat{JID: "two@s.whatsapp.net", Name: "Two", Type: "direct"}); err != nil {
		t.Fatal(err)
	}
	body, code := postForm(t, u.handler(), "/chats/rules/preview", url.Values{
		"scope": {"filtered"}, "snapshot": {snapshot[1]}, "type": {"direct"}, "logging": {"off"},
	})
	if code != http.StatusConflict || !strings.Contains(visibleText(body), "filtered set changed") {
		t.Fatalf("same-count membership swap = %d, %s; want stale-set review", code, visibleText(body))
	}
}
