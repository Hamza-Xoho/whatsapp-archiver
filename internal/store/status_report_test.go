package store

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

// legacyOldestHistoryQuery is the second messages_resolved query of HistoryCoverage before the one-pass report.
const legacyOldestHistoryQuery = legacyViews + `SELECT canonical_jid,MIN(ts) FROM messages_resolved WHERE source='history' GROUP BY canonical_jid`

// TestStatusReportScansMessagesOnce pins the cost of `archiver status`: the query behind StatusReport reads the
// messages table once. The message_keys CTE of ChatStats and the oldest-history query of HistoryCoverage each used
// to read it again, and each evaluated the window function of messages_resolved on its own.
func TestStatusReportScansMessagesOnce(t *testing.T) {
	s := seedStatsArchive(t, 12, 600)
	rows, err := s.read.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+statusReportQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var plan []string
	scans := 0
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
		// In the plan the view's FROM messages m shows as "SCAN m"; no other table of the report uses that alias.
		if detail == "SCAN m" || strings.HasPrefix(detail, "SCAN m USING") {
			scans++
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	// W2(f): the report reads chat_stats and scans messages no time at all (TestStatusReportTouchesOnlyDerivedTablesAndLedger
	// pins zero); this older bound stays as the upper limit the query must never exceed.
	if scans > 1 {
		t.Fatalf("report scans messages %d times, want at most 1; plan:\n%s", scans, strings.Join(plan, "\n"))
	}
}

// TestStatusReportEqualsSeparateQueries compares StatusReport with the two queries it replaced, each run as it was
// written before: the per-chat rows with legacyChatStatsQuery and the oldest history message per chat with
// legacyOldestHistoryQuery. The archive adds three cases to the seeded one: a chat whose first copy is live and whose
// later alias copy is history (the view keeps the live copy, so the chat has no history), a chat with only live
// messages, and a chat whose first copy is history and later alias copy is live.
func TestStatusReportEqualsSeparateQueries(t *testing.T) {
	s := seedStatsArchive(t, 12, 600)
	ctx := context.Background()
	add := func(chat, id, source string, ts, at int64) {
		t.Helper()
		if _, err := s.InsertMessage(ctx, Message{ChatJID: chat, ID: id, SenderJID: "s@lid", Source: source, Timestamp: ts, ReceivedAt: at, RawProto: []byte("x")}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.UpsertChat(ctx, Chat{JID: "9001@s.whatsapp.net", Name: "Alias chat", Type: "individual"}); err != nil {
		t.Fatal(err)
	}
	add("9001@s.whatsapp.net", "a1", "live", 5000, 10) // first copy is live
	add("9001@lid", "a1", "history", 4000, 20)         // later alias copy is history: not counted as history
	add("9001@lid", "a2", "history", 4500, 30)         // first copy is history
	add("9001@s.whatsapp.net", "a2", "live", 4500, 31) // later alias copy is live
	// the alias is learned after the messages, as in production, so both copies are stored
	if _, err := s.InsertIDMapping(ctx, IDMapping{LID: "9001@lid", PN: "9001@s.whatsapp.net", Source: "test", FirstSeen: time.Unix(1, 0)}); err != nil {
		t.Fatal(err)
	}
	add("9002@s.whatsapp.net", "l1", "live", 6000, 40) // a chat with only live messages
	add("9002@s.whatsapp.net", "l2", "live", 6001, 41)
	for i, p := range []int{10, 100, 57} {
		if err := s.InsertHistoryChunk(ctx, HistoryChunk{ID: string(rune('a' + i)), SyncType: "FULL", Progress: p, ReceivedAt: int64(100 + i)}); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.StatusReport(ctx)
	if err != nil {
		t.Fatal(err)
	}

	rows, err := s.read.QueryContext(ctx, legacyChatStatsQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var wantChats []ChatStat
	for rows.Next() {
		var v ChatStat
		if err = rows.Scan(&v.CanonicalJID, &v.Name, &v.MessageCount, &v.Oldest, &v.Newest, &v.MediaFailed, &v.MediaExpired, &v.MediaPending, &v.OpenGaps); err != nil {
			t.Fatal(err)
		}
		wantChats = append(wantChats, v)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	// W2(f), accepted in W2(d): chat_stats times are the min and max over all message rows of the canonical, where the
	// previous query took them over one row per duplicated id (the first received). Only a duplicate whose two copies
	// carry different ts shows the difference, and this archive has one: a1 is 5000 (live) and 4000 (history copy), so
	// the oldest message of 9001@lid is 4000, not 4500.
	for i := range wantChats {
		if wantChats[i].CanonicalJID == "9001@lid" {
			if wantChats[i].Oldest != 4500 {
				t.Fatalf("fixture changed: previous oldest of 9001@lid = %d, want 4500", wantChats[i].Oldest)
			}
			wantChats[i].Oldest = 4000
		}
	}
	if !reflect.DeepEqual(got.Chats, wantChats) {
		t.Fatalf("Chats differ from the previous query:\n got %+v\nwant %+v", got.Chats, wantChats)
	}

	hrows, err := s.read.QueryContext(ctx, legacyOldestHistoryQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hrows.Close() }()
	wantOldest := map[string]int64{}
	for hrows.Next() {
		var jid string
		var ts int64
		if err = hrows.Scan(&jid, &ts); err != nil {
			t.Fatal(err)
		}
		wantOldest[jid] = ts
	}
	if err = hrows.Err(); err != nil {
		t.Fatal(err)
	}
	// Same accepted divergence for the oldest history message: chat_stats takes the oldest history row of any copy, so a1's
	// history copy (4000, the live copy came first) now counts for 9001@lid.
	wantNow := map[string]int64{}
	for k, v := range wantOldest {
		wantNow[k] = v
	}
	wantNow["9001@lid"] = 4000
	if !reflect.DeepEqual(got.Coverage.OldestByChat, wantNow) {
		t.Fatalf("OldestByChat differs from the previous query (9001@lid aside):\n got %v\nwant %v", got.Coverage.OldestByChat, wantNow)
	}
	if _, ok := wantOldest["9002@s.whatsapp.net"]; ok {
		t.Fatal("a chat with only live messages has an oldest history entry")
	}
	if ts, ok := wantOldest["9001@lid"]; !ok || ts != 4500 {
		t.Fatalf("alias chat (canonical 9001@lid) oldest history = %d, %v; want 4500 from the history-first copy only", ts, ok)
	}
	if got.Coverage.Chunks != 3 || got.Coverage.LastProgress != 57 || got.Coverage.LastChunkTime != 102 {
		t.Fatalf("coverage chunk fields = %+v", got.Coverage)
	}
}
