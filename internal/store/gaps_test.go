package store_test

import (
	"context"
	"testing"
	"time"

	"whatsapp-archiver/internal/store"
)

// R11: an open gap is an undecryptable event whose message never arrived. It closes on a message with id = target_id,
// or on any other event that came from a message with that ID (source_id = target_id), under any form of the chat.
// IDs follow docs/reviews/2026-10-26-progress-phase0-2-archive.md, Task 6: a "…6F66" gap is closed by a protocol
// event that is the message itself; a "…FCCF" gap is a real message that someone only reacted to.

func gap(chat, id string) store.MessageEvent {
	return store.MessageEvent{ChatJID: chat, TargetID: id, Kind: "undecryptable", EventID: "gap-" + id, SenderJID: "p@lid", Cause: "no_sender_key", Timestamp: 5, ReceivedAt: 5}
}

func applyEvent(t *testing.T, s *store.Store, e store.MessageEvent) {
	t.Helper()
	if _, err := s.Apply(context.Background(), store.Batch{Event: &e}); err != nil {
		t.Fatal(err)
	}
}

func openGaps(t *testing.T, s *store.Store) int {
	t.Helper()
	n, err := s.OpenGapCount(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestGapClosesWhenTheMessageArrivesUnderAnotherFormOfTheChat(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	applyEvent(t, s, gap("9665@s.whatsapp.net", "3A1B0001"))
	if _, err := s.InsertIDMapping(ctx, store.IDMapping{LID: "77@lid", PN: "9665@s.whatsapp.net", Source: "test", FirstSeen: time.Unix(1, 0)}); err != nil {
		t.Fatal(err)
	}
	if n := openGaps(t, s); n != 1 {
		t.Fatalf("gaps = %d, want 1", n)
	}
	if _, err := s.InsertMessage(ctx, message("77@lid", "3A1B0001", time.Unix(6, 0))); err != nil {
		t.Fatal(err)
	}
	if n := openGaps(t, s); n != 0 {
		t.Fatalf("gaps = %d after the message arrived under the LID form, want 0", n)
	}
}

// The …6F66 shape: the event that fills the gap is a protocol message with no target; only its own ID (source_id)
// says which message it was.
func TestGapClosesWhenAnEventWithThatSourceIDArrives(t *testing.T) {
	s := open(t)
	applyEvent(t, s, gap("g@g.us", "3A1B6F66"))
	applyEvent(t, s, store.MessageEvent{ChatJID: "g@g.us", TargetID: "", Kind: "protocol", EventID: "ev1", SourceID: "3A1B6F66",
		SenderJID: "p@lid", Value: "GROUP_MEMBER_LABEL_CHANGE", Timestamp: 6, ReceivedAt: 6})
	if n := openGaps(t, s); n != 0 {
		t.Fatalf("gaps = %d, want 0: the protocol event is the message itself", n)
	}
	var canonical string
	if err := readDB(t, s).QueryRow("SELECT canonical_jid FROM open_gaps UNION ALL SELECT 'none' LIMIT 1").Scan(&canonical); err != nil || canonical != "none" {
		t.Fatalf("open_gaps = %q, %v", canonical, err)
	}
}

// Source IDs are matched across the chat's forms too.
func TestGapClosesOnASourceIDUnderAnAlias(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	applyEvent(t, s, gap("77@lid", "3A1B0002"))
	applyEvent(t, s, store.MessageEvent{ChatJID: "9665@s.whatsapp.net", Kind: "pin", EventID: "p1", TargetID: "OTHER", SourceID: "3A1B0002", Timestamp: 6, ReceivedAt: 6})
	if _, err := s.InsertIDMapping(ctx, store.IDMapping{LID: "77@lid", PN: "9665@s.whatsapp.net", Source: "test", FirstSeen: time.Unix(1, 0)}); err != nil {
		t.Fatal(err)
	}
	if n := openGaps(t, s); n != 0 {
		t.Fatalf("gaps = %d, want 0", n)
	}
}

// The …FCCF shape: a real message that someone reacted to later. The reaction targets the gap's ID, but it is not the
// gap's message, so the gap stays open. Edits, revokes and poll votes that target it behave the same.
func TestGapStaysOpenWhenAnEventOnlyTargetsIt(t *testing.T) {
	s := open(t)
	applyEvent(t, s, gap("g@g.us", "3A1BFCCF"))
	for i, kind := range []string{"reaction", "edit", "revoke", "poll_vote", "pin"} {
		applyEvent(t, s, store.MessageEvent{ChatJID: "g@g.us", TargetID: "3A1BFCCF", Kind: kind, EventID: "t" + string(rune('a'+i)), SourceID: "3A1B90" + string(rune('A'+i)), Value: "+", SenderJID: "q@lid", Timestamp: 7, ReceivedAt: 7})
	}
	if n := openGaps(t, s); n != 1 {
		t.Fatalf("gaps = %d, want 1: events that only target the gap do not close it", n)
	}
}

// An undecryptable event never closes another gap, and an empty source_id never closes anything.
func TestGapIsNotClosedByUndecryptableEventsOrEmptySourceIDs(t *testing.T) {
	s := open(t)
	applyEvent(t, s, gap("g@g.us", "3A1B0003"))
	other := gap("g@g.us", "3A1B0004")
	other.SourceID = "3A1B0003"
	applyEvent(t, s, other)
	applyEvent(t, s, store.MessageEvent{ChatJID: "g@g.us", TargetID: "3A1B0003", Kind: "protocol", EventID: "x", SourceID: "", Timestamp: 7, ReceivedAt: 7})
	if n := openGaps(t, s); n != 2 {
		t.Fatalf("gaps = %d, want 2", n)
	}
}

// Another chat's message with the same ID does not close the gap.
func TestGapIsNotClosedByAnotherChat(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	applyEvent(t, s, gap("g@g.us", "3A1B0005"))
	if _, err := s.InsertMessage(ctx, message("other@g.us", "3A1B0005", time.Unix(6, 0))); err != nil {
		t.Fatal(err)
	}
	applyEvent(t, s, store.MessageEvent{ChatJID: "other@g.us", Kind: "protocol", EventID: "e", SourceID: "3A1B0005", Timestamp: 6, ReceivedAt: 6})
	if n := openGaps(t, s); n != 1 {
		t.Fatalf("gaps = %d, want 1", n)
	}
}

// The view carries the gap's canonical chat, and the cause that was stored.
func TestOpenGapsViewExposesCanonicalChatAndCause(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	applyEvent(t, s, gap("9665@s.whatsapp.net", "3A1B0006"))
	if _, err := s.InsertIDMapping(ctx, store.IDMapping{LID: "77@lid", PN: "9665@s.whatsapp.net", Source: "test", FirstSeen: time.Unix(1, 0)}); err != nil {
		t.Fatal(err)
	}
	if got := text(t, s, "SELECT canonical_jid||'|'||cause||'|'||target_id FROM open_gaps"); got != "77@lid|no_sender_key|3A1B0006" {
		t.Fatalf("open_gaps row = %q", got)
	}
}
