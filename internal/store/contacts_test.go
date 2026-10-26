package store_test

import (
	"context"
	"errors"
	"testing"

	"whatsapp-archiver/internal/store"
)

func setContacts(t *testing.T, s *store.Store, names ...store.ContactName) int {
	t.Helper()
	n, err := s.SetContactNames(context.Background(), names)
	if err != nil {
		t.Fatalf("SetContactNames: %v", err)
	}
	return n
}

// participantRow reads "push_name|contact_name|updated_at>0" of a participant, or "no row".
func participantRow(t *testing.T, s *store.Store, jid string) string {
	t.Helper()
	got := table(t, readDB(t, s), `SELECT push_name, contact_name, updated_at>0 FROM participants WHERE jid=?`, jid)
	if got == "" {
		return "no row"
	}
	return got[:len(got)-1]
}

func statName(t *testing.T, s *store.Store, canonical string) string {
	t.Helper()
	got := table(t, readDB(t, s), `SELECT name FROM chat_stats WHERE canonical=?`, canonical)
	if got == "" {
		return "no row"
	}
	return got[:len(got)-1]
}

func dataVersion(t *testing.T, s *store.Store) int {
	t.Helper()
	var v int
	if err := readDB(t, s).QueryRow("PRAGMA data_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// A contact name inserts a participant row, changes it, and leaves the push name alone (the two are separate
// columns, schema section 3).
func TestSetContactNamesInsertsAndUpdates(t *testing.T) {
	s := open(t)
	if n := setContacts(t, s, store.ContactName{JID: "9665@s.whatsapp.net", Name: "Layla Khan"}); n != 1 {
		t.Errorf("changed = %d, want 1", n)
	}
	if got := participantRow(t, s, "9665@s.whatsapp.net"); got != "|Layla Khan|1" {
		t.Errorf("new participant = %q", got)
	}

	apply(t, s, store.Batch{Sender: &store.Participant{JID: "9665@s.whatsapp.net", PushName: "Lay", UpdatedAt: 50}})
	if got := participantRow(t, s, "9665@s.whatsapp.net"); got != "Lay|Layla Khan|1" {
		t.Errorf("after a push name = %q: the push name must not touch contact_name", got)
	}
	if n := setContacts(t, s, store.ContactName{JID: "9665@s.whatsapp.net", Name: "Layla K."}); n != 1 {
		t.Errorf("changed = %d, want 1", n)
	}
	if got := participantRow(t, s, "9665@s.whatsapp.net"); got != "Lay|Layla K.|1" {
		t.Errorf("after a rename = %q: the push name must survive", got)
	}
	// And a later push name keeps the contact name.
	apply(t, s, store.Batch{Sender: &store.Participant{JID: "9665@s.whatsapp.net", PushName: "Layla!", UpdatedAt: 60}})
	if got := participantRow(t, s, "9665@s.whatsapp.net"); got != "Layla!|Layla K.|1" {
		t.Errorf("after a second push name = %q", got)
	}
}

// updated_at is the time of the contact name change.
func TestSetContactNamesStampsUpdatedAt(t *testing.T) {
	s := open(t)
	apply(t, s, store.Batch{Sender: &store.Participant{JID: "5@lid", PushName: "Old", UpdatedAt: 5}})
	setContacts(t, s, store.ContactName{JID: "5@lid", Name: "New"})
	var at int64
	if err := readDB(t, s).QueryRow(`SELECT updated_at FROM participants WHERE jid='5@lid'`).Scan(&at); err != nil {
		t.Fatal(err)
	}
	if at < 1_000_000_000 {
		t.Errorf("updated_at = %d, want the current time", at)
	}
}

// The unchanged-write skip is real: the same names again modify nothing and report 0 changes.
func TestSetContactNamesUnchangedWritesNothing(t *testing.T) {
	s := open(t)
	apply(t, s, mb("5@s.whatsapp.net", "A", "text", "x", 10))
	names := []store.ContactName{{JID: "5@s.whatsapp.net", Name: "Layla"}, {JID: "6@lid", Name: "Omar"}}
	setContacts(t, s, names...)
	before := dataVersion(t, s)
	if n := setContacts(t, s, names...); n != 0 {
		t.Errorf("changed = %d, want 0", n)
	}
	if after := dataVersion(t, s); before != after {
		t.Fatalf("an unchanged contact list modified the database (data_version %d -> %d)", before, after)
	}
	// One changed name among unchanged ones counts once.
	if n := setContacts(t, s, store.ContactName{JID: "5@s.whatsapp.net", Name: "Layla"}, store.ContactName{JID: "6@lid", Name: "Omar S"}); n != 1 {
		t.Errorf("changed = %d, want 1", n)
	}
}

// An empty name is skipped, never erasing a stored one (a removed contact keeps its last name; same rule as push_name),
// and creates no row.
func TestSetContactNamesSkipsEmptyNames(t *testing.T) {
	s := open(t)
	setContacts(t, s, store.ContactName{JID: "5@lid", Name: "Layla"})
	if n := setContacts(t, s, store.ContactName{JID: "5@lid", Name: ""}, store.ContactName{JID: "6@lid", Name: ""}); n != 0 {
		t.Errorf("changed = %d, want 0", n)
	}
	if got := participantRow(t, s, "5@lid"); got != "|Layla|1" {
		t.Errorf("name erased: %q", got)
	}
	if got := participantRow(t, s, "6@lid"); got != "no row" {
		t.Errorf("empty name made a row: %q", got)
	}
	if n := setContacts(t, s); n != 0 {
		t.Errorf("no names: changed = %d", n)
	}
}

// A name without a JID is a caller bug: the whole call is refused and nothing is written.
func TestSetContactNamesRefusesAnEmptyJID(t *testing.T) {
	s := open(t)
	_, err := s.SetContactNames(context.Background(), []store.ContactName{{JID: "5@lid", Name: "Layla"}, {JID: "", Name: "Nobody"}})
	if !errors.Is(err, store.ErrConstraint) {
		t.Fatalf("err = %v, want ErrConstraint", err)
	}
	if got := participantRow(t, s, "5@lid"); got != "no row" {
		t.Errorf("a refused call wrote %q", got)
	}
}

// Atomic: when a later name fails, the earlier ones, and the chat names they changed, are rolled back.
func TestSetContactNamesIsAtomic(t *testing.T) {
	s := open(t)
	apply(t, s, mb("5@s.whatsapp.net", "A", "text", "x", 10))
	db := rawDB(t, s)
	if _, err := db.Exec(`CREATE TRIGGER boom BEFORE INSERT ON participants WHEN NEW.contact_name='boom' BEGIN SELECT RAISE(ABORT,'boom'); END`); err != nil {
		t.Fatal(err)
	}
	_, err := s.SetContactNames(context.Background(), []store.ContactName{{JID: "5@s.whatsapp.net", Name: "Layla"}, {JID: "7@lid", Name: "boom"}})
	if err == nil {
		t.Fatal("want the trigger's error")
	}
	if got := participantRow(t, s, "5@s.whatsapp.net"); got != "no row" {
		t.Errorf("first name survived the failed call: %q", got)
	}
	if got := statName(t, s, "5@s.whatsapp.net"); got != "" {
		t.Errorf("chat name = %q after a failed call", got)
	}
	// The store is still usable.
	setContacts(t, s, store.ContactName{JID: "5@s.whatsapp.net", Name: "Layla"})
	if got := statName(t, s, "5@s.whatsapp.net"); got != "Layla" {
		t.Errorf("chat name = %q", got)
	}
}

// W2(d) divergence: writing contact_name must refresh chat_stats.name. The order is chat name, then contact name,
// then push name.
func TestSetContactNamesRefreshesTheChatName(t *testing.T) {
	s := open(t)
	apply(t, s, mb("5@s.whatsapp.net", "A", "text", "x", 10))
	apply(t, s, store.Batch{Sender: &store.Participant{JID: "5@s.whatsapp.net", PushName: "lay", UpdatedAt: 11}})
	if got := statName(t, s, "5@s.whatsapp.net"); got != "lay" {
		t.Fatalf("push-name chat = %q", got)
	}
	setContacts(t, s, store.ContactName{JID: "5@s.whatsapp.net", Name: "Layla Khan"})
	if got := statName(t, s, "5@s.whatsapp.net"); got != "Layla Khan" {
		t.Errorf("contact name did not reach chat_stats.name: %q", got)
	}
	setContacts(t, s, store.ContactName{JID: "5@s.whatsapp.net", Name: "Layla K."})
	if got := statName(t, s, "5@s.whatsapp.net"); got != "Layla K." {
		t.Errorf("rename did not reach chat_stats.name: %q", got)
	}
	// A chat name wins over the contact name; a later contact change leaves it.
	apply(t, s, store.Batch{Chat: &store.Chat{JID: "5@s.whatsapp.net", Name: "Layla (work)"}})
	setContacts(t, s, store.ContactName{JID: "5@s.whatsapp.net", Name: "Layla Z."})
	if got := statName(t, s, "5@s.whatsapp.net"); got != "Layla (work)" {
		t.Errorf("chat name = %q, the chat's own name wins", got)
	}
}

// Only the chat whose canonical includes the JID changes; the others and every other column stay.
func TestSetContactNamesTouchesOnlyThatChat(t *testing.T) {
	s := open(t)
	apply(t, s, mb("5@s.whatsapp.net", "A", "text", "x", 10))
	apply(t, s, mb("6@s.whatsapp.net", "B", "text", "y", 20))
	apply(t, s, withChat(mb("g@g.us", "C", "text", "z", 30), "Family", "group"))
	beforeOther, beforeGroup, beforeStats := stat(t, s, "6@s.whatsapp.net"), stat(t, s, "g@g.us"), stat(t, s, "5@s.whatsapp.net")
	setContacts(t, s, store.ContactName{JID: "5@s.whatsapp.net", Name: "Layla"})
	if got := stat(t, s, "6@s.whatsapp.net"); got != beforeOther {
		t.Errorf("another chat changed: %q -> %q", beforeOther, got)
	}
	if got := stat(t, s, "g@g.us"); got != beforeGroup {
		t.Errorf("a group changed: %q -> %q", beforeGroup, got)
	}
	if got, want := stat(t, s, "5@s.whatsapp.net"), "Layla"+beforeStats; got != want {
		t.Errorf("chat_stats = %q, want only the name to change: %q", got, want)
	}
	// A participant that is not a chat gets a row but no chat_stats row.
	setContacts(t, s, store.ContactName{JID: "99@lid", Name: "Stranger"})
	if got := stat(t, s, "99@lid"); got != "no row" {
		t.Errorf("a contact made a chat_stats row: %q", got)
	}
	if got := participantRow(t, s, "99@lid"); got != "|Stranger|1" {
		t.Errorf("participant = %q", got)
	}
}

// CON-7 and R8: JIDs are stored as received; the name of a chat is found through every alias of its canonical, in
// either direction, and also when the mapping arrives after the contact name.
func TestSetContactNamesResolvesAliases(t *testing.T) {
	t.Run("name under the phone number, chat under the LID", func(t *testing.T) {
		s := open(t)
		apply(t, s, mb("2@lid", "A", "text", "x", 10))
		mapping(t, s, "2@lid", "9665@s.whatsapp.net")
		setContacts(t, s, store.ContactName{JID: "9665@s.whatsapp.net", Name: "Layla"})
		if got := statName(t, s, "2@lid"); got != "Layla" {
			t.Errorf("name = %q", got)
		}
		if got := participantRow(t, s, "2@lid"); got != "no row" {
			t.Errorf("the LID got a participant row of its own: %q", got)
		}
		if got := participantRow(t, s, "9665@s.whatsapp.net"); got != "|Layla|1" {
			t.Errorf("the number was not stored as received: %q", got)
		}
	})
	t.Run("name under the LID, chat under the phone number", func(t *testing.T) {
		s := open(t)
		apply(t, s, mb("9665@s.whatsapp.net", "A", "text", "x", 10))
		mapping(t, s, "2@lid", "9665@s.whatsapp.net")
		setContacts(t, s, store.ContactName{JID: "2@lid", Name: "Layla"})
		if got := statName(t, s, "2@lid"); got != "Layla" {
			t.Errorf("name = %q", got)
		}
	})
	t.Run("mapping after the name", func(t *testing.T) {
		s := open(t)
		apply(t, s, mb("2@lid", "A", "text", "x", 10))
		setContacts(t, s, store.ContactName{JID: "9665@s.whatsapp.net", Name: "Layla"})
		if got := statName(t, s, "2@lid"); got != "" {
			t.Fatalf("name before the mapping = %q", got)
		}
		mapping(t, s, "2@lid", "9665@s.whatsapp.net")
		if got := statName(t, s, "2@lid"); got != "Layla" {
			t.Errorf("name after the mapping = %q", got)
		}
	})
}

// A contact name written before the chat exists names the chat when its first message arrives.
func TestContactNameBeforeTheFirstMessage(t *testing.T) {
	s := open(t)
	setContacts(t, s, store.ContactName{JID: "5@s.whatsapp.net", Name: "Layla"})
	apply(t, s, mb("5@s.whatsapp.net", "A", "text", "x", 10))
	if got := statName(t, s, "5@s.whatsapp.net"); got != "Layla" {
		t.Errorf("name = %q", got)
	}
}

// The last of two names for one JID in a call wins, as if they had been written in order.
func TestSetContactNamesLastDuplicateWins(t *testing.T) {
	s := open(t)
	apply(t, s, mb("5@s.whatsapp.net", "A", "text", "x", 10))
	setContacts(t, s, store.ContactName{JID: "5@s.whatsapp.net", Name: "First"}, store.ContactName{JID: "5@s.whatsapp.net", Name: "Second"})
	if got := participantRow(t, s, "5@s.whatsapp.net"); got != "|Second|1" {
		t.Errorf("participant = %q", got)
	}
	if got := statName(t, s, "5@s.whatsapp.net"); got != "Second" {
		t.Errorf("name = %q", got)
	}
}

// Raw tables are never touched, and the incremental result is what RebuildDerived computes.
func TestSetContactNamesLeavesRawTablesAndMatchesTheRebuild(t *testing.T) {
	s := open(t)
	workload(t, s)
	db := readDB(t, s)
	raw := func() string {
		return table(t, db, `SELECT (SELECT COUNT(*) FROM messages), (SELECT COUNT(*) FROM message_events), (SELECT COUNT(*) FROM chat_events), (SELECT COUNT(*) FROM id_mappings), (SELECT COUNT(*) FROM history_sync_chunks), (SELECT total(length(raw_proto)) FROM messages)`)
	}
	before := raw()
	var jids []string
	rows, err := db.Query(`SELECT canonical FROM chat_stats WHERE type<>'group' ORDER BY canonical`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var j string
		if err = rows.Scan(&j); err != nil {
			t.Fatal(err)
		}
		jids = append(jids, j)
	}
	_ = rows.Close()
	if len(jids) == 0 {
		t.Fatal("the workload has no one-to-one chat")
	}
	var names []store.ContactName
	for i, j := range jids {
		names = append(names, store.ContactName{JID: j, Name: "Contact " + string(rune('A'+i))})
	}
	setContacts(t, s, names...)
	if after := raw(); after != before {
		t.Errorf("raw tables changed:\n%s\n%s", before, after)
	}
	incremental := derivedState(t, s)
	if err = s.RebuildDerived(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rebuilt := derivedState(t, s); rebuilt != incremental {
		t.Errorf("rebuild differs from the incremental result:\n%s\n---\n%s", incremental, rebuilt)
	}
}
