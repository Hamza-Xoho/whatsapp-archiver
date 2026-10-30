package web

import (
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
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
