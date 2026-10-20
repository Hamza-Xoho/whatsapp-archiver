package media

import (
	"testing"
	"time"

	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

func TestDecideSkipsStickerAndLargeVideo(t *testing.T) {
	r := config.MediaRules{Download: []string{"video"}, Skip: []string{"sticker", "gif"}, VideoMaxMB: 100}
	if a, _ := decide(store.Media{Type: "sticker"}, r); a != "skipped" {
		t.Fatal(a)
	}
	if a, reason := decide(store.Media{Type: "video", DeclaredSize: 150 << 20}, r); a != "skipped" || reason != "over 100 MB cap" {
		t.Fatalf("%s %s", a, reason)
	}
}
func TestDecideDownloadsDocument(t *testing.T) {
	r := config.MediaRules{Download: []string{"document"}}
	if a, _ := decide(store.Media{Type: "document"}, r); a != "pending" {
		t.Fatal(a)
	}
}

// A chat's own media rules replace the global ones: a chat set to download nothing skips an image the global rules
// download, while other chats keep the global rules. Both are read at each Enqueue, so a change applies to the next one.
func TestPerChatMediaRuleOverridesGlobal(t *testing.T) {
	st := testkit.Store(t)
	global := config.MediaRules{Download: []string{"image"}, Workers: 1}
	var textOnly *config.MediaRules
	perChat := func(chat string) *config.MediaRules {
		if chat == "quiet@g.us" {
			return textOnly
		}
		return nil
	}
	q := NewQueue(t.TempDir(), func() config.MediaRules { return global }, perChat, st, &fakeDown{}, fakeClock{time.Unix(1, 0)}, quietLog(), nil)
	enqueue(t, q, store.Media{ChatJID: "quiet@g.us", MessageID: "M0", Type: "image", Source: "live"})
	textOnly = &config.MediaRules{Download: []string{}}
	enqueue(t, q, store.Media{ChatJID: "quiet@g.us", MessageID: "M1", Type: "image", Source: "live"})
	enqueue(t, q, store.Media{ChatJID: "other@g.us", MessageID: "M2", Type: "image", Source: "live"})
	if m := row(t, st, "quiet@g.us", "M0"); m.State != "pending" {
		t.Fatalf("before the chat rule: state %q, want pending", m.State)
	}
	if m := row(t, st, "quiet@g.us", "M1"); m.State != "skipped" || m.Reason != "type not enabled" {
		t.Fatalf("chat with its own rule: state %q reason %q, want skipped", m.State, m.Reason)
	}
	if m := row(t, st, "other@g.us", "M2"); m.State != "pending" {
		t.Fatalf("chat on the global rules: state %q, want pending", m.State)
	}
	global = config.MediaRules{Download: []string{}}
	enqueue(t, q, store.Media{ChatJID: "other@g.us", MessageID: "M3", Type: "image", Source: "live"})
	if m := row(t, st, "other@g.us", "M3"); m.State != "skipped" {
		t.Fatalf("after a global change: state %q, want skipped", m.State)
	}
}
