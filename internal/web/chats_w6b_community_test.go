package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"whatsapp-archiver/internal/ingest"
)

func TestCommunityExpansionConfirmationAndPerRowMediaControls(t *testing.T) {
	u := newChatsUI(t)
	namedChat(t, u, "neighbours@g.us", "Neighbours", "community", "", dashNow)
	namedChat(t, u, "announcements@g.us", "Announcements", "group", "neighbours@g.us", dashNow.Add(-time.Minute))
	namedChat(t, u, "chat@g.us", "Chat", "group", "neighbours@g.us", dashNow.Add(-2*time.Minute))

	page := rawChatsPage(t, u, "/chats")
	communityStart := strings.Index(page, `href="/chats/neighbours@g.us"`)
	if communityStart < 0 {
		t.Fatal("community row missing")
	}
	communityEnd := strings.Index(page[communityStart:], "</tr>")
	community := page[communityStart : communityStart+communityEnd]
	for _, want := range []string{
		`href="/chats/announcements@g.us"`, `href="/chats/chat@g.us"`,
		`name="download" value="image"`, `name="download" value="audio"`, `name="video_max_mb"`,
		"Stop logging Neighbours?", "2 child chats",
	} {
		if !strings.Contains(community, want) {
			t.Errorf("community row lacks %q: %s", want, community)
		}
	}

	preview, code := postForm(t, u.handler(), "/chats/rules/preview", url.Values{"jid[]": {"neighbours@g.us"}, "logging": {"off"}})
	if code != http.StatusOK {
		t.Fatalf("preview: %d: %s", code, preview)
	}
	text := visibleText(preview)
	if !strings.Contains(text, "3 chats") || !strings.Contains(text, "2 child chats affected") {
		t.Fatalf("community preview does not name the complete scope: %s", text)
	}
}

func TestCommunityInheritanceSurvivesRestartAndEqualChildOverride(t *testing.T) {
	u := newChatsUI(t)
	namedChat(t, u, "neighbours@g.us", "Neighbours", "community", "", dashNow)
	namedChat(t, u, "child@g.us", "Child", "group", "neighbours@g.us", dashNow.Add(-time.Minute))
	if _, code := postForm(t, u.handler(), "/chats/rules", url.Values{"jid[]": {"neighbours@g.us"}, "logging": {"off"}}); code != http.StatusOK {
		t.Fatalf("community off: %d", code)
	}
	// This is an explicit child override even though its value currently equals the inherited value.
	if _, code := postForm(t, u.handler(), "/chats/rules", url.Values{"jid[]": {"child@g.us"}, "logging": {"off"}}); code != http.StatusOK {
		t.Fatalf("child override: %d", code)
	}
	if _, code := postForm(t, u.handler(), "/chats/rules", url.Values{"jid[]": {"neighbours@g.us"}, "logging": {"on"}}); code != http.StatusOK {
		t.Fatalf("community on: %d", code)
	}
	if !u.rules.Excluded("child@g.us") {
		t.Fatal("an explicit child override equal to the old inherited value was overwritten")
	}

	// A child discovered after the community action is synthesized by the persisted rule reader, which is the same
	// path cmd/archiver uses to build a fresh RuleSet on restart. No UI read may be required.
	namedChat(t, u, "future@g.us", "Future", "group", "neighbours@g.us", dashNow.Add(time.Minute))
	rules, err := u.st.ChatRules(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	restarted := ingest.NewRuleSet(rules)
	if restarted.Excluded("future@g.us") {
		t.Fatal("future child inherited the community's current on rule incorrectly")
	}
	// Stop the parent again without visiting GET /chats; a fresh process must now exclude the known future child.
	if _, code := postForm(t, u.handler(), "/chats/rules", url.Values{"jid[]": {"neighbours@g.us"}, "logging": {"off"}}); code != http.StatusOK {
		t.Fatalf("community off again: %d", code)
	}
	rules, err = u.st.ChatRules(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !ingest.NewRuleSet(rules).Excluded("future@g.us") {
		t.Fatal("restart ignored inherited community rule for a child with no explicit override")
	}
}
