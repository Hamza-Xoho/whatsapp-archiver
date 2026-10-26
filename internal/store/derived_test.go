package store_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"whatsapp-archiver/internal/store"
)

const (
	grp  = "120363111@g.us"
	pn1  = "966550100123@s.whatsapp.net"
	lid1 = "204871@lid"
	pn2  = "966550100456@s.whatsapp.net"
	pn3  = "966550100789@s.whatsapp.net"
	lid3 = "30@lid"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func apply(t *testing.T, s *store.Store, b store.Batch) store.Applied {
	t.Helper()
	res, err := s.Apply(context.Background(), b)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return res
}

func mapping(t *testing.T, s *store.Store, lid, pn string) {
	t.Helper()
	if _, err := s.InsertIDMapping(context.Background(), store.IDMapping{LID: lid, PN: pn, Source: "test", FirstSeen: time.Unix(1, 0)}); err != nil {
		t.Fatal(err)
	}
}

// workload drives the store through every path that maintains a derived table: messages of every kind, edits, revokes,
// reactions, media state moves, mappings that arrive before and after the messages, duplicates, gaps closed by a
// message, by an event and across aliases, and chats that only have a rule or a sighting.
func workload(t *testing.T, s *store.Store) {
	t.Helper()
	ctx := context.Background()
	mapping(t, s, lid1, pn1) // before any message of the chat

	apply(t, s, withChat(mb(grp, "G-1", "text", "كِتَاب جميل", 100), "Family", "group"))
	apply(t, s, withMedia(mb(grp, "G-2", "image", "", 110, func(m *store.Message) { m.Caption = "الصورة الجميلة" }), "pending", 1000, ""))
	apply(t, s, mb(grp, "G-3", "text", "hello world", 50, func(m *store.Message) { m.Source = "history" }))
	apply(t, s, withMedia(mb(grp, "G-4", "document", "", 120), "done", 5000, "تقرير_2026.pdf"))
	apply(t, s, mb(grp, "G-5", "location", "", 121, func(m *store.Message) { m.Details = `{"name":"Café Riyadh","address":"King Fahd Rd"}` }))
	apply(t, s, mb(grp, "G-6", "poll", "Lunch?", 122, func(m *store.Message) { m.Details = `{"question":"Lunch?","options":["Pizza","شاورما"],"max":1}` }))
	apply(t, s, mb(grp, "G-7", "contacts", "Bob", 123, func(m *store.Message) {
		m.Details = `{"display_name":"Bob","contacts":[{"display_name":"Bob Smith"},{"display_name":"Carol"}]}`
	}))
	apply(t, s, withMedia(mb(grp, "G-8", "voice", "", 124), "pending", 300, ""))
	apply(t, s, withMedia(mb(grp, "G-9", "video", "", 125), "skipped", 9000, ""))
	apply(t, s, withMedia(mb(grp, "G-10", "sticker", "", 126), "pending", 40, ""))
	apply(t, s, mb(grp, "G-11", "unknown", "", 127, func(m *store.Message) { m.Details = `{"fields":["x"]}` }))
	apply(t, s, mb(grp, "G-12", "event", "Picnic", 128, func(m *store.Message) { m.Details = `{"name":"Picnic"}` }))
	apply(t, s, mb(grp, "G-13", "system", "", 129, func(m *store.Message) { m.Details = `{"type":"callLogMesssage"}` }))
	apply(t, s, mb(grp, "G-14", "live_location", "", 130, func(m *store.Message) { m.Caption = "on my way" }))
	apply(t, s, withMedia(mb(grp, "G-15", "image", "", 131), "pending", 300, ""))
	apply(t, s, withMedia(mb(grp, "G-16", "image", "", 132), "expired", 700, ""))
	apply(t, s, mb(grp, "G-17", "text", "again", 133, func(m *store.Message) { m.FromMe = true }))

	// Events: an edit (searchable), a revoke (the message stays searchable), a reaction and a pin (their new_text is
	// an emoji / pin type and must not be indexed), a poll vote.
	apply(t, s, ev(grp, "G-1", "edit", "E1", 140, func(e *store.MessageEvent) { e.NewText = "كتابٌ جديد" }))
	apply(t, s, ev(grp, "G-6", "revoke", "E2", 141))
	apply(t, s, ev(grp, "G-1", "reaction", "E3", 142, func(e *store.MessageEvent) { e.NewText = "thumbsup"; e.Value = "+" }))
	apply(t, s, ev(grp, "G-1", "pin", "E4", 143, func(e *store.MessageEvent) { e.NewText = "pinned" }))
	apply(t, s, ev(grp, "G-6", "poll_vote", "E5", 144, func(e *store.MessageEvent) { e.Value = `{"options":["Pizza"]}` }))

	// Gaps: GAP1 stays open; GAP2 is closed by the message, GAP3 by an event whose own id it is, GAP4 is only
	// targeted by a reaction, so it stays open.
	gap := func(target string) store.Batch {
		return ev(grp, target, "undecryptable", "U-"+target, 150, func(e *store.MessageEvent) { e.Cause = "no_sender_key"; e.SenderJID = "" })
	}
	for _, g := range []string{"GAP1", "GAP2", "GAP3", "GAP4"} {
		apply(t, s, gap(g))
	}
	apply(t, s, mb(grp, "GAP2", "text", "late arrival", 135))
	apply(t, s, ev(grp, "", "protocol", "P-GAP3", 151, func(e *store.MessageEvent) { e.SourceID = "GAP3" }))
	apply(t, s, ev(grp, "GAP4", "reaction", "R-GAP4", 152, func(e *store.MessageEvent) { e.SourceID = "RID-GAP4"; e.Value = "+" }))

	// Media state moves, one transaction each.
	media := func(id, state string, updated int64) {
		m, err := s.MediaByID(ctx, grp, id)
		must(t, err)
		m.State, m.UpdatedAt = state, updated
		must(t, s.UpdateMedia(ctx, m))
	}
	media("G-2", "done", 200)
	media("G-10", "expired", 1000)
	media("G-10", "retry_requested", 1000)
	claimed, err := s.ClaimMedia(ctx, time.Unix(1000, 0))
	must(t, err)
	if claimed == nil || claimed.MessageID != "G-15" {
		t.Fatalf("claimed %+v, the workload expects G-15 (newest pending)", claimed)
	}
	must(t, s.ResetMediaOnStart(ctx, time.Now())) // G-15 back to pending; G-10's request is older than 24 h: failed
	media("G-8", "failed", 300)
	claimed, err = s.ClaimMedia(ctx, time.Unix(1000, 0))
	must(t, err)
	if claimed == nil || claimed.MessageID != "G-15" {
		t.Fatalf("second claim %+v, want G-15", claimed)
	}

	// A chat reached by its LID and by its number: the same message under both forms is one message.
	apply(t, s, withChat(mb(pn1, "P-1", "text", "مرحبا بالمدرسة", 200), "", ""))
	apply(t, s, store.Batch{Sender: &store.Participant{JID: pn1, PushName: "Layla", UpdatedAt: 201}})
	apply(t, s, mb(lid1, "P-2", "text", "Ok", 210))
	if res := apply(t, s, mb(lid1, "P-1", "text", "مرحبا بالمدرسة", 200)); res.New {
		t.Fatal("P-1 under the LID form must be a duplicate")
	}
	apply(t, s, ev(pn1, "PG1", "undecryptable", "U-PG1", 220, func(e *store.MessageEvent) { e.Cause = "other"; e.SenderJID = "" }))
	apply(t, s, ev(pn1, "PG2", "undecryptable", "U-PG2", 221, func(e *store.MessageEvent) { e.Cause = "other"; e.SenderJID = "" }))
	apply(t, s, mb(lid1, "PG1", "text", "closes the gap under the other form", 222))

	// A chat whose mapping arrives after its messages, twice: the canonical moves from the number to 9@lid, then to
	// 10@lid (string order, R8), and its chat_stats row moves with it.
	apply(t, s, mb(pn2, "Q-1", "text", "first", 300))
	apply(t, s, mb(pn2, "Q-2", "text", "second", 310, func(m *store.Message) { m.Source = "history" }))
	apply(t, s, withMedia(mb(pn2, "Q-3", "image", "", 311), "failed", 10, ""))
	mapping(t, s, "9@lid", pn2)
	apply(t, s, withChat(mb("9@lid", "Q-4", "text", "third", 320), "Dana", ""))
	mapping(t, s, "10@lid", pn2)

	// The same message stored under two forms before the mapping that links them (the dedupe cannot know yet).
	apply(t, s, withMedia(mb(pn3, "D-1", "image", "", 400), "done", 77, ""))
	apply(t, s, withMedia(mb(lid3, "D-1", "image", "", 400), "done", 77, ""))
	apply(t, s, mb(lid3, "D-2", "text", "only under the lid", 405))
	mapping(t, s, lid3, pn3)

	// A message whose media row comes alone and later: the filename joins the searchable text.
	apply(t, s, mb(grp, "G-18", "document", "", 160))
	apply(t, s, store.Batch{Media: &store.Media{ChatJID: grp, MessageID: "G-18", Source: "live", Type: "document", State: "pending", Filename: "invoice_late.pdf", MessageTimestamp: 160}})

	// Media rows with no message and no other trace of their chat, one per state: they make no chat_stats row (R20 has
	// four sources), whatever their state, in the incremental path and in the rebuild alike.
	for i, state := range []string{"skipped", "pending", "done", "failed", "expired"} {
		chat := fmt.Sprintf("%d%s", 770+i, "@g.us")
		apply(t, s, store.Batch{Media: &store.Media{ChatJID: chat, MessageID: "X-1", Source: "history", Type: "image", State: state, DeclaredSize: 5, MessageTimestamp: 7}})
	}

	// A mapping that arrives after the data and absorbs no stats row: the number was never a listed chat but carries a
	// push name and an undecryptable event.
	apply(t, s, mb("55@lid", "N-1", "text", "x", 600))
	apply(t, s, store.Batch{Sender: &store.Participant{JID: "55@s.whatsapp.net", PushName: "Layla", UpdatedAt: 1}})
	apply(t, s, ev("55@s.whatsapp.net", "NG1", "undecryptable", "U-NG1", 601, func(e *store.MessageEvent) { e.Cause = "other"; e.SenderJID = "" }))
	mapping(t, s, "55@lid", "55@s.whatsapp.net")

	// Chats that only have a rule or a sighting (R20), then a rename and a rule on a known chat.
	must(t, s.SetChatRules(ctx, []store.ChatRule{{JID: "999@g.us", Logging: true}, {JID: grp, Logging: true}}))
	if _, err = s.MarkChatSeen(ctx, "888@g.us", 5); err != nil {
		t.Fatal(err)
	}
	apply(t, s, store.Batch{Chat: &store.Chat{JID: grp, Name: "Family ★", Type: "group"}})

	// A batch that fails leaves nothing.
	bad := mb(grp, "BAD", "carrier_pigeon", "x", 1)
	if _, err = s.Apply(ctx, bad); err == nil {
		t.Fatal("an unknown kind must fail")
	}
}

// R3: the derived tables are maintained in Go, so a write path that forgets one is only caught by comparing with a
// rebuild from the raw tables. chat_canonical, chat_stats and the token index of both FTS tables must be identical.
func TestRebuildDerivedEqualsIncrementalMaintenance(t *testing.T) {
	s := open(t)
	workload(t, s)
	incremental := derivedState(t, s)

	// The comparison must not be vacuous: the workload has to have produced the interesting rows.
	for _, want := range []string{
		"chat_canonical\n", "10@lid|10@lid", "9@lid|10@lid", pn2 + "|10@lid", pn3 + "|" + lid3, "888@g.us|888@g.us",
		"chat_stats\n", "search_fts\n", "search_tri\n", "caf", "|-1|", // the edit is rowid -1
	} {
		if !strings.Contains(incremental, want) {
			t.Fatalf("workload state lacks %q:\n%s", want, incremental)
		}
	}
	if n := count(t, s, "SELECT COUNT(*) FROM chat_stats WHERE canonical IN (?, ?)", pn2, "9@lid"); n != 0 {
		t.Fatalf("%d stale chat_stats rows of the chat survived its mappings", n)
	}

	must(t, s.RebuildDerived(context.Background()))
	rebuilt := derivedState(t, s)
	if rebuilt != incremental {
		t.Fatalf("rebuild differs from incremental maintenance.\n--- incremental\n%s\n--- rebuilt\n%s", incremental, rebuilt)
	}
	must(t, s.RebuildDerived(context.Background())) // idempotent
	if again := derivedState(t, s); again != rebuilt {
		t.Fatalf("a second rebuild changed the state:\n%s", again)
	}
}

// A rebuild repairs a store whose derived tables were damaged or never filled (an archive restored from a backup).
func TestRebuildDerivedRepairsDamagedTables(t *testing.T) {
	s := open(t)
	workload(t, s)
	want := derivedState(t, s)
	db := rawDB(t, s)
	for _, q := range []string{`DELETE FROM chat_stats`, `DELETE FROM chat_canonical`, `DELETE FROM search_fts`, `INSERT INTO search_tri(rowid, body) VALUES (99999, 'garbage')`} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	_ = db.Close()
	must(t, s.RebuildDerived(context.Background()))
	if got := derivedState(t, s); got != want {
		t.Fatalf("rebuild did not restore the state.\n--- want\n%s\n--- got\n%s", want, got)
	}
}

// Rebuild recreates the two FTS tables exactly as the migration made them (contentless_delete=1, spike 8).
func TestRebuildDerivedRecreatesTheFTSTablesAsMigrated(t *testing.T) {
	s := open(t)
	db := rawDB(t, s)
	before := table(t, db, `SELECT name, sql FROM sqlite_master WHERE name IN ('search_fts','search_tri') ORDER BY name`)
	if !strings.Contains(before, "contentless_delete=1") || !strings.Contains(before, "trigram") {
		t.Fatalf("unexpected migrated tables:\n%s", before)
	}
	must(t, s.RebuildDerived(context.Background()))
	if after := table(t, db, `SELECT name, sql FROM sqlite_master WHERE name IN ('search_fts','search_tri') ORDER BY name`); after != before {
		t.Fatalf("rebuild changed the table definitions:\n%s\nvs\n%s", before, after)
	}
}

// The rebuild is one transaction: a failure part-way leaves the previous derived state in place.
func TestRebuildDerivedIsAtomic(t *testing.T) {
	s := open(t)
	workload(t, s)
	want := derivedState(t, s)
	db := rawDB(t, s)
	// A trigger that fails the first chat_stats insert of the refill.
	if _, err := db.Exec(`CREATE TRIGGER fail_stats BEFORE INSERT ON chat_stats BEGIN SELECT RAISE(ABORT, 'rejected'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.RebuildDerived(context.Background()); err == nil {
		t.Fatal("the rebuild must report the failure")
	}
	if _, err := db.Exec(`DROP TRIGGER fail_stats`); err != nil {
		t.Fatal(err)
	}
	if got := derivedState(t, s); got != want {
		t.Fatalf("a failed rebuild changed the derived state:\n%s", got)
	}
}

// Raw tables are never touched by a rebuild (CON-2).
func TestRebuildDerivedLeavesRawTablesAlone(t *testing.T) {
	s := open(t)
	workload(t, s)
	before, err := s.DumpRaw(context.Background())
	must(t, err)
	must(t, s.RebuildDerived(context.Background()))
	after, err := s.DumpRaw(context.Background())
	must(t, err)
	if before != after {
		t.Fatal("RebuildDerived changed a raw table")
	}
}
