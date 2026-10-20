package media

import (
	"database/sql"
	"errors"
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
	perChat := func(chat string) (*config.MediaRules, error) {
		if chat == "quiet@g.us" {
			return textOnly, nil
		}
		return nil, nil
	}
	q := NewQueue(t.TempDir(), func() (config.MediaRules, error) { return global, nil }, perChat, st, &fakeDown{}, fakeClock{time.Unix(1, 0)}, quietLog(), nil)
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

// "An error, never an answer": when a rule cannot be read, Enqueue makes no download decision and returns the error,
// so Pipeline.persist retries it and alerts. Guessing the global rules would download media of a chat set to
// download nothing, seen under its other JID form.
func TestEnqueueRuleReadErrorMakesNoDecision(t *testing.T) {
	boom := errors.New("database is locked")
	image := config.MediaRules{Download: []string{"image"}}
	cases := map[string]struct {
		global  func() (config.MediaRules, error)
		perChat func(string) (*config.MediaRules, error)
	}{
		"chat rule": {func() (config.MediaRules, error) { return image, nil }, func(string) (*config.MediaRules, error) { return nil, boom }},
		"global rules": {func() (config.MediaRules, error) { return config.MediaRules{}, boom }, func(string) (*config.MediaRules, error) { return nil, nil }},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			st := testkit.Store(t)
			q := NewQueue(t.TempDir(), c.global, c.perChat, st, &fakeDown{}, fakeClock{time.Unix(1, 0)}, quietLog(), nil)
			err := q.Enqueue(t.Context(), store.Media{ChatJID: "1234@lid", MessageID: "M", Type: "image", Source: "live"})
			if !errors.Is(err, boom) {
				t.Fatalf("Enqueue err = %v, want the read error", err)
			}
			if _, err = st.MediaByID(t.Context(), "1234@lid", "M"); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("a media row was written without a rule: err=%v", err)
			}
		})
	}
}
