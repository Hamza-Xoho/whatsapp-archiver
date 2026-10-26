package store

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// legacyChatStatsQuery is ChatStats as it was before the status-cost fix, copied here as the oracle that the rewritten
// query must agree with row for row. It rescans media, open gaps and the alias view once per chat.
const legacyChatStatsQuery = legacyViews + `SELECT m.canonical_jid,COALESCE(MAX(c.name),''),COUNT(*),MIN(m.ts),MAX(m.ts),
 (SELECT COUNT(*) FROM media x LEFT JOIN chat_aliases a ON a.alias_jid=x.chat_jid WHERE COALESCE(a.canonical_jid,x.chat_jid)=m.canonical_jid AND x.state IN ('failed')),
 (SELECT COUNT(*) FROM media x LEFT JOIN chat_aliases a ON a.alias_jid=x.chat_jid WHERE COALESCE(a.canonical_jid,x.chat_jid)=m.canonical_jid AND x.state IN ('expired','retry_requested')),
 (SELECT COUNT(*) FROM media x LEFT JOIN chat_aliases a ON a.alias_jid=x.chat_jid WHERE COALESCE(a.canonical_jid,x.chat_jid)=m.canonical_jid AND x.state IN ('pending','downloading')),
 (SELECT COUNT(*) FROM open_gaps g LEFT JOIN chat_aliases a ON a.alias_jid=g.chat_jid WHERE COALESCE(a.canonical_jid,g.chat_jid)=m.canonical_jid)
 FROM messages_resolved m LEFT JOIN chats c ON c.jid=m.chat_jid GROUP BY m.canonical_jid ORDER BY m.canonical_jid`

// legacyViews recreates the dropped chat_aliases and messages_resolved views (ADR-35) as CTEs that build the aliases
// from chat_canonical, so the old queries can still serve as oracles for the counting, the duplicate rank and the
// columns. It does not check that chat_canonical itself is right: the mapping tests in apply_test.go cover that.
const legacyViews = `WITH chat_aliases AS (SELECT alias AS alias_jid, canonical AS canonical_jid FROM chat_canonical),
messages_resolved AS (SELECT * FROM (
  SELECT m.*, COALESCE(a.canonical_jid, m.chat_jid) AS canonical_jid,
         ROW_NUMBER() OVER (PARTITION BY COALESCE(a.canonical_jid, m.chat_jid), m.id ORDER BY m.received_at, m.chat_jid) AS duplicate_rank
  FROM messages m LEFT JOIN chat_aliases a ON a.alias_jid = m.chat_jid
) WHERE duplicate_rank = 1)
`

