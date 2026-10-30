package store_test

import (
	"testing"
	"time"

	"whatsapp-archiver/internal/store"
)

func TestExportPageIsCanonicalChronologicalTypedAndPrivate(t *testing.T) {
	s := open(t)
	ctx := t.Context()
	if _, err := s.InsertIDMapping(ctx, store.IDMapping{LID: "7@lid", PN: "700@s.whatsapp.net"}); err != nil {
		t.Fatal(err)
	}
	for _, m := range []store.Message{
		{ChatJID: "7@lid", ID: "b", SenderJID: "2@lid", Kind: "document", Caption: "later", Details: `{}`, Source: "live", Timestamp: 200, ReceivedAt: 200, RawProto: []byte("SECRET-B")},
		{ChatJID: "700@s.whatsapp.net", ID: "a", SenderJID: "1@lid", Kind: "text", Text: "earlier", Details: `{}`, Source: "history", Timestamp: 100, ReceivedAt: 100, RawProto: []byte("SECRET-A")},
		{ChatJID: "other@g.us", ID: "foreign", Kind: "text", Text: "FOREIGN", Source: "live", Timestamp: 150, ReceivedAt: 150, RawProto: []byte("FOREIGN-RAW")},
	} {
		if _, err := s.InsertMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.InsertEvent(ctx, store.MessageEvent{ChatJID: "7@lid", TargetID: "a", Kind: "edit", EventID: "e1", NewText: "edited", Timestamp: 101, ReceivedAt: 101, RawProto: []byte("EVENT-SECRET")}); err != nil {
		t.Fatal(err)
	}
	page, err := s.ExportPage(ctx, store.ExportParams{CanonicalJID: "7@lid", From: 100, To: 200, Limit: 1})
	if err != nil || len(page) != 1 || page[0].Message.ID != "a" || page[0].Message.RawProto != nil || len(page[0].Events) != 1 || page[0].Events[0].RawProto != nil {
		t.Fatalf("first export page = %+v, %v", page, err)
	}
	next, err := s.ExportPage(ctx, store.ExportParams{CanonicalJID: "7@lid", AfterTS: page[0].Message.Timestamp, AfterID: page[0].Message.ID, From: 100, To: 200, Limit: 10})
	if err != nil || len(next) != 1 || next[0].Message.ID != "b" {
		t.Fatalf("second export page = %+v, %v", next, err)
	}
	aliases, err := s.ExportAliases(ctx, "7@lid")
	if err != nil || len(aliases) != 2 || aliases[0] != "7@lid" || aliases[1] != "700@s.whatsapp.net" {
		t.Fatalf("aliases = %v, %v", aliases, err)
	}
	if _, err = s.ExportPage(ctx, store.ExportParams{CanonicalJID: "unknown@g.us", Limit: 10}); err == nil {
		t.Fatal("unknown canonical chat export succeeded")
	}
	_ = time.UTC
}
