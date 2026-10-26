package store_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"whatsapp-archiver/internal/store"
)

// stat reads one chat_stats row as "name|type|messages|last|oldest|oldest_history|done|failed|expired|pending|bytes|gaps".
func stat(t *testing.T, s *store.Store, canonical string) string {
	t.Helper()
	db := readDB(t, s)
	got := table(t, db, `SELECT name, type, messages, last_ts, oldest_ts, oldest_history_ts, media_done, media_failed, media_expired, media_pending, media_bytes, gaps_open FROM chat_stats WHERE canonical=?`, canonical)
	if got == "" {
		return "no row"
	}
	return got[:len(got)-1]
}

func wantStat(t *testing.T, s *store.Store, canonical, want string) {
	t.Helper()
	if got := stat(t, s, canonical); got != want {
		t.Errorf("chat_stats[%s] = %q, want %q", canonical, got, want)
	}
}

// Schema 4.3 step 3: messages, last_ts, oldest_ts and oldest_history_ts follow every new message.
func TestChatStatsCountMessagesAndTimes(t *testing.T) {
	s := open(t)
	apply(t, s, mb("c@g.us", "A", "text", "a", 100))
	wantStat(t, s, "c@g.us", "||1|100|100|NULL|0|0|0|0|0|0")
	apply(t, s, mb("c@g.us", "B", "text", "b", 70))
	wantStat(t, s, "c@g.us", "||2|100|70|NULL|0|0|0|0|0|0")
	apply(t, s, mb("c@g.us", "C", "text", "c", 90, func(m *store.Message) { m.Source = "history" }))
	wantStat(t, s, "c@g.us", "||3|100|70|90|0|0|0|0|0|0")
	apply(t, s, mb("c@g.us", "D", "text", "d", 40, func(m *store.Message) { m.Source = "history" }))
	wantStat(t, s, "c@g.us", "||4|100|40|40|0|0|0|0|0|0")
	apply(t, s, mb("c@g.us", "E", "text", "e", 500))
	wantStat(t, s, "c@g.us", "||5|500|40|40|0|0|0|0|0|0")
}

// A duplicate (same id, same chat or another alias) writes nothing: counts, media and the index stay as they were.
func TestDuplicateApplyDoesNotDoubleCount(t *testing.T) {
	s := open(t)
	b := withMedia(mb("9665@s.whatsapp.net", "M1", "image", "", 100, func(m *store.Message) { m.Caption = "once" }), "pending", 50, "")
	apply(t, s, b)
	db := rawDB(t, s)
	mapping(t, s, "77@lid", "9665@s.whatsapp.net")
	want := stat(t, s, "77@lid") // after the mapping the row is the merged one
	for _, chat := range []string{"9665@s.whatsapp.net", "77@lid", "9665@s.whatsapp.net"} {
		m := *b.Message
		m.ChatJID = chat
		again := b
		again.Message = &m
		if res := apply(t, s, again); res.New {
			t.Fatalf("duplicate under %s reported as new", chat)
		}
	}
	e := ev("77@lid", "M1", "edit", "E1", 120, func(e *store.MessageEvent) { e.NewText = "edited" })
	if first := apply(t, s, e); !first.New {
		t.Fatal("the first edit must be new")
	}
	if second := apply(t, s, e); second.New {
		t.Fatal("the same edit again must not be new")
	}
	if got := stat(t, s, "77@lid"); got != want {
		t.Errorf("duplicates changed chat_stats: %q, was %q", got, want)
	}
	if got := docs(t, db, "search_fts"); got != "-1,1" {
		t.Errorf("index docs = %q, want the message (1) and the one edit (-1)", got)
	}
}

// A failing Apply leaves the derived tables as they were (R2): the media insert fails after the message, its stats and
// its index rows were written in the same transaction.
func TestFailedApplyLeavesDerivedTablesUntouched(t *testing.T) {
	s := open(t)
	apply(t, s, withChat(mb("g@g.us", "OK", "text", "kept", 50), "Family", "group"))
	db := rawDB(t, s)
	before := derivedState(t, s)
	failInserts(t, s, "media", "SELECT RAISE(ABORT, 'rejected')")
	b := withMedia(mb("g@g.us", "M1", "image", "", 100, func(m *store.Message) { m.Caption = "never stored" }), "pending", 5, "")
	b.Chat = &store.Chat{JID: "g@g.us", Name: "Renamed", Type: "group"}
	if _, err := s.Apply(context.Background(), b); !errors.Is(err, store.ErrConstraint) {
		t.Fatalf("err = %v, want a constraint error", err)
	}
	// And a batch for a chat that is new: no stats row appears either.
	b2 := withMedia(mb("new@g.us", "M2", "image", "", 100), "pending", 5, "")
	if _, err := s.Apply(context.Background(), b2); !errors.Is(err, store.ErrConstraint) {
		t.Fatalf("err = %v, want a constraint error", err)
	}
	if after := derivedState(t, s); after != before {
		t.Fatalf("a failed Apply changed the derived tables.\n--- before\n%s\n--- after\n%s", before, after)
	}
	if got := docs(t, db, "search_tri"); got != "1" {
		t.Errorf("search_tri docs = %q, want only the first message", got)
	}
}