// seedStatsArchive writes an archive of msgs messages over chats chats through the store's write methods. Every tenth
// chat also has a LID: some messages arrive under either alias, and the alias is learned after the messages, so the
// same message is stored twice and only the view collapses it. Media rows cover every state and some undecryptable
// events have no target (open gaps).
func seedStatsArchive(t *testing.T, chats, msgs int) *Store {
	t.Helper()
	s := openInternal(t)
	ctx := context.Background()
	if _, err := s.write.ExecContext(ctx, "PRAGMA synchronous=OFF"); err != nil {
		t.Fatal(err)
	}
	pn := func(c int) string { return fmt.Sprintf("%d@s.whatsapp.net", 1000+c) }
	lid := func(c int) string { return fmt.Sprintf("%d@lid", 5000+c) }
	for c := 0; c < chats; c++ {
		name := ""
		if c%4 != 3 {
			name = fmt.Sprintf("Chat %d", c)
		}
		if err := s.UpsertChat(ctx, Chat{JID: pn(c), Name: name, Type: "individual"}); err != nil {
			t.Fatal(err)
		}
	}
	type ref struct{ chat, id string }
	refs := make([]ref, 0, msgs)
	raw := []byte("x")
	for n := 0; n < msgs; n++ {
		c := n % chats
		chat := pn(c)
		if c%10 == 0 && n%3 == 0 {
			chat = lid(c)
		}
		id := fmt.Sprintf("m%d", n)
		source := "live"
		if n%2 == 1 {
			source = "history"
		}
		ok, err := s.InsertMessage(ctx, Message{ChatJID: chat, ID: id, SenderJID: "s@lid", Source: source, Timestamp: int64(n), ReceivedAt: int64(n), RawProto: raw})
		if err != nil || !ok {
			t.Fatalf("message %d: ok=%v err=%v", n, ok, err)
		}
		refs = append(refs, ref{chat, id})
		if c%10 == 0 { // the other alias, before the alias is known: stored as a second row
			other := lid(c)
			if chat == other {
				other = pn(c)
			}
			if _, err = s.InsertMessage(ctx, Message{ChatJID: other, ID: id, SenderJID: "s@lid", Source: source, Timestamp: int64(n), ReceivedAt: int64(n) + 1, RawProto: raw}); err != nil {
				t.Fatal(err)
			}
		}
	}
	for c := 0; c < chats; c += 10 {
		if _, err := s.InsertIDMapping(ctx, IDMapping{LID: lid(c), PN: pn(c), Source: "test", FirstSeen: time.Unix(1, 0)}); err != nil {
			t.Fatal(err)
		}
	}
	states := []string{"done", "failed", "expired", "retry_requested", "pending", "downloading", "skipped"}
	for n := 0; n < msgs/5; n++ {
		r := refs[n*5]
		if _, err := s.Apply(ctx, Batch{Media: &Media{ChatJID: r.chat, MessageID: r.id, SenderJID: "s@lid", Source: "live", Type: "image", MIME: "image/jpeg", DeclaredSize: 10, Path: "p", State: states[n%len(states)], Attempts: 1, UpdatedAt: 1, MessageTimestamp: int64(n * 5)}}); err != nil {
			t.Fatal(err)
		}
	}
	for n := 0; n < msgs; n += 50 { // closed: the target exists
		r := refs[n]
		if _, err := s.InsertEvent(ctx, MessageEvent{ChatJID: r.chat, TargetID: r.id, Kind: "undecryptable", EventID: fmt.Sprintf("u%d", n), SenderJID: "s@lid", Timestamp: 1, ReceivedAt: 1}); err != nil {
			t.Fatal(err)
		}
	}
	for n := 0; n < msgs; n += 70 { // open: no message has this target
		c := n % chats
		chat := pn(c)
		if c%10 == 0 && n%2 == 0 {
			chat = lid(c)
		}
		if _, err := s.InsertEvent(ctx, MessageEvent{ChatJID: chat, TargetID: fmt.Sprintf("gone%d", n), Kind: "undecryptable", EventID: fmt.Sprintf("g%d", n), SenderJID: "s@lid", Timestamp: 1, ReceivedAt: 1}); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// TestChatStatsMatchesPreviousQuery pins the rows of ChatStats to the previous query on an archive that uses every
// column: aliases, duplicates seen before the alias, media in every state and open gaps.
func TestChatStatsMatchesPreviousQuery(t *testing.T) {
	const chats = 12
	s := seedStatsArchive(t, chats, 600)
	ctx := context.Background()
	got, err := s.ChatStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.read.QueryContext(ctx, legacyChatStatsQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var want []ChatStat
	for rows.Next() {
		var v ChatStat
		if err = rows.Scan(&v.CanonicalJID, &v.Name, &v.MessageCount, &v.Oldest, &v.Newest, &v.MediaFailed, &v.MediaExpired, &v.MediaPending, &v.OpenGaps); err != nil {
			t.Fatal(err)
		}
		want = append(want, v)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ChatStats differs from the previous query:\n got %+v\nwant %+v", got, want)
	}
	var failed, expired, pending, gaps, unnamed int
	for _, v := range got {
		failed += v.MediaFailed
		expired += v.MediaExpired
		pending += v.MediaPending
		gaps += v.OpenGaps
		if v.Name == "" {
			unnamed++
		}
	}
	if len(got) != chats || failed == 0 || expired == 0 || pending == 0 || gaps == 0 || unnamed == 0 {
		t.Fatalf("fixture does not exercise every column: chats=%d failed=%d expired=%d pending=%d gaps=%d unnamed=%d", len(got), failed, expired, pending, gaps, unnamed)
	}
}

// A subquery that is a direct child of the query plan (parent 0 in EXPLAIN QUERY PLAN) is evaluated once per output
// row, here once per chat. Each correlated subquery of ChatStats rescans the media table, the open gaps or the alias
// view for every chat, so `archiver status` cost grows with chats times archive size. The query must compute each
// aggregate in one pass and join the results per chat.
func TestChatStatsDoesNotRescanPerChat(t *testing.T) {
	s := seedStatsArchive(t, 12, 600)
	rows, err := s.read.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+chatStatsQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var plan, perChat []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
		if parent == 0 && strings.HasPrefix(detail, "CORRELATED SCALAR SUBQUERY") {
			perChat = append(perChat, detail)
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(perChat) > 0 {
		t.Fatalf("ChatStats evaluates %d subqueries once per chat (%s); plan:\n%s", len(perChat), strings.Join(perChat, ", "), strings.Join(plan, "\n"))
	}
}
