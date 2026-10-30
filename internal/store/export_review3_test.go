package store_test

import (
	"fmt"
	"testing"

	"whatsapp-archiver/internal/store"
)

func TestExportZeroTimestampCursorsTerminateWithoutDuplicates(t *testing.T) {
	s := open(t)
	ctx := t.Context()
	for i := range 205 {
		addSearchMessage(t, s, searchMessage("g@g.us", fmt.Sprintf("m%03d", i), "1@lid", "text", "x", 0))
		if _, err := s.InsertEvent(ctx, store.MessageEvent{ChatJID: "g@g.us", TargetID: fmt.Sprintf("target%03d", i), Kind: "reaction", EventID: "same", Value: "x", Timestamp: 0, ReceivedAt: int64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	var mts int64
	var mid string
	has := false
	count := 0
	for {
		p, err := s.ExportPage(ctx, store.ExportParams{CanonicalJID: "g@g.us", AfterTS: mts, AfterID: mid, HasCursor: has, Limit: 17})
		if err != nil {
			t.Fatal(err)
		}
		if len(p) == 0 {
			break
		}
		for _, r := range p {
			count++
			mts, mid = r.Message.Timestamp, r.Message.ID
			has = true
		}
	}
	if count != 205 {
		t.Fatalf("zero-ts messages=%d, want 205", count)
	}
	seen := map[string]bool{}
	var ets int64
	var key string
	has = false
	count = 0
	for {
		p, err := s.ExportEventPage(ctx, store.ExportEventParams{CanonicalJID: "g@g.us", AfterTS: ets, AfterKey: key, HasCursor: has, Limit: 19})
		if err != nil {
			t.Fatal(err)
		}
		if len(p) == 0 {
			break
		}
		for _, e := range p {
			if seen[e.Key] {
				t.Fatalf("duplicate event key %q", e.Key)
			}
			seen[e.Key] = true
			count++
			ets, key = e.TS, e.Key
			has = true
		}
	}
	if count != 205 {
		t.Fatalf("zero-ts events=%d, want 205", count)
	}
}

func TestExportEventCursorKeyIsUniqueAcrossAliasesTargetsAndArms(t *testing.T) {
	s := open(t)
	ctx := t.Context()
	if _, err := s.InsertIDMapping(ctx, store.IDMapping{LID: "7@lid", PN: "700@s.whatsapp.net"}); err != nil {
		t.Fatal(err)
	}
	addSearchMessage(t, s, searchMessage("7@lid", "known", "1@lid", "text", "x", 1))
	for _, e := range []store.MessageEvent{{ChatJID: "7@lid", TargetID: "a", Kind: "reaction", EventID: "same", Timestamp: 5}, {ChatJID: "700@s.whatsapp.net", TargetID: "b", Kind: "reaction", EventID: "same", Timestamp: 5}} {
		if _, err := s.InsertEvent(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.InsertChatEvent(ctx, store.ChatEvent{ChatJID: "7@lid", Kind: "reaction", EventID: "same", Timestamp: 5}); err != nil {
		t.Fatal(err)
	}
	first, err := s.ExportEventPage(ctx, store.ExportEventParams{CanonicalJID: "7@lid", Limit: 1})
	if err != nil || len(first) != 1 {
		t.Fatalf("first=%+v %v", first, err)
	}
	second, err := s.ExportEventPage(ctx, store.ExportEventParams{CanonicalJID: "7@lid", AfterTS: first[0].TS, AfterKey: first[0].Key, HasCursor: true, Limit: 10})
	if err != nil || len(second) != 2 {
		t.Fatalf("second=%+v %v", second, err)
	}
	keys := map[string]bool{first[0].Key: true}
	for _, e := range second {
		if keys[e.Key] {
			t.Fatalf("duplicate key %q", e.Key)
		}
		keys[e.Key] = true
	}
}
