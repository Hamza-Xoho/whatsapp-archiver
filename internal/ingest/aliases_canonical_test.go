package ingest_test

import (
	"context"
	"testing"
	"time"

	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

// I2 (Hamza, 26 Oct 2026): a person with two LIDs mapped to one phone number is one chat. Logging turned off on one LID
// stops every form of the chat in the ingest filter, exactly where ChatList shows it as off.
func TestRuleOnOneLIDExcludesTheSiblingLIDAndChatListAgrees(t *testing.T) {
	st := testkit.Store(t)
	ctx := context.Background()
	for _, lid := range []string{"1@lid", "2@lid"} {
		if _, err := st.InsertIDMapping(ctx, store.IDMapping{LID: lid, PN: "9@s.whatsapp.net", Source: "test", FirstSeen: time.Unix(1, 0)}); err != nil {
			t.Fatal(err)
		}
	}
	rules := ingest.NewRuleSet(nil)
	p := ingest.NewPipeline(st, ingest.NewFilter(rules, storeAliasLookup(st)), &queueFake{}, &healthFake{}, quiet())
	p.Handle(messageEvent("1@lid", "BEFORE", text("kept: logging was on")))
	off := []store.ChatRule{{JID: "1@lid", Logging: false}}
	if err := st.SetChatRules(ctx, off); err != nil {
		t.Fatal(err)
	}
	rules.Replace(off)

	list, err := st.ChatList(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	var row *store.ChatRow
	for i := range list {
		if list[i].CanonicalJID == "1@lid" {
			row = &list[i]
		}
	}
	if row == nil || row.Logging {
		t.Fatalf("ChatList row for the chat = %+v, want it listed with logging off", row)
	}
	filter := ingest.NewFilter(rules, storeAliasLookup(st))
	for _, form := range []string{"1@lid", "2@lid", "9@s.whatsapp.net"} {
		excluded, err := filter.Excluded(form)
		if err != nil || !excluded {
			t.Errorf("Filter.Excluded(%s) = %v, %v; ChatList says the chat is off, so want true", form, excluded, err)
		}
	}
	p.Handle(messageEvent("2@lid", "AFTER-LID", text("must not be stored")))
	p.Handle(messageEvent("9@s.whatsapp.net", "AFTER-PN", text("must not be stored")))
	if got := rows(t, st, "SELECT id FROM messages ORDER BY id"); len(got) != 1 || got[0] != "BEFORE" {
		t.Fatalf("messages = %v, want only BEFORE", got)
	}
}