// The display name is the chat's name, else the contact or push name of the chat's own JID, else ”. It follows the
// directory rows (schema 4.3 step 2).
func TestChatStatsNameAndType(t *testing.T) {
	s := open(t)
	apply(t, s, withChat(mb("g@g.us", "A", "text", "x", 10), "Family", "group"))
	wantStat(t, s, "g@g.us", "Family|group|1|10|10|NULL|0|0|0|0|0|0")
	apply(t, s, store.Batch{Chat: &store.Chat{JID: "g@g.us", Name: "Family 2", Type: "group"}})
	wantStat(t, s, "g@g.us", "Family 2|group|1|10|10|NULL|0|0|0|0|0|0")
	apply(t, s, store.Batch{Chat: &store.Chat{JID: "g@g.us", Name: "", Type: "group"}}) // an empty name never erases
	wantStat(t, s, "g@g.us", "Family 2|group|1|10|10|NULL|0|0|0|0|0|0")

	// A one-to-one chat has no name of its own: the other side's push name is used, and follows a change.
	apply(t, s, withChat(mb("5@s.whatsapp.net", "B", "text", "x", 20), "", ""))
	wantStat(t, s, "5@s.whatsapp.net", "||1|20|20|NULL|0|0|0|0|0|0")
	apply(t, s, store.Batch{Sender: &store.Participant{JID: "5@s.whatsapp.net", PushName: "Layla", UpdatedAt: 21}})
	wantStat(t, s, "5@s.whatsapp.net", "Layla||1|20|20|NULL|0|0|0|0|0|0")
	apply(t, s, store.Batch{Sender: &store.Participant{JID: "5@s.whatsapp.net", PushName: "Layla K", UpdatedAt: 22}})
	wantStat(t, s, "5@s.whatsapp.net", "Layla K||1|20|20|NULL|0|0|0|0|0|0")
	apply(t, s, store.Batch{Chat: &store.Chat{JID: "5@s.whatsapp.net", Name: "Layla (work)"}})
	wantStat(t, s, "5@s.whatsapp.net", "Layla (work)||1|20|20|NULL|0|0|0|0|0|0")

	// A sender who is not a chat gets no row.
	if got := stat(t, s, alice); got != "no row" {
		t.Errorf("sender row = %q, want none", got)
	}
}

// Schema 4.3 step 6: every media state change moves one count between buckets in the same transaction; bytes follow
// the done rows (FR-25).
func TestChatStatsMediaBucketsFollowEveryStateChange(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	apply(t, s, withMedia(mb("c@g.us", "A", "image", "", 10), "pending", 100, ""))
	apply(t, s, withMedia(mb("c@g.us", "B", "image", "", 11), "skipped", 900, ""))
	wantStat(t, s, "c@g.us", "||2|11|10|NULL|0|0|0|1|0|0")
	set := func(id, state string, updated int64) {
		m, err := s.MediaByID(ctx, "c@g.us", id)
		must(t, err)
		m.State, m.UpdatedAt = state, updated
		must(t, s.UpdateMedia(ctx, m))
	}
	claimed, err := s.ClaimMedia(ctx, time.Unix(100, 0))
	if err != nil || claimed == nil {
		t.Fatalf("claim = %v, %v", claimed, err)
	}
	wantStat(t, s, "c@g.us", "||2|11|10|NULL|0|0|0|1|0|0") // downloading counts as pending
	set("A", "done", 20)
	wantStat(t, s, "c@g.us", "||2|11|10|NULL|1|0|0|0|100|0")
	set("A", "expired", 21)
	wantStat(t, s, "c@g.us", "||2|11|10|NULL|0|0|1|0|0|0") // bytes leave with the done row
	set("A", "retry_requested", 22)
	wantStat(t, s, "c@g.us", "||2|11|10|NULL|0|0|1|0|0|0") // expired and retry_requested share a bucket
	must(t, s.FailStaleRetries(ctx, time.Unix(22, 0).Add(25*time.Hour)))
	wantStat(t, s, "c@g.us", "||2|11|10|NULL|0|1|0|0|0|0")
	set("B", "pending", 30) // a skipped row asked for later
	wantStat(t, s, "c@g.us", "||2|11|10|NULL|0|1|0|1|0|0")
	set("B", "done", 31)
	wantStat(t, s, "c@g.us", "||2|11|10|NULL|1|1|0|0|900|0")
	set("B", "done", 32) // same state again: no change
	wantStat(t, s, "c@g.us", "||2|11|10|NULL|1|1|0|0|900|0")
	// A reset on start moves downloading back to pending (same bucket) and must not disturb the others.
	apply(t, s, withMedia(mb("c@g.us", "C", "image", "", 12), "downloading", 5, ""))
	must(t, s.ResetMediaOnStart(ctx, time.Unix(40, 0)))
	wantStat(t, s, "c@g.us", "||3|12|10|NULL|1|1|0|1|900|0")
}

