package store_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"whatsapp-archiver/internal/store"
)

// I2 (Hamza, 26 Oct 2026): Aliases answers from chat_canonical, so it lists exactly the forms ChatList and the other
// reads treat as one chat.
func TestAliasesAreTheOtherFormsOfTheCanonicalGroup(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	for _, m := range []store.IDMapping{
		{LID: "1@lid", PN: "9@s.whatsapp.net"}, {LID: "2@lid", PN: "9@s.whatsapp.net"}, {LID: "5@lid", PN: "7@s.whatsapp.net"},
	} {
		m.Source, m.FirstSeen = "test", time.Unix(2, 0)
		if _, err := s.InsertIDMapping(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	for jid, want := range map[string][]string{
		"1@lid":            {"2@lid", "9@s.whatsapp.net"},
		"2@lid":            {"1@lid", "9@s.whatsapp.net"},
		"9@s.whatsapp.net": {"1@lid", "2@lid"},
		"5@lid":            {"7@s.whatsapp.net"},
		"7@s.whatsapp.net": {"5@lid"},
		"g@g.us":           nil, // a chat the archive has never seen: no forms but itself
	} {
		got, err := s.Aliases(ctx, jid)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("Aliases(%s) = %v, %v; want %v", jid, got, err, want)
		}
	}
}

// A mapping that joins two groups later gives both sides the union; the result is sorted, so it is deterministic.
func TestAliasesFollowAMergeAndStaySorted(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	for _, m := range []store.IDMapping{{LID: "3@lid", PN: "8@s.whatsapp.net"}, {LID: "1@lid", PN: "9@s.whatsapp.net"}, {LID: "3@lid", PN: "9@s.whatsapp.net"}} {
		m.Source, m.FirstSeen = "test", time.Unix(2, 0)
		if _, err := s.InsertIDMapping(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Aliases(ctx, "8@s.whatsapp.net")
	want := []string{"1@lid", "3@lid", "9@s.whatsapp.net"}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("Aliases = %v, %v; want %v", got, err, want)
	}
}
