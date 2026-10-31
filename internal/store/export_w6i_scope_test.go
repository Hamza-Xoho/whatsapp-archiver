package store_test

import (
	"slices"
	"testing"

	"whatsapp-archiver/internal/store"
)

func TestExportMediaPageScopesThroughCanonicalMessagesAndTheirTimestamps(t *testing.T) {
	s := open(t)
	ctx := t.Context()
	if _, err := s.InsertIDMapping(ctx, store.IDMapping{LID: "7@lid", PN: "700@s.whatsapp.net"}); err != nil {
		t.Fatal(err)
	}
	for _, message := range []store.Message{
		{ChatJID: "7@lid", ID: "zero", Kind: "document", Source: "live", Timestamp: 100, ReceivedAt: 1, RawProto: []byte("x")},
		{ChatJID: "700@s.whatsapp.net", ID: "stale", Kind: "document", Source: "live", Timestamp: 110, ReceivedAt: 2, RawProto: []byte("x")},
		{ChatJID: "7@lid", ID: "outside", Kind: "document", Source: "live", Timestamp: 300, ReceivedAt: 3, RawProto: []byte("x")},
	} {
		if _, err := s.InsertMessage(ctx, message); err != nil {
			t.Fatal(err)
		}
	}
	for _, media := range []store.Media{
		{ChatJID: "7@lid", MessageID: "zero", Type: "document", Filename: "zero.bin", State: "done", MessageTimestamp: 0},
		{ChatJID: "700@s.whatsapp.net", MessageID: "stale", Type: "document", Filename: "stale.bin", State: "done", MessageTimestamp: 999},
		{ChatJID: "7@lid", MessageID: "outside", Type: "document", Filename: "outside.bin", State: "done", MessageTimestamp: 100},
		{ChatJID: "7@lid", MessageID: "orphan", Type: "document", Filename: "orphan.bin", State: "done", MessageTimestamp: 100},
	} {
		copy := media
		if _, err := s.Apply(ctx, store.Batch{Media: &copy}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.ExportMediaPage(ctx, store.ExportParams{CanonicalJID: "7@lid", From: 50, To: 150, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, len(page))
	for i := range page {
		ids[i] = page[i].Message.ID
		if page[i].Message.Timestamp < 50 || page[i].Message.Timestamp > 150 {
			t.Errorf("%s uses media timestamp %d instead of selected message timestamp", page[i].Message.ID, page[i].Message.Timestamp)
		}
	}
	if !slices.Equal(ids, []string{"zero", "stale"}) {
		t.Fatalf("scoped media IDs = %v, want in-range canonical messages only", ids)
	}
}

func TestExportPageOwnsCanonicalFormsAndDuplicateState(t *testing.T) {
	s := open(t)
	ctx := t.Context()
	if _, err := s.InsertIDMapping(ctx, store.IDMapping{LID: "7@lid", PN: "700@s.whatsapp.net"}); err != nil {
		t.Fatal(err)
	}
	for _, message := range []store.Message{
		{ChatJID: "7@lid", ID: "mine", Kind: "text", Text: "canonical", Source: "live", Timestamp: 100, ReceivedAt: 1, RawProto: []byte("x")},
		{ChatJID: "700@s.whatsapp.net", ID: "mine", Kind: "text", Text: "duplicate", Source: "history", Timestamp: 100, ReceivedAt: 2, RawProto: []byte("x")},
		{ChatJID: "foreign@g.us", ID: "foreign", Kind: "text", Text: "FOREIGN", Source: "live", Timestamp: 90, ReceivedAt: 1, RawProto: []byte("x")},
	} {
		if _, err := s.InsertMessage(ctx, message); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.ExportPage(ctx, store.ExportParams{CanonicalJID: "7@lid", Forms: []string{"foreign@g.us"}, DuplicateStateKnown: true, HasDuplicates: false, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].Message.ID != "mine" || page[0].Message.Text != "canonical" {
		t.Fatalf("caller-controlled canonicalization escaped Store ownership: %+v", page)
	}
}