// Gaps (R11): recomputed when an undecryptable event arrives and when a message or an event that closes one arrives.
func TestChatStatsGapsOpenFollowGapsAndTheirClosers(t *testing.T) {
	s := open(t)
	apply(t, s, mb("c@g.us", "A", "text", "x", 10))
	gap := func(target string) {
		apply(t, s, ev("c@g.us", target, "undecryptable", "U-"+target, 20, func(e *store.MessageEvent) { e.Cause = "other"; e.SenderJID = "" }))
	}
	gap("G1")
	gap("G2")
	gap("G3")
	wantStat(t, s, "c@g.us", "||1|10|10|NULL|0|0|0|0|0|3")
	apply(t, s, mb("c@g.us", "G1", "text", "late", 30))
	wantStat(t, s, "c@g.us", "||2|30|10|NULL|0|0|0|0|0|2")
	apply(t, s, ev("c@g.us", "", "protocol", "P2", 31, func(e *store.MessageEvent) { e.SourceID = "G2" }))
	wantStat(t, s, "c@g.us", "||2|30|10|NULL|0|0|0|0|0|1")
	apply(t, s, ev("c@g.us", "G3", "reaction", "R3", 32, func(e *store.MessageEvent) { e.SourceID = "other-id" })) // targets, does not close
	wantStat(t, s, "c@g.us", "||2|30|10|NULL|0|0|0|0|0|1")
	apply(t, s, mb("other@g.us", "G3", "text", "another chat", 33))
	wantStat(t, s, "c@g.us", "||2|30|10|NULL|0|0|0|0|0|1")
	// The gap and its closer under different forms of the same chat.
	mapping(t, s, "1@lid", "c1@s.whatsapp.net")
	apply(t, s, ev("c1@s.whatsapp.net", "H1", "undecryptable", "U-H1", 40, func(e *store.MessageEvent) { e.Cause = "other"; e.SenderJID = "" }))
	apply(t, s, mb("1@lid", "H0", "text", "row for the chat", 41))
	wantStat(t, s, "1@lid", "||1|41|41|NULL|0|0|0|0|0|1")
	apply(t, s, mb("1@lid", "H1", "text", "closes it", 42))
	wantStat(t, s, "1@lid", "||2|42|41|NULL|0|0|0|0|0|0")
}

// R20: a row exists for every canonical seen in messages, chats, chat_rules or chat_seen, with zero counts.
func TestChatStatsRowExistsForEveryCanonicalSeen(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	zero := "||0|0|0|NULL|0|0|0|0|0|0"
	must(t, s.SetChatRules(ctx, []store.ChatRule{{JID: "rule@g.us", Logging: false}}))
	wantStat(t, s, "rule@g.us", zero)
	if _, err := s.MarkChatSeen(ctx, "seen@g.us", 5); err != nil {
		t.Fatal(err)
	}
	wantStat(t, s, "seen@g.us", zero)
	apply(t, s, store.Batch{Chat: &store.Chat{JID: "dir@g.us", Name: "Dir", Type: "group"}})
	wantStat(t, s, "dir@g.us", "Dir|group|0|0|0|NULL|0|0|0|0|0|0")
	if got := count(t, s, "SELECT COUNT(*) FROM chat_stats"); got != 3 {
		t.Errorf("chat_stats has %d rows, want 3", got)
	}
	// The rule's chat later gets a message: the row is the same one, counts move.
	apply(t, s, mb("rule@g.us", "A", "text", "x", 9))
	wantStat(t, s, "rule@g.us", "||1|9|9|NULL|0|0|0|0|0|0")
	// A rule for a chat that is an alias of a known chat adds no row of its own.
	mapping(t, s, "5@lid", "5@s.whatsapp.net")
	apply(t, s, mb("5@lid", "B", "text", "x", 9))
	must(t, s.SetChatRules(ctx, []store.ChatRule{{JID: "5@s.whatsapp.net", Logging: true}}))
	if got := count(t, s, "SELECT COUNT(*) FROM chat_stats WHERE canonical LIKE '5@%'"); got != 1 {
		t.Errorf("rows for the 5@ chat = %d, want 1", got)
	}
}

