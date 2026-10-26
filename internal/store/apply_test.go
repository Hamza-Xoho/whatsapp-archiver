package store_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"modernc.org/sqlite"

	"whatsapp-archiver/internal/store"
)

// readDB opens a second, read-only connection on the archive for assertions that look at the tables themselves.
func readDB(t *testing.T, s *store.Store) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(s.Path())+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// count runs a COUNT(*)-style query on the archive.
func count(t *testing.T, s *store.Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := readDB(t, s).QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// text runs a single-value query on the archive.
func text(t *testing.T, s *store.Store, query string, args ...any) string {
	t.Helper()
	var v sql.NullString
	if err := readDB(t, s).QueryRow(query, args...).Scan(&v); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return v.String
}

// Commit counting: a connection hook registers a commit hook on every connection the driver opens, and counts per
// archive file. Only write transactions commit, so reads add nothing.
var (
	countersMu sync.Mutex
	counters   = map[string]*atomic.Int64{}
)

func init() {
	sqlite.RegisterConnectionHook(func(conn sqlite.ExecQuerier, dsn string) error {
		if h, ok := conn.(sqlite.HookRegisterer); ok && !strings.Contains(dsn, "mode=ro") {
			c := commitCounter(dsnPath(dsn))
			h.RegisterCommitHook(func() int32 { c.Add(1); return 0 })
		}
		return nil
	})
}

func dsnPath(dsn string) string {
	p, _, _ := strings.Cut(strings.TrimPrefix(dsn, "file:"), "?")
	return p
}

func commitCounter(path string) *atomic.Int64 {
	countersMu.Lock()
	defer countersMu.Unlock()
	c, ok := counters[path]
	if !ok {
		c = new(atomic.Int64)
		counters[path] = c
	}
	return c
}

// commitsOn is how many write transactions have committed on archive s so far.
func commitsOn(s *store.Store) int64 { return commitCounter(filepath.ToSlash(s.Path())).Load() }

func imageBatch(chat, id string) store.Batch {
	m := message(chat, id, time.Unix(100, 0))
	m.Kind, m.Text, m.Caption, m.FromMe, m.PushName = "image", "", "look at this", true, "Alice"
	return store.Batch{
		Chat:    &store.Chat{JID: chat, Name: "Family", Type: "group"},
		Sender:  &store.Participant{JID: m.SenderJID, PushName: "Alice", UpdatedAt: 100},
		Message: &m,
		Media:   &store.Media{ChatJID: chat, MessageID: id, SenderJID: m.SenderJID, Source: "live", Type: "image", MIME: "image/jpeg", DeclaredSize: 2048, State: "pending", MessageTimestamp: 100},
	}
}

// R2: chat, participant, message, media row and the canonical rows of a new event are one transaction, one commit.
func TestApplyStoresAMessageInOneTransaction(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	before := commitsOn(s)
	res, err := s.Apply(ctx, imageBatch("g@g.us", "M1"))
	if err != nil || !res.New {
		t.Fatalf("Apply = %+v, %v; want a new message", res, err)
	}
	if n := commitsOn(s) - before; n != 1 {
		t.Fatalf("Apply committed %d times, want 1 (it was 4 to 5 transactions per message before V1)", n)
	}
	if res.Seq == 0 {
		t.Fatal("Applied.Seq is the new message's seq and must not be 0")
	}
	if got := text(t, s, "SELECT name||'|'||type FROM chats WHERE jid='g@g.us'"); got != "Family|group" {
		t.Errorf("chat = %q", got)
	}
	if got := text(t, s, "SELECT push_name FROM participants WHERE jid='sender@lid'"); got != "Alice" {
		t.Errorf("participant push name = %q", got)
	}
	row := "SELECT kind||'|'||text||'|'||caption||'|'||from_me||'|'||push_name||'|'||source||'|'||ts||'|'||mentions||'|'||details FROM messages WHERE chat_jid='g@g.us' AND id='M1'"
	if got := text(t, s, row); got != "image||look at this|1|Alice|live|100|[]|{}" {
		t.Errorf("message row = %q", got)
	}
	if got := text(t, s, "SELECT seq FROM messages WHERE id='M1'"); got != "1" {
		t.Errorf("messages.seq = %q, want 1", got)
	}
	if got := text(t, s, "SELECT type||'|'||mime||'|'||declared_size||'|'||state||'|'||source_rank||'|'||message_ts FROM media WHERE message_id='M1'"); got != "image|image/jpeg|2048|pending|1|100" {
		t.Errorf("media row = %q", got)
	}
	// R8: every JID stored as a chat or sender has a canonical row (itself until a mapping says otherwise).
	for _, jid := range []string{"g@g.us", "sender@lid"} {
		if got := text(t, s, "SELECT canonical FROM chat_canonical WHERE alias=?", jid); got != jid {
			t.Errorf("canonical of %s = %q", jid, got)
		}
	}
}

// "new" must be right for the derived-table updates that run once: a duplicate is reported as not new, adds no row, and
// does not insert its media a second time.
func TestApplyReportsDuplicatesAsNotNew(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	first, err := s.Apply(ctx, imageBatch("g@g.us", "M1"))
	if err != nil || !first.New {
		t.Fatalf("first = %+v, %v", first, err)
	}
	second, err := s.Apply(ctx, imageBatch("g@g.us", "M1"))
	if err != nil || second.New {
		t.Fatalf("second = %+v, %v; want New=false", second, err)
	}
	if n := count(t, s, "SELECT COUNT(*) FROM messages"); n != 1 {
		t.Fatalf("messages = %d, want 1", n)
	}
	if n := count(t, s, "SELECT COUNT(*) FROM media"); n != 1 {
		t.Fatalf("media rows = %d, want 1", n)
	}
}

// A message already stored under another form of the chat is a duplicate, and its media is not queued twice.
func TestApplyDuplicateUnderAnAliasInsertsNoMedia(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if _, err := s.Apply(ctx, imageBatch("9665@s.whatsapp.net", "M1")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertIDMapping(ctx, store.IDMapping{LID: "77@lid", PN: "9665@s.whatsapp.net", Source: "test", FirstSeen: time.Unix(1, 0)}); err != nil {
		t.Fatal(err)
	}
	res, err := s.Apply(ctx, imageBatch("77@lid", "M1"))
	if err != nil || res.New {
		t.Fatalf("Apply = %+v, %v; want a duplicate", res, err)
	}
	if n := count(t, s, "SELECT COUNT(*) FROM media"); n != 1 {
		t.Fatalf("media rows = %d, want 1", n)
	}
}

// Events and chat events report "new" from RowsAffected too.
func TestApplyEventsAndChatEventsReportNew(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	ev := store.MessageEvent{ChatJID: "g@g.us", TargetID: "A", Kind: "reaction", EventID: "E1", SenderJID: "p@lid", Value: "+", Timestamp: 1, ReceivedAt: 1}
	ce := store.ChatEvent{ChatJID: "g@g.us", Kind: "app_state", EventID: "C1", Value: "{}", Timestamp: 1, ReceivedAt: 1}
	for i, want := range []bool{true, false} {
		res, err := s.Apply(ctx, store.Batch{Event: &ev})
		if err != nil || res.New != want {
			t.Fatalf("event attempt %d = %+v, %v; want New=%v", i, res, err, want)
		}
		res, err = s.Apply(ctx, store.Batch{ChatEvent: &ce})
		if err != nil || res.New != want {
			t.Fatalf("chat event attempt %d = %+v, %v; want New=%v", i, res, err, want)
		}
	}
	if got := text(t, s, "SELECT canonical FROM chat_canonical WHERE alias='p@lid'"); got != "p@lid" {
		t.Errorf("the event's sender has no canonical row: %q", got)
	}
	if got := text(t, s, "SELECT source||'|'||source_id||'|'||cause FROM message_events"); got != "live||" {
		t.Errorf("event defaults = %q, want source live, no source_id, no cause", got)
	}
}

// R2 atomicity: a failure at any step of the batch leaves nothing behind (no chat, participant, canonical row or
// message), and it is a constraint error, so the pipeline does not retry it forever.
func TestApplyIsAtomic(t *testing.T) {
	for _, table := range []string{"media", "messages", "participants"} {
		t.Run(table, func(t *testing.T) {
			s := open(t)
			failInserts(t, s, table, "SELECT RAISE(ABORT, 'rejected')")
			_, err := s.Apply(context.Background(), imageBatch("g@g.us", "M1"))
			if !errors.Is(err, store.ErrConstraint) {
				t.Fatalf("err = %v, want a constraint error", err)
			}
			for _, q := range []string{"chats", "participants", "messages", "media", "chat_canonical"} {
				if n := count(t, s, "SELECT COUNT(*) FROM "+q); n != 0 {
					t.Errorf("%s has %d rows after the failed batch", q, n)
				}
			}
		})
	}
}

// A closed store is not a constraint error: the pipeline retries it.
func TestApplyOnAClosedStoreIsNotAConstraintError(t *testing.T) {
	s := open(t)
	_ = s.Close()
	_, err := s.Apply(context.Background(), imageBatch("g@g.us", "M1"))
	if err == nil || errors.Is(err, store.ErrConstraint) {
		t.Fatalf("err = %v", err)
	}
}

func TestApplyRejectsMalformedBatches(t *testing.T) {
	ctx := context.Background()
	m := message("g@g.us", "A", time.Unix(1, 0))
	e := store.MessageEvent{ChatJID: "g@g.us", TargetID: "A", Kind: "edit", EventID: "E"}
	cases := map[string]store.Batch{
		"message and event":     {Message: &m, Event: &e},
		"message kind unknown":  {Message: func() *store.Message { x := m; x.Kind = "carrier_pigeon"; return &x }()},
		"event kind unknown":    {Event: func() *store.MessageEvent { x := e; x.Kind = "carrier_pigeon"; return &x }()},
		"event cause unknown":   {Event: func() *store.MessageEvent { x := e; x.Kind = "undecryptable"; x.Cause = "gremlins"; return &x }()},
		"event source unknown":  {Event: func() *store.MessageEvent { x := e; x.Source = "dream"; return &x }()},
		"details unknown field": {Message: func() *store.Message { x := m; x.Details = `{"nonsense":1}`; return &x }()},
		"details not json":      {Message: func() *store.Message { x := m; x.Details = `{`; return &x }()},
		"media state unknown":   {Media: &store.Media{ChatJID: "g@g.us", MessageID: "A", State: "teleported"}},
	}
	for name, b := range cases {
		t.Run(name, func(t *testing.T) {
			s := open(t)
			_, err := s.Apply(ctx, b)
			if !errors.Is(err, store.ErrConstraint) {
				t.Fatalf("err = %v, want a constraint error (a write that can never succeed)", err)
			}
			for _, q := range []string{"chats", "messages", "message_events", "media", "chat_canonical"} {
				if n := count(t, s, "SELECT COUNT(*) FROM "+q); n != 0 {
					t.Errorf("%s has %d rows after the rejected batch", q, n)
				}
			}
		})
	}
}

// An empty batch is a no-op; an empty kind is a plain text message (callers that do not classify yet).
func TestApplyEmptyBatchAndDefaultKind(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if res, err := s.Apply(ctx, store.Batch{}); err != nil || res.New {
		t.Fatalf("empty batch = %+v, %v", res, err)
	}
	m := message("g@g.us", "A", time.Unix(1, 0))
	m.Kind = ""
	if _, err := s.Apply(ctx, store.Batch{Message: &m}); err != nil {
		t.Fatal(err)
	}
	if got := text(t, s, "SELECT kind FROM messages"); got != "text" {
		t.Fatalf("kind = %q, want text", got)
	}
}

// A media row alone (no message in the batch) is accepted: the queue's tests and a rebuilt queue need it.
func TestApplyMediaOnlyBatchDefaultsSourceAndRank(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	for _, c := range []struct {
		id, source, wantSource string
		wantRank               int
	}{
		{"a", "", "live", 1}, {"b", "live", "live", 1}, {"c", "history", "history", 2}, {"d", "replay", "replay", 2},
	} {
		if _, err := s.Apply(ctx, store.Batch{Media: &store.Media{ChatJID: "g@g.us", MessageID: c.id, Source: c.source, State: "pending"}}); err != nil {
			t.Fatal(err)
		}
		got := text(t, s, "SELECT source||'|'||source_rank FROM media WHERE message_id=?", c.id)
		if want := c.wantSource + "|" + string(rune('0'+c.wantRank)); got != want {
			t.Errorf("media %s source|rank = %q, want %q", c.id, got, want)
		}
	}
}

func TestApplyKeepsAMediaSourceRankThatWasSet(t *testing.T) {
	s := open(t)
	if _, err := s.Apply(context.Background(), store.Batch{Media: &store.Media{ChatJID: "g@g.us", MessageID: "a", Source: "history", SourceRank: 0, State: "pending", Thumb: []byte{1, 2, 3}}}); err != nil {
		t.Fatal(err)
	}
	th, err := s.MediaThumb(context.Background(), "g@g.us", "a")
	if err != nil || string(th) != "\x01\x02\x03" {
		t.Fatalf("thumb = %v, %v", th, err)
	}
}

// Directory writes: an empty push name never erases a known one, and a write that changes nothing is skipped.
func TestApplyParticipantAndChatUpserts(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	apply := func(b store.Batch) {
		t.Helper()
		if _, err := s.Apply(ctx, b); err != nil {
			t.Fatal(err)
		}
	}
	apply(store.Batch{Sender: &store.Participant{JID: "1@lid", PushName: "Alice", UpdatedAt: 10}})
	apply(store.Batch{Sender: &store.Participant{JID: "1@lid", PushName: "Alice", UpdatedAt: 20}})
	if got := text(t, s, "SELECT push_name||'|'||updated_at FROM participants"); got != "Alice|10" {
		t.Errorf("unchanged name rewrote the row: %q", got)
	}
	apply(store.Batch{Sender: &store.Participant{JID: "1@lid", PushName: "", UpdatedAt: 30}})
	if got := text(t, s, "SELECT push_name||'|'||updated_at FROM participants"); got != "Alice|10" {
		t.Errorf("empty name changed the row: %q", got)
	}
	apply(store.Batch{Sender: &store.Participant{JID: "1@lid", PushName: "Alice B", UpdatedAt: 40}})
	if got := text(t, s, "SELECT push_name||'|'||updated_at FROM participants"); got != "Alice B|40" {
		t.Errorf("changed name = %q", got)
	}
	apply(store.Batch{Chat: &store.Chat{JID: "g@g.us", Name: "Family", Type: "group"}})
	apply(store.Batch{Chat: &store.Chat{JID: "g@g.us", Name: "", Type: "group"}})
	if got := text(t, s, "SELECT name FROM chats"); got != "Family" {
		t.Errorf("empty chat name erased the name: %q", got)
	}
}

// R2: the unchanged-write skip is real: an upsert that changes nothing commits no change to the row.
func TestApplyUnchangedDirectoryRowsAreNotRewritten(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	b := store.Batch{Chat: &store.Chat{JID: "g@g.us", Name: "Family", Type: "group"}, Sender: &store.Participant{JID: "1@lid", PushName: "Alice", UpdatedAt: 10}}
	if _, err := s.Apply(ctx, b); err != nil {
		t.Fatal(err)
	}
	db := readDB(t, s)
	var before int
	if err := db.QueryRow("PRAGMA data_version").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Apply(ctx, b); err != nil {
		t.Fatal(err)
	}
	var after int
	if err := db.QueryRow("PRAGMA data_version").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("a batch that changes nothing modified the database (data_version %d -> %d)", before, after)
	}
}

// R8 / schema 4.3 step 5: a new id mapping rewrites the chat_canonical rows of its component to the smallest LID.
func TestIDMappingMakesTheSmallestLIDCanonical(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	for _, chat := range []string{"9665@s.whatsapp.net", "2@lid"} {
		if _, err := s.Apply(ctx, store.Batch{Message: func() *store.Message { m := message(chat, "X"+chat, time.Unix(1, 0)); return &m }()}); err != nil {
			t.Fatal(err)
		}
	}
	canon := func(jid string) string {
		t.Helper()
		got, err := s.Canonical(ctx, jid)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if canon("9665@s.whatsapp.net") != "9665@s.whatsapp.net" {
		t.Fatal("an unmapped chat is its own canonical")
	}
	if _, err := s.InsertIDMapping(ctx, store.IDMapping{LID: "2@lid", PN: "9665@s.whatsapp.net", Source: "test", FirstSeen: time.Unix(2, 0)}); err != nil {
		t.Fatal(err)
	}
	if canon("9665@s.whatsapp.net") != "2@lid" || canon("2@lid") != "2@lid" {
		t.Fatalf("after the mapping: pn -> %s, lid -> %s; want 2@lid for both", canon("9665@s.whatsapp.net"), canon("2@lid"))
	}
	// A second, smaller LID for the same phone number takes over the whole component.
	if _, err := s.InsertIDMapping(ctx, store.IDMapping{LID: "1@lid", PN: "9665@s.whatsapp.net", Source: "test", FirstSeen: time.Unix(3, 0)}); err != nil {
		t.Fatal(err)
	}
	for _, jid := range []string{"9665@s.whatsapp.net", "2@lid", "1@lid"} {
		if got := canon(jid); got != "1@lid" {
			t.Errorf("canonical of %s = %s, want 1@lid", jid, got)
		}
	}
	// A larger LID later does not change the canonical.
	if _, err := s.InsertIDMapping(ctx, store.IDMapping{LID: "5@lid", PN: "9665@s.whatsapp.net", Source: "test", FirstSeen: time.Unix(4, 0)}); err != nil {
		t.Fatal(err)
	}
	if got := canon("5@lid"); got != "1@lid" {
		t.Errorf("canonical of 5@lid = %s, want 1@lid", got)
	}
	if n := count(t, s, "SELECT COUNT(*) FROM chat_canonical WHERE canonical='1@lid'"); n != 4 {
		t.Errorf("%d aliases under 1@lid, want 4", n)
	}
}

// Two components joined by one new mapping merge, even when the rows of both were already written.
func TestIDMappingMergesTwoComponents(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	for _, m := range []store.IDMapping{{LID: "1@lid", PN: "11@s.whatsapp.net"}, {LID: "3@lid", PN: "33@s.whatsapp.net"}} {
		if _, err := s.InsertIDMapping(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := s.Canonical(ctx, "33@s.whatsapp.net"); got != "3@lid" {
		t.Fatalf("before the merge 33 -> %s", got)
	}
	if _, err := s.InsertIDMapping(ctx, store.IDMapping{LID: "3@lid", PN: "11@s.whatsapp.net"}); err != nil {
		t.Fatal(err)
	}
	for _, jid := range []string{"1@lid", "3@lid", "11@s.whatsapp.net", "33@s.whatsapp.net"} {
		if got, _ := s.Canonical(ctx, jid); got != "1@lid" {
			t.Errorf("canonical of %s = %s, want 1@lid", jid, got)
		}
	}
}

// A repeated mapping reports not new and changes nothing; a mapping that fails to rewrite the canonical rows is not kept.
func TestIDMappingIsAtomicWithItsCanonicalRows(t *testing.T) {
	s := open(t)
	failInserts(t, s, "chat_canonical", "SELECT RAISE(ABORT, 'rejected')")
	_, err := s.InsertIDMapping(context.Background(), store.IDMapping{LID: "1@lid", PN: "1@s.whatsapp.net", Source: "test"})
	if !errors.Is(err, store.ErrConstraint) {
		t.Fatalf("err = %v, want a constraint error", err)
	}
	if n := count(t, s, "SELECT COUNT(*) FROM id_mappings"); n != 0 {
		t.Fatalf("the mapping was kept without its canonical rows (%d rows)", n)
	}
}

// Rules and sightings for a JID the archive has not stored yet still need a canonical row, so reads that join on
// chat_canonical (and the exclusion filter's alias lookup) find them.
func TestRulesAndSightingsEnsureCanonicalRows(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if err := s.SetChatRules(ctx, []store.ChatRule{{JID: "5@s.whatsapp.net", Logging: false}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkChatSeen(ctx, "6@g.us", 1); err != nil {
		t.Fatal(err)
	}
	for _, jid := range []string{"5@s.whatsapp.net", "6@g.us"} {
		if n := count(t, s, "SELECT COUNT(*) FROM chat_canonical WHERE alias=? AND canonical=?", jid, jid); n != 1 {
			t.Errorf("no canonical row for %s", jid)
		}
	}
}

func TestCanonicalOfAnUnknownJIDIsItself(t *testing.T) {
	s := open(t)
	if got, err := s.Canonical(context.Background(), "nobody@lid"); err != nil || got != "nobody@lid" {
		t.Fatalf("Canonical = %q, %v", got, err)
	}
}
