package store_test

import (
	"testing"

	"whatsapp-archiver/internal/store"
)

func TestExportSnapshotFreezesAliasesMediaEventsAndEmptyDecision(t *testing.T) {
	s := open(t)
	ctx := t.Context()
	if err := s.UpsertChat(ctx, store.Chat{JID: "empty@g.us", Name: "Empty", Type: "group"}); err != nil {
		t.Fatal(err)
	}
	empty, err := s.NewExportSnapshot(ctx, "empty@g.us")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.InsertMessage(ctx, store.Message{ChatJID: "empty@g.us", ID: "after", Kind: "text", Source: "live", Timestamp: 1, ReceivedAt: 1, RawProto: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	if found, err := s.ExportSnapshotHasMessages(ctx, empty, 0, 0); err != nil || found {
		t.Fatalf("empty snapshot became nonempty: found=%v err=%v", found, err)
	}

	if _, err = s.InsertIDMapping(ctx, store.IDMapping{LID: "7@lid", PN: "700@s.whatsapp.net"}); err != nil {
		t.Fatal(err)
	}
	for _, message := range []store.Message{
		{ChatJID: "7@lid", ID: "base", Kind: "text", Text: "snapshot", Source: "live", Timestamp: 10, ReceivedAt: 10, RawProto: []byte("x")},
		{ChatJID: "7@lid", ID: "media-later", Kind: "document", Source: "live", Timestamp: 11, ReceivedAt: 11, RawProto: []byte("x")},
	} {
		if _, err = s.InsertMessage(ctx, message); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.InsertEvent(ctx, store.MessageEvent{ChatJID: "7@lid", TargetID: "base", Kind: "reaction", EventID: "before", Value: "ok", Timestamp: 12}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.InsertChatEvent(ctx, store.ChatEvent{ChatJID: "7@lid", Kind: "subject", EventID: "before", Value: "before", Timestamp: 13}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.NewExportSnapshot(ctx, "7@lid")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.InsertIDMapping(ctx, store.IDMapping{LID: "7@lid", PN: "701@s.whatsapp.net"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.InsertMessage(ctx, store.Message{ChatJID: "701@s.whatsapp.net", ID: "new-alias", Kind: "text", Text: "late", Source: "live", Timestamp: 9, ReceivedAt: 9, RawProto: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.InsertEvent(ctx, store.MessageEvent{ChatJID: "7@lid", TargetID: "base", Kind: "edit", EventID: "after", NewText: "late", Timestamp: 14}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.InsertChatEvent(ctx, store.ChatEvent{ChatJID: "7@lid", Kind: "subject", EventID: "after", Value: "after", Timestamp: 15}); err != nil {
		t.Fatal(err)
	}
	lateMedia := store.Media{ChatJID: "7@lid", MessageID: "media-later", Type: "document", Filename: "late.bin", State: "done"}
	if _, err = s.Apply(ctx, store.Batch{Media: &lateMedia}); err != nil {
		t.Fatal(err)
	}
	page, err := s.ExportPage(ctx, store.ExportParams{CanonicalJID: "7@lid", Snapshot: snapshot, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 || len(page[0].Events) != 1 || page[1].Media != nil {
		t.Fatalf("message enrichment crossed snapshot: %+v", page)
	}
	media, err := s.ExportMediaPage(ctx, store.ExportParams{CanonicalJID: "7@lid", Snapshot: snapshot, Limit: 20})
	if err != nil || len(media) != 0 {
		t.Fatalf("late media crossed snapshot: media=%+v err=%v", media, err)
	}
	events, err := s.ExportEventPage(ctx, store.ExportEventParams{CanonicalJID: "7@lid", Snapshot: snapshot, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].ID() != "before" || events[1].ID() != "before" {
		t.Fatalf("event timeline crossed snapshot: %+v", events)
	}
}
