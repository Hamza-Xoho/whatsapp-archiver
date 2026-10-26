package store

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"testing"
	"time"
)

// seedScaleArchive writes n messages over 250 chats straight into the raw tables with recursive CTEs (the way
// rebuild_bench_test.go does) and then derives chat_canonical and chat_stats. Every tenth chat has a LID form: a third
// of its messages are stored under the LID, and every fiftieth is stored under both forms (a duplicate id that only the
// canonical collapses). One message in twenty has a media row in some state, 60 gaps are open, and a handful of closed
// ones have a message with the target id.
func seedScaleArchive(t *testing.T, n int) *Store {
	t.Helper()
	s := openInternal(t)
	ctx := context.Background()
	started := time.Now()
	if _, err := s.write.ExecContext(ctx, "PRAGMA synchronous=OFF"); err != nil {
		t.Fatal(err)
	}
	const forms = `f(i,cid,alias,lidform) AS (SELECT i, i%250, (i%250)%10=0, ((i%250)%10=0 AND i%3=0) FROM n)`
	statements := []struct {
		query string
		args  []any
	}{
		{`WITH RECURSIVE n(i) AS (SELECT 0 UNION ALL SELECT i+1 FROM n WHERE i<?1-1), ` + forms + `
INSERT INTO messages(chat_jid,id,kind,text,ts,received_at,source,raw_proto)
SELECT CASE WHEN lidform THEN (5000+cid)||'@lid' ELSE (1000+cid)||'@s.whatsapp.net' END,'M'||i,'text','message number '||i||' hello world',
 1000000+i,i,CASE WHEN i%2=0 THEN 'live' ELSE 'history' END,x'78' FROM f
UNION ALL
SELECT CASE WHEN lidform THEN (1000+cid)||'@s.whatsapp.net' ELSE (5000+cid)||'@lid' END,'M'||i,'text','message number '||i||' hello world',
 1000000+i,i+1,CASE WHEN i%2=0 THEN 'live' ELSE 'history' END,x'78' FROM f WHERE alias AND i%50=0`, []any{n}},
		{`WITH RECURSIVE c(i) AS (SELECT 0 UNION ALL SELECT i+1 FROM c WHERE i<249)
INSERT INTO chats(jid,name,type) SELECT (1000+i)||'@s.whatsapp.net',CASE WHEN i%4=3 THEN '' ELSE 'Chat '||i END,'individual' FROM c`, nil},
		{`WITH RECURSIVE c(i) AS (SELECT 3 UNION ALL SELECT i+4 FROM c WHERE i<249)
INSERT INTO participants(jid,push_name,contact_name,updated_at) SELECT (1000+i)||'@s.whatsapp.net','Push '||i,CASE WHEN i%8=7 THEN 'Contact '||i ELSE '' END,1 FROM c`, nil},
		{`WITH RECURSIVE c(i) AS (SELECT 0 UNION ALL SELECT i+10 FROM c WHERE i<249)
INSERT INTO id_mappings(lid,pn,source,first_seen) SELECT (5000+i)||'@lid',(1000+i)||'@s.whatsapp.net','scale',1 FROM c`, nil},
		{`INSERT INTO media(chat_jid,message_id,sender_jid,source,source_rank,type,declared_size,message_ts,state,updated_at)
SELECT chat_jid,id,'s@lid',source,2,'image',100,ts,
 CASE seq%7 WHEN 0 THEN 'done' WHEN 1 THEN 'failed' WHEN 2 THEN 'expired' WHEN 3 THEN 'retry_requested' WHEN 4 THEN 'pending' WHEN 5 THEN 'downloading' ELSE 'skipped' END,1
FROM messages WHERE seq%20=0`, nil},
		{`INSERT INTO message_events(chat_jid,target_id,kind,event_id,ts,received_at) SELECT chat_jid,'gone'||seq,'undecryptable','u'||seq,1,1 FROM messages WHERE seq%5000=0`, nil},
		{`INSERT INTO message_events(chat_jid,target_id,kind,event_id,ts,received_at) SELECT chat_jid,id,'undecryptable','c'||seq,1,1 FROM messages WHERE seq%7000=0`, nil},
		{`INSERT INTO history_sync_chunks(id,stage,sync_type,chunk_order,progress,received_at) VALUES ('a','imported','FULL',0,50,100),('b','imported','FULL',1,100,101)`, nil},
	}
	for _, st := range statements {
		if _, err := s.write.ExecContext(ctx, st.query, st.args...); err != nil {
			t.Fatalf("seed %.60s: %v", st.query, err)
		}
	}
	t.Logf("seeded %d messages in %v", n, time.Since(started).Round(time.Millisecond))
	// RebuildDerived also builds both FTS tables, which takes over five minutes at 300k messages under -race (8.7 s
	// without it) and is not what these tests read. The two tables they do read are filled by the same code RebuildDerived
	// runs for them: rebuildCanonical and statsSelect(false), in one transaction.
	begin := time.Now()
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = rebuildCanonical(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, statsSelect(false)); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	t.Logf("derived chat_canonical and chat_stats in %v", time.Since(begin).Round(time.Millisecond))
	return s
}

