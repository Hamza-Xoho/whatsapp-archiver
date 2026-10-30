package ingest_test

import (
	"context"
	"io"
	"log/slog"
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

type communityWriter struct{ writes int }

func (w *communityWriter) Apply(context.Context, store.Batch) (store.Applied, error) {
	w.writes++
	return store.Applied{New: true}, nil
}
func (*communityWriter) InsertIDMapping(context.Context, store.IDMapping) (bool, error) {
	return true, nil
}

type communityQueue struct{}

func (communityQueue) Decide(context.Context, *store.Media) error  { return nil }
func (communityQueue) Wake()                                       {}
func (communityQueue) OnRetry(context.Context, *events.MediaRetry) {}

type ruleCheckingQueue struct {
	rules *ingest.RuleSet
	got   *config.MediaRules
}

func (q *ruleCheckingQueue) Decide(_ context.Context, media *store.Media) error {
	q.got = q.rules.Media(media.ChatJID)
	return nil
}
func (*ruleCheckingQueue) Wake()                                       {}
func (*ruleCheckingQueue) OnRetry(context.Context, *events.MediaRetry) {}

type communityHealth struct{}

func (communityHealth) Seen(time.Time, bool)    {}
func (communityHealth) SetState(string, string) {}
func (communityHealth) WriteFailed(error)       {}
func (communityHealth) Panic(string)            {}

func TestFutureCommunityChildIsExcludedBeforeItsFirstWrite(t *testing.T) {
	rules := ingest.NewRuleSet([]store.ChatRule{{JID: "community@g.us", Logging: false}})
	w := &communityWriter{}
	p := ingest.NewPipeline(w, ingest.NewFilter(rules, nil), communityQueue{}, communityHealth{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r := ingest.Result{Kind: ingest.KindMessage, Chat: store.Chat{JID: "future@g.us", Type: "group", ParentJID: "community@g.us"},
		Message: &store.Message{ChatJID: "future@g.us", ID: "first", Kind: "text", Source: "live", RawProto: []byte("x")}, Received: time.Unix(1, 0)}
	if err := p.Ingest(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	if w.writes != 0 {
		t.Fatalf("future child wrote %d batches before inheriting the stopped community; want 0", w.writes)
	}
}

func TestJoinedGroupCarriesCommunityParentBeforeFirstMessage(t *testing.T) {
	rules := ingest.NewRuleSet([]store.ChatRule{{JID: "community@g.us", Logging: false}})
	w := &communityWriter{}
	p := ingest.NewPipeline(w, ingest.NewFilter(rules, nil), communityQueue{}, communityHealth{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.Handle(&events.JoinedGroup{GroupInfo: types.GroupInfo{JID: jid("child@g.us"),
		GroupLinkedParent: types.GroupLinkedParent{LinkedParentJID: jid("community@g.us")}}})
	p.Handle(messageEvent("child@g.us", "first", &waE2E.Message{Conversation: proto.String("hello")}))
	if w.writes != 0 {
		t.Fatalf("real JoinedGroup/message path wrote %d child batches before applying the community rule; want 0", w.writes)
	}
}

func TestCommunityDiscoveryPreservesExplicitChildRule(t *testing.T) {
	parentMedia := &config.MediaRules{Download: []string{}}
	childMedia := &config.MediaRules{Download: []string{"image"}}
	rules := ingest.NewRuleSet([]store.ChatRule{
		{JID: "community@g.us", Logging: false, Media: parentMedia},
		{JID: "child@g.us", Logging: true, Media: childMedia},
	})
	w := &communityWriter{}
	q := &ruleCheckingQueue{rules: rules}
	p := ingest.NewPipeline(w, ingest.NewFilter(rules, nil), q, communityHealth{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r := ingest.Result{Kind: ingest.KindMessage, Chat: store.Chat{JID: "child@g.us", ParentJID: "community@g.us"},
		Message: &store.Message{ChatJID: "child@g.us", ID: "first", Kind: "image", Source: "live", RawProto: []byte("x")},
		Media:   &store.Media{ChatJID: "child@g.us", MessageID: "first", Type: "image"}, Received: time.Unix(1, 0)}
	if err := p.Ingest(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	if w.writes != 1 {
		t.Fatalf("explicitly enabled child wrote %d batches; want 1", w.writes)
	}
	if q.got == nil || len(q.got.Download) != 1 || q.got.Download[0] != "image" {
		t.Fatalf("media rule after discovery = %+v; want child's own image rule", q.got)
	}
}

func TestExcludedJoinedGroupPersistsParentAcrossRestart(t *testing.T) {
	st := testkit.Store(t)
	if err := st.SetChatRules(t.Context(), []store.ChatRule{{JID: "community@g.us", Logging: false}}); err != nil {
		t.Fatal(err)
	}
	rules, err := st.ChatRules(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	p := ingest.NewPipeline(st, ingest.NewFilter(ingest.NewRuleSet(rules), nil), communityQueue{}, communityHealth{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.Handle(&events.JoinedGroup{GroupInfo: types.GroupInfo{JID: jid("child@g.us"),
		GroupLinkedParent: types.GroupLinkedParent{LinkedParentJID: jid("community@g.us")}}})

	rules, err = st.ChatRules(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	restarted := ingest.NewPipeline(st, ingest.NewFilter(ingest.NewRuleSet(rules), nil), communityQueue{}, communityHealth{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	restarted.Handle(messageEvent("child@g.us", "after-restart", &waE2E.Message{Conversation: proto.String("must stay excluded")}))

	rows, err := st.ChatList(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.CanonicalJID == "child@g.us" && row.MessageCount != 0 {
			t.Fatalf("ordinary message after restart was archived: %+v", row)
		}
	}
	controls, err := st.ChatControls(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if controls["child@g.us"].Parent != "community@g.us" {
		t.Fatalf("safe child directory metadata was not persisted: %+v", controls["child@g.us"])
	}
}

func TestJoinedCommunityMetadataSurvivesOrdinaryMessages(t *testing.T) {
	st := testkit.Store(t)
	rules := ingest.NewRuleSet(nil)
	p := ingest.NewPipeline(st, ingest.NewFilter(rules, nil), communityQueue{}, communityHealth{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.Handle(&events.JoinedGroup{GroupInfo: types.GroupInfo{
		JID:         jid("community@g.us"),
		GroupName:   types.GroupName{Name: "Neighbours"},
		GroupParent: types.GroupParent{IsParent: true},
	}})
	p.Handle(&events.JoinedGroup{GroupInfo: types.GroupInfo{
		JID:               jid("child@g.us"),
		GroupName:         types.GroupName{Name: "Announcements"},
		GroupLinkedParent: types.GroupLinkedParent{LinkedParentJID: jid("community@g.us")},
	}})
	p.Handle(messageEvent("community@g.us", "parent-message", &waE2E.Message{Conversation: proto.String("hello")}))
	p.Handle(messageEvent("child@g.us", "child-message", &waE2E.Message{Conversation: proto.String("hello")}))

	controls, err := st.ChatControls(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got := controls["community@g.us"].Type; got != "community" {
		t.Fatalf("community type after an ordinary message = %q, want community", got)
	}
	child := controls["child@g.us"]
	if child.Type != "group" || child.Parent != "community@g.us" {
		t.Fatalf("child metadata after an ordinary message = %+v, want group under community@g.us", child)
	}
	communities, _, _, err := st.ChatListPage(t.Context(), store.ChatListFilter{Type: "community"}, store.ChatListAfter{}, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(communities) != 1 || communities[0].CanonicalJID != "community@g.us" || communities[0].DisplayName != "Neighbours" {
		t.Fatalf("community-filtered chat list = %+v", communities)
	}
}