// Schema 4.3 step 5: a new mapping that changes a canonical recomputes the merged chat from the raw tables and deletes
// the stale row; the same message under two forms counts once.
func TestMappingMergeRecomputesTheMergedChat(t *testing.T) {
	s := open(t)
	apply(t, s, withChat(mb("2@s.whatsapp.net", "A", "text", "one", 10), "", ""))
	apply(t, s, withMedia(mb("2@s.whatsapp.net", "S", "image", "", 12), "done", 7, ""))
	apply(t, s, withChat(mb("30@lid", "B", "text", "two", 20, func(m *store.Message) { m.Source = "history" }), "Two", ""))
	apply(t, s, mb("30@lid", "S", "image", "", 12)) // the same message S under the other form
	wantStat(t, s, "2@s.whatsapp.net", "||2|12|10|NULL|1|0|0|0|7|0")
	wantStat(t, s, "30@lid", "Two||2|20|12|20|0|0|0|0|0|0")
	mapping(t, s, "30@lid", "2@s.whatsapp.net")
	wantStat(t, s, "30@lid", "Two||3|20|10|20|1|0|0|0|7|0") // A, B, S: S counted once
	wantStat(t, s, "2@s.whatsapp.net", "no row")
	// A mapping that changes nothing changes nothing.
	mapping(t, s, "30@lid", "2@s.whatsapp.net")
	wantStat(t, s, "30@lid", "Two||3|20|10|20|1|0|0|0|7|0")
	// A second LID with a smaller string takes over the canonical (R8 is string order).
	mapping(t, s, "100@lid", "2@s.whatsapp.net")
	wantStat(t, s, "30@lid", "no row")
	wantStat(t, s, "100@lid", "Two||3|20|10|20|1|0|0|0|7|0")
	if fmt.Sprint(count(t, s, "SELECT COUNT(*) FROM chat_stats")) != "1" {
		t.Error("exactly one chat_stats row must remain")
	}
}

// A mapping that absorbs no chat_stats row (the new alias was never a listed chat) still changes the merged chat: the
// alias may carry the push name of the contact and the undecryptable events of the chat.
func TestMappingRefreshesNameAndGapsFromAnAliasWithoutARow(t *testing.T) {
	s := open(t)
	apply(t, s, mb("5@lid", "A", "text", "x", 10))
	apply(t, s, store.Batch{Sender: &store.Participant{JID: "5@s.whatsapp.net", PushName: "Layla", UpdatedAt: 1}})
	apply(t, s, ev("5@s.whatsapp.net", "G1", "undecryptable", "U1", 20, func(e *store.MessageEvent) { e.Cause = "other"; e.SenderJID = "" }))
	wantStat(t, s, "5@lid", "||1|10|10|NULL|0|0|0|0|0|0")
	if got := stat(t, s, "5@s.whatsapp.net"); got != "no row" {
		t.Fatalf("the number alone has a row: %q", got)
	}
	mapping(t, s, "5@lid", "5@s.whatsapp.net")
	wantStat(t, s, "5@lid", "Layla||1|10|10|NULL|0|0|0|0|0|1")
}

// R20 lists four sources of a chat_stats row: messages, chats, chat_rules and chat_seen. A media row alone, in any
// state, makes none; the chat gets its row, with the media counts, once it is seen.
func TestMediaOnlyChatHasNoRowUntilItIsSeen(t *testing.T) {
	s := open(t)
	for i, state := range []string{"skipped", "pending", "done", "failed", "expired"} {
		chat := fmt.Sprintf("m%d@g.us", i)
		apply(t, s, store.Batch{Media: &store.Media{ChatJID: chat, MessageID: "X", Source: "history", Type: "image", State: state, DeclaredSize: 9, MessageTimestamp: 1}})
		wantStat(t, s, chat, "no row")
	}
	must(t, s.SetChatRules(context.Background(), []store.ChatRule{{JID: "m2@g.us", Logging: true}, {JID: "m1@g.us", Logging: true}}))
	wantStat(t, s, "m2@g.us", "||0|0|0|NULL|1|0|0|0|9|0") // done
	wantStat(t, s, "m1@g.us", "||0|0|0|NULL|0|0|0|1|0|0") // pending
	wantStat(t, s, "m0@g.us", "no row")
	// A state change of a media-only chat keeps it row-less.
	m, err := s.MediaByID(context.Background(), "m3@g.us", "X")
	must(t, err)
	m.State = "done"
	must(t, s.UpdateMedia(context.Background(), m))
	wantStat(t, s, "m3@g.us", "no row")
}
