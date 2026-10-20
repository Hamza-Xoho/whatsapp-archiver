package store_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/store"
)

func browseMsg(t *testing.T, s *store.Store, chat, id, sender string, ts int64, text string) {
	t.Helper()
	m := store.Message{ChatJID: chat, ID: id, SenderJID: sender, Timestamp: ts, ReceivedAt: ts, Source: "live", Text: text, RawProto: []byte(id)}
	if _, err := s.InsertMessage(context.Background(), m); err != nil {
		t.Fatal(err)
	}
}

func browseEvent(t *testing.T, s *store.Store, e store.MessageEvent) {
	t.Helper()
	if e.ReceivedAt == 0 {
		e.ReceivedAt = e.Timestamp
	}
	if _, err := s.InsertEvent(context.Background(), e); err != nil {
		t.Fatal(err)
	}
}

func rowFor(t *testing.T, rows []store.ChatRow, jid string) store.ChatRow {
	t.Helper()
	for _, r := range rows {
		if r.CanonicalJID == jid {
			return r
		}
	}
	t.Fatalf("no row for %s in %+v", jid, rows)
	return store.ChatRow{}
}

func TestChatListNamesRulesAndFilter(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	browseMsg(t, s, "120363001@g.us", "g1", "1@lid", 10, "x")
	browseMsg(t, s, "5551@s.whatsapp.net", "d1", "5551@s.whatsapp.net", 20, "x")
	browseMsg(t, s, "5552@s.whatsapp.net", "d2", "5552@s.whatsapp.net", 30, "x")
	if err := s.UpsertChat(ctx, store.Chat{JID: "120363001@g.us", Name: "Family", Type: "group"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertParticipant(ctx, store.Participant{JID: "5551@s.whatsapp.net", DisplayName: "Aisha"}); err != nil {
		t.Fatal(err)
	}
	media := &config.MediaRules{Download: []string{}}
	if err := s.SetChatRules(ctx, []store.ChatRule{{JID: "5552@s.whatsapp.net", Logging: false}, {JID: "120363001@g.us", Logging: true, Media: media}}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ChatList(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("%d rows, want 3", len(rows))
	}
	if r := rowFor(t, rows, "120363001@g.us"); r.DisplayName != "Family" || !r.Logging || !r.HasMediaRule {
		t.Errorf("group row %+v", r)
	}
	if r := rowFor(t, rows, "5551@s.whatsapp.net"); r.DisplayName != "Aisha" || !r.Logging || r.HasMediaRule || r.MessageCount != 1 {
		t.Errorf("direct row %+v", r)
	}
	if r := rowFor(t, rows, "5552@s.whatsapp.net"); r.DisplayName != "5552@s.whatsapp.net" || r.Logging {
		t.Errorf("unnamed row %+v", r)
	}
	for filter, want := range map[string][]string{
		"aISHA":  {"5551@s.whatsapp.net"},
		"fam":    {"120363001@g.us"},
		"5552@s": {"5552@s.whatsapp.net"},
		"zzz":    nil,
	} {
		rows, err := s.ChatList(ctx, filter)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, r := range rows {
			got = append(got, r.CanonicalJID)
		}
		if !slices.Equal(got, want) {
			t.Errorf("filter %q: %v, want %v", filter, got, want)
		}
	}
}

// A rule set on the phone form of a chat shows on the chat, whose canonical JID is its LID form.
func TestChatListResolvesRuleAndSeenAliases(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if _, err := s.InsertIDMapping(ctx, store.IDMapping{LID: "77@lid", PN: "9665@s.whatsapp.net", Source: "test", FirstSeen: time.Unix(1, 0)}); err != nil {
		t.Fatal(err)
	}
	browseMsg(t, s, "77@lid", "a", "77@lid", 10, "x")
	if err := s.SetChatRules(ctx, []store.ChatRule{{JID: "9665@s.whatsapp.net", Logging: false}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkChatSeen(ctx, "77@lid", 1); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ChatList(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if r := rowFor(t, rows, "77@lid"); r.Logging || !r.New {
		t.Fatalf("row %+v: want not logging and new", r)
	}
	if err := s.AcknowledgeChats(ctx, []string{"77@lid"}); err != nil {
		t.Fatal(err)
	}
	// The same chat later arrives under its phone form: it was already acknowledged, so it is not new again.
	if _, err := s.MarkChatSeen(ctx, "9665@s.whatsapp.net", 2); err != nil {
		t.Fatal(err)
	}
	rows, err = s.ChatList(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if r := rowFor(t, rows, "77@lid"); r.New {
		t.Fatalf("row %+v: new again under its phone form", r)
	}
}

func TestChatPagePagesNewestFirstBeforeTS(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	for i := 1; i <= 10; i++ {
		browseMsg(t, s, "g@g.us", fmt.Sprintf("m%02d", i), "1@lid", int64(100+i), fmt.Sprint("text", i))
	}
	page, err := s.ChatPage(ctx, "g@g.us", 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	if ids := viewIDs(page); !slices.Equal(ids, []string{"m10", "m09", "m08", "m07"}) {
		t.Fatalf("first page %v", ids)
	}
	page, err = s.ChatPage(ctx, "g@g.us", page[3].TS, 4)
	if err != nil {
		t.Fatal(err)
	}
	if ids := viewIDs(page); !slices.Equal(ids, []string{"m06", "m05", "m04", "m03"}) {
		t.Fatalf("second page %v", ids)
	}
}

func viewIDs(page []store.ViewMessage) []string {
	var out []string
	for _, m := range page {
		out = append(out, m.ID)
	}
	return out
}

// The page shows a chat's messages under all its JID forms, once each, with edits, deletions, reactions, media state
// and sender names.
func TestChatPageResolvesAliasesAndEvents(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if _, err := s.InsertIDMapping(ctx, store.IDMapping{LID: "77@lid", PN: "9665@s.whatsapp.net", Source: "test", FirstSeen: time.Unix(1, 0)}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertParticipant(ctx, store.Participant{JID: "9665@s.whatsapp.net", DisplayName: "Omar"}); err != nil {
		t.Fatal(err)
	}
	browseMsg(t, s, "77@lid", "a", "9665@s.whatsapp.net", 10, "original")
	browseMsg(t, s, "9665@s.whatsapp.net", "b", "me@s.whatsapp.net", 20, "bye")
	browseEvent(t, s, store.MessageEvent{ChatJID: "9665@s.whatsapp.net", TargetID: "a", Kind: "edit", EventID: "e1", NewText: "first edit", Timestamp: 11})
	browseEvent(t, s, store.MessageEvent{ChatJID: "77@lid", TargetID: "a", Kind: "edit", EventID: "e2", NewText: "second edit", Timestamp: 12})
	browseEvent(t, s, store.MessageEvent{ChatJID: "77@lid", TargetID: "b", Kind: "revoke", EventID: "r1", Timestamp: 21})
	browseEvent(t, s, store.MessageEvent{ChatJID: "77@lid", TargetID: "a", Kind: "reaction", EventID: "x1", SenderJID: "p1", Value: "👍", Timestamp: 13})
	browseEvent(t, s, store.MessageEvent{ChatJID: "77@lid", TargetID: "a", Kind: "reaction", EventID: "x2", SenderJID: "p2", Value: "😂", Timestamp: 14})
	browseEvent(t, s, store.MessageEvent{ChatJID: "77@lid", TargetID: "a", Kind: "reaction", EventID: "x3", SenderJID: "p2", Value: "", Timestamp: 15})
	if err := s.InsertMedia(ctx, store.Media{ChatJID: "9665@s.whatsapp.net", MessageID: "b", Type: "image", State: "done"}); err != nil {
		t.Fatal(err)
	}
	page, err := s.ChatPage(ctx, "77@lid", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if ids := viewIDs(page); !slices.Equal(ids, []string{"b", "a"}) {
		t.Fatalf("page %v", ids)
	}
	b, a := page[0], page[1]
	if a.Text != "original" || a.EditedText != "second edit" || a.SenderName != "Omar" || a.Deleted {
		t.Errorf("message a %+v", a)
	}
	if !slices.Equal(a.Reactions, []string{"👍"}) {
		t.Errorf("reactions %v: a removed reaction must not show", a.Reactions)
	}
	if !b.Deleted || b.MediaState != "done" || b.Text != "bye" {
		t.Errorf("message b %+v", b)
	}
}

func TestTraceListsEventsMediaAndGap(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	browseMsg(t, s, "g@g.us", "a", "1@lid", 10, "hello")
	browseEvent(t, s, store.MessageEvent{ChatJID: "g@g.us", TargetID: "a", Kind: "edit", EventID: "e1", NewText: "hi", Timestamp: 11})
	if err := s.InsertMedia(ctx, store.Media{ChatJID: "g@g.us", MessageID: "a", Type: "image", State: "pending"}); err != nil {
		t.Fatal(err)
	}
	tr, err := s.Trace(ctx, "g@g.us", "a")
	if err != nil {
		t.Fatal(err)
	}
	if tr.Message.Text != "hello" || len(tr.Events) != 1 || tr.Events[0].Kind != "edit" || tr.Media == nil || tr.Media.State != "pending" || tr.Gap {
		t.Fatalf("trace %+v", tr)
	}
	browseEvent(t, s, store.MessageEvent{ChatJID: "g@g.us", TargetID: "lost", Kind: "undecryptable", EventID: "u1", SenderJID: "1@lid", Timestamp: 12})
	tr, err = s.Trace(ctx, "g@g.us", "lost")
	if err != nil {
		t.Fatal(err)
	}
	if !tr.Gap || tr.Message.ID != "" || len(tr.Events) != 1 {
		t.Fatalf("gap trace %+v", tr)
	}
	if _, err = s.Trace(ctx, "g@g.us", "none"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unknown message: err %v, want sql.ErrNoRows", err)
	}
}
