package ingest_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"

	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

func storeAliasLookup(st *store.Store) func(string) ([]string, error) {
	return func(jid string) ([]string, error) { return st.Aliases(context.Background(), jid) }
}

// SC-2: a chat turned off is not stored from its next event, with no restart.
func TestRuleChangeAppliesToNextEvent(t *testing.T) {
	st := testkit.Store(t)
	rules := ingest.NewRuleSet(nil)
	p := ingest.NewPipeline(st, ingest.NewFilter(rules, storeAliasLookup(st)), &queueFake{}, &healthFake{}, quiet())
	p.Handle(messageEvent("g@g.us", "ONE", text("first")))
	rules.Replace([]store.ChatRule{{JID: "g@g.us", Logging: false}})
	p.Handle(messageEvent("g@g.us", "TWO", text("second")))
	if got := strings.Join(rows(t, st, "SELECT id FROM messages"), ","); got != "ONE" {
		t.Fatalf("messages = %q, want only ONE", got)
	}
	rules.Replace([]store.ChatRule{{JID: "g@g.us", Logging: true}})
	p.Handle(messageEvent("g@g.us", "THREE", text("third")))
	if got := strings.Join(rows(t, st, "SELECT id FROM messages ORDER BY id"), ","); got != "ONE,THREE" {
		t.Fatalf("messages = %q after turning logging back on", got)
	}
}

// Review Focus / CON-7: logging turned off on the phone form of a chat also stops its LID form.
func TestRuleOnPhoneFormExcludesLIDForm(t *testing.T) {
	st := testkit.Store(t)
	ctx := context.Background()
	if _, err := st.InsertIDMapping(ctx, store.IDMapping{LID: "1234@lid", PN: "9665@s.whatsapp.net", Source: "test", FirstSeen: time.Unix(1, 0)}); err != nil {
		t.Fatal(err)
	}
	rules := ingest.NewRuleSet(nil)
	p := ingest.NewPipeline(st, ingest.NewFilter(rules, storeAliasLookup(st)), &queueFake{}, &healthFake{}, quiet())
	rules.Replace([]store.ChatRule{{JID: "9665@s.whatsapp.net", Logging: false}})
	p.Handle(messageEvent("1234@lid", "LID", text("secret")))
	p.Handle(messageEvent("9665@s.whatsapp.net", "PN", text("secret")))
	if got := rows(t, st, "SELECT id FROM messages"); len(got) != 0 {
		t.Fatalf("stored %v for a chat whose phone form is not logged", got)
	}
	if !rules.Excluded("9665@s.whatsapp.net") || rules.Excluded("1234@lid") {
		t.Fatal("RuleSet.Excluded must answer for the JID as given; aliases are the filter's job")
	}
}

func TestRuleSetReplaceIsSafeWhileReading(t *testing.T) {
	rules := ingest.NewRuleSet(ingest.ExclusionRules([]string{"a@g.us"}))
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 500 {
				_ = rules.Excluded("a@g.us")
				_ = rules.Media("a@g.us")
			}
		}()
	}
	for i := range 500 {
		rules.Replace([]store.ChatRule{{JID: "a@g.us", Logging: i%2 == 0}})
	}
	wg.Wait()
}

// countingSeen counts the chat_seen writes the pipeline asks for.
type countingSeen struct {
	mu    sync.Mutex
	marks []string
	known []store.ChatSeen
}

func (c *countingSeen) MarkChatSeen(_ context.Context, jid string, _ int64) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.marks = append(c.marks, jid)
	return true, nil
}
func (c *countingSeen) SeenChats(context.Context) ([]store.ChatSeen, error) { return c.known, nil }

