package store_test

import (
	"testing"

	"whatsapp-archiver/internal/store"
)

func TestExportEventPageIncludesOrphansTargetlessAndChatEventsPrivately(t *testing.T) {
	s := open(t)
	ctx := t.Context()
	addSearchMessage(t, s, searchMessage("g@g.us", "known", "1@lid", "text", "body", 20))
	for _, e := range []store.MessageEvent{
		{ChatJID: "g@g.us", TargetID: "missing", Kind: "reaction", EventID: "orphan", Value: "👍", Timestamp: 10, ReceivedAt: 10, RawProto: []byte("secret")},
		{ChatJID: "g@g.us", TargetID: "", Kind: "protocol", EventID: "targetless", Value: "notice", Timestamp: 30, ReceivedAt: 30, RawProto: []byte("secret")},
		{ChatJID: "other@g.us", TargetID: "", Kind: "protocol", EventID: "foreign", Timestamp: 15},
	} {
		if _, err := s.InsertEvent(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.InsertChatEvent(ctx, store.ChatEvent{ChatJID: "g@g.us", Kind: "subject", EventID: "chat", Value: "renamed", Timestamp: 25, ReceivedAt: 25, RawProto: []byte("secret")}); err != nil {
		t.Fatal(err)
	}
	page, err := s.ExportEventPage(ctx, store.ExportEventParams{CanonicalJID: "g@g.us", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 3 || page[0].ID() != "orphan" || page[1].ID() != "chat" || page[2].ID() != "targetless" {
		t.Fatalf("event page = %+v", page)
	}
	for _, e := range page {
		if e.RawProto() != nil {
			t.Fatalf("raw proto escaped: %+v", e)
		}
		if e.ID() == "foreign" {
			t.Fatal("foreign event escaped")
		}
	}
}