// The status report and the chat list read O(chats) at 300k messages (plan W2(f), schema 4.4). The plan's target for the
// status report is 100 ms; the test fails only over 250 ms so a loaded CI machine does not make it flaky, and logs the
// number. The chat list is a whole screen: its 95th percentile over 20 calls must stay under 150 ms.
func TestStatusReportAndChatListAtScale(t *testing.T) {
	if testing.Short() {
		t.Skip("-short: make check-fast skips this test; make test (so make check) runs it twice: 50k messages under -race, and 300k messages without -race")
	}
	s := seedScaleArchive(t, scaleMessages)
	ctx := context.Background()

	t.Run("status report", func(t *testing.T) {
		var took []time.Duration
		for i := 0; i < 7; i++ {
			begin := time.Now()
			r, err := s.StatusReport(ctx)
			took = append(took, time.Since(begin))
			if err != nil {
				t.Fatal(err)
			}
			if i > 0 {
				continue
			}
			messages, gaps := 0, 0
			for _, c := range r.Chats {
				messages += c.MessageCount
				gaps += c.OpenGaps
			}
			if len(r.Chats) != 250 || messages != scaleMessages || gaps == 0 {
				t.Fatalf("report has %d chats, %d messages, %d gaps; want 250 chats and %d messages (duplicates collapse) and some gaps", len(r.Chats), messages, gaps, scaleMessages)
			}
			if r.Coverage.Chunks != 2 || !r.Coverage.Complete || len(r.Coverage.OldestByChat) != 125 { // history messages are the odd ones, which fall in the 125 odd chats
				t.Fatalf("coverage = %+v", r.Coverage)
			}
		}
		slices.Sort(took)
		median := took[len(took)/2]
		t.Logf("StatusReport at %d messages: median %v, min %v, max %v (plan target 100 ms)", scaleMessages, median, took[0], took[len(took)-1])
		if median > 250*time.Millisecond {
			t.Fatalf("StatusReport median %v, want well under 100 ms (fail over 250 ms)", median)
		}
	})

	t.Run("chat list", func(t *testing.T) {
		var took []time.Duration
		for i := 0; i < 20; i++ {
			begin := time.Now()
			rows, err := s.ChatList(ctx, "")
			took = append(took, time.Since(begin))
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 250 {
				t.Fatalf("chat list has %d rows, want 250", len(rows))
			}
		}
		slices.Sort(took)
		p95 := took[18]
		t.Logf("ChatList at %d messages: p95 %v, median %v, max %v (limit 150 ms)", scaleMessages, p95, took[10], took[19])
		if p95 > 150*time.Millisecond {
			t.Fatalf("ChatList p95 %v, want under 150 ms", p95)
		}
	})
}

// tablesRead lists the tables whose b-trees (tables or their indexes) a query opens, from the bytecode of EXPLAIN: the
// root pages of its OpenRead and ReopenIdx instructions looked up in sqlite_master. It cannot be fooled by the aliases
// a plan line uses for a table.
func tablesRead(t *testing.T, s *Store, query string) []string {
	t.Helper()
	ctx := context.Background()
	owner := map[int64]string{}
	rows, err := s.read.QueryContext(ctx, `SELECT tbl_name,rootpage FROM sqlite_master WHERE rootpage>0`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name string
		var root int64
		if err = rows.Scan(&name, &root); err != nil {
			t.Fatal(err)
		}
		owner[root] = name
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	_ = rows.Close()
	prog, err := s.read.QueryContext(ctx, "EXPLAIN "+query)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = prog.Close() }()
	seen := map[string]bool{}
	for prog.Next() {
		var addr, p1, p2, p3 int64
		var opcode string
		var p4, p5, comment sql.NullString
		if err = prog.Scan(&addr, &opcode, &p1, &p2, &p3, &p4, &p5, &comment); err != nil {
			t.Fatal(err)
		}
		if opcode == "OpenRead" || opcode == "ReopenIdx" {
			if name, ok := owner[p2]; ok {
				seen[name] = true
			} else {
				t.Fatalf("%s opens root page %d, which is no table of the archive", opcode, p2)
			}
		}
	}
	if err = prog.Err(); err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

// StatusReport reads chat_stats and the history ledger and nothing else (plan W2(f)): the queries behind it open no
// b-tree of messages, media or message_events (or the open_gaps view over them), whatever the size of the archive.
func TestStatusReportTouchesOnlyDerivedTablesAndLedger(t *testing.T) {
	s := seedStatsArchive(t, 12, 600)
	for name, query := range map[string]string{"chats": statusReportQuery, "ledger": historyChunksQuery} {
		got := tablesRead(t, s, query)
		for _, table := range got {
			if table != "chat_stats" && table != "history_sync_chunks" {
				t.Errorf("the %s query of StatusReport reads %s (all: %v); only chat_stats and history_sync_chunks may be read", name, table, got)
			}
		}
		if len(got) == 0 {
			t.Errorf("the %s query reads no table at all", name)
		}
		plan, err := s.read.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+query)
		if err != nil {
			t.Fatal(err)
		}
		for plan.Next() {
			var id, parent, unused int
			var detail string
			if err = plan.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			for _, raw := range []string{"messages", "media", "message_events"} {
				if strings.HasPrefix(detail, "SCAN "+raw) {
					t.Errorf("the %s query scans %s: %s", name, raw, detail)
				}
			}
		}
		_ = plan.Close()
	}
}