// P2: chat_seen is written on a chat's first sighting only, never once per message, and chats already in chat_seen at
// start are never written again. Excluded chats are not marked.
func TestPipelineMarksChatSeenOncePerChat(t *testing.T) {
	st := testkit.Store(t)
	seen := &countingSeen{known: []store.ChatSeen{{JID: "old@g.us", FirstSeen: 1}}}
	tracker, err := ingest.NewSeenTracker(context.Background(), seen)
	if err != nil {
		t.Fatal(err)
	}
	rules := ingest.NewRuleSet(ingest.ExclusionRules([]string{"off@g.us"}))
	p := ingest.NewPipeline(st, ingest.NewFilter(rules, nil), &queueFake{}, &healthFake{}, quiet()).WithSeen(tracker)
	for _, id := range []string{"A", "B", "C"} {
		p.Handle(messageEvent("new@g.us", id, text(id)))
		p.Handle(messageEvent("old@g.us", "O"+id, text(id)))
		p.Handle(messageEvent("off@g.us", "X"+id, text(id)))
	}
	if strings.Join(seen.marks, ",") != "new@g.us" {
		t.Fatalf("chat_seen writes = %v, want exactly one for new@g.us", seen.marks)
	}
}

// With a real store the first sighting of a chat lands in chat_seen.
func TestPipelineRecordsFirstSightingInStore(t *testing.T) {
	st := testkit.Store(t)
	tracker, err := ingest.NewSeenTracker(context.Background(), st)
	if err != nil {
		t.Fatal(err)
	}
	p := ingest.NewPipeline(st, ingest.NewFilter(nil, nil), &queueFake{}, &healthFake{}, quiet()).WithSeen(tracker)
	p.Handle(messageEvent("g@g.us", "A", text("a")))
	if got := strings.Join(rows(t, st, "SELECT jid,acknowledged FROM chat_seen"), ","); got != "g@g.us|0" {
		t.Fatalf("chat_seen = %q", got)
	}
}

// failingSeen is a chat_seen store whose writes fail (a plain error, not a constraint, so persist would retry it).
type failingSeen struct {
	mu    sync.Mutex
	marks int
}

func (f *failingSeen) MarkChatSeen(context.Context, string, int64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.marks++
	return false, errors.New("disk I/O error")
}
func (f *failingSeen) SeenChats(context.Context) ([]store.ChatSeen, error) { return nil, nil }

// F6: the seen flag is a UI convenience. A failing chat_seen write after the message is stored does not fail or
// retry the event, hold up the media enqueue or the health tick, or mark the chat; the next sighting tries again.
func TestSeenWriteFailureDoesNotHoldUpCapture(t *testing.T) {
	st := testkit.Store(t)
	seen := &failingSeen{}
	tracker, err := ingest.NewSeenTracker(context.Background(), seen)
	if err != nil {
		t.Fatal(err)
	}
	q, h := &queueFake{}, &healthFake{}
	var logs bytes.Buffer
	p := ingest.NewPipeline(st, ingest.NewFilter(nil, nil), q, h, slog.New(slog.NewTextHandler(&logs, nil))).WithSeen(tracker)
	img := messageEvent("g@g.us", "IMG", &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Mimetype: proto.String("image/jpeg")}})
	done := make(chan bool, 1)
	go func() { done <- p.Handle(img) }()
	select {
	case ack := <-done:
		if !ack {
			t.Fatal("Handle did not acknowledge an event that was stored")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Handle is stuck retrying the chat_seen write")
	}
	stats, _ := st.ChatStats(t.Context())
	if len(stats) != 1 || stats[0].MessageCount != 1 || len(q.items) != 1 || h.seen != 1 || h.failed != 0 {
		t.Fatalf("stats=%+v media=%d health seen=%d failed=%d", stats, len(q.items), h.seen, h.failed)
	}
	if !strings.Contains(logs.String(), "level=WARN") {
		t.Fatalf("the failed seen write is not logged at Warn:\n%s", logs.String())
	}
	p.Handle(messageEvent("g@g.us", "B", text("b")))
	if seen.marks != 2 {
		t.Fatalf("chat_seen writes = %d, want a second attempt at the next sighting", seen.marks)
	}
}
