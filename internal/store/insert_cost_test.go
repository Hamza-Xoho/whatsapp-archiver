package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func openInternal(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "archive.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// The per-insert duplicate check must be answered from primary-key and index lookups: no scan of messages and no
// materialised view, whatever the size of the table.
func TestInsertMessageCostDoesNotGrowWithTableSize(t *testing.T) {
	s := openInternal(t)
	ctx := context.Background()
	rows, err := s.write.QueryContext(ctx, "EXPLAIN QUERY PLAN "+dedupeQuery, "a@g.us", "ID")
	if err != nil {
		t.Fatal(err)
	}
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	_ = rows.Close()
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	all := strings.Join(plan, "\n")
	if strings.Contains(all, "SCAN messages") || strings.Contains(all, "MATERIALIZE") {
		t.Fatalf("dedupe query plan scans or materialises:\n%s", all)
	}

	// Time n inserts into an empty archive, then n more after bulk-seeding many rows: cost must not depend on size.
	const seed, n = 20000, 100
	insertN := func(prefix string) time.Duration {
		start := time.Now()
		for i := 0; i < n; i++ {
			m := Message{ChatJID: fmt.Sprintf("chat%d@g.us", i%50), ID: fmt.Sprintf("%s%07d", prefix, i), Source: "history", Timestamp: int64(i), ReceivedAt: 1, RawProto: []byte("x")}
			if ok, err := s.InsertMessage(ctx, m); err != nil || !ok {
				t.Fatalf("insert %s%d: ok=%v err=%v", prefix, i, ok, err)
			}
		}
		return time.Since(start)
	}
	small := insertN("A")
	if _, err = s.write.ExecContext(ctx, `WITH RECURSIVE c(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM c WHERE i<?)
INSERT INTO messages(chat_jid,id,ts,received_at,source,raw_proto) SELECT 'seed' || (i%50) || '@g.us','S' || i,i,1,'history',x'78' FROM c`, seed); err != nil {
		t.Fatal(err)
	}
	large := insertN("B")
	t.Logf("%d inserts into an empty archive %v, into %d rows %v", n, small, seed, large)
	if large > 4*small {
		t.Fatalf("insert cost grew with table size: %d inserts took %v when empty, %v with %d rows", n, small, large, seed)
	}
}

func TestInsertMessageDuplicateAcrossSiblingPhoneNumbers(t *testing.T) {
	s := openInternal(t)
	ctx := context.Background()
	msg := func(chat string) Message {
		return Message{ChatJID: chat, ID: "X", Source: "live", Timestamp: 1, ReceivedAt: 1, RawProto: []byte("x")}
	}
	if ok, err := s.InsertMessage(ctx, msg("111@s.whatsapp.net")); err != nil || !ok {
		t.Fatalf("seed: ok=%v err=%v", ok, err)
	}
	for _, pn := range []string{"111@s.whatsapp.net", "222@s.whatsapp.net"} {
		if _, err := s.InsertIDMapping(ctx, IDMapping{LID: "9@lid", PN: pn, Source: "test"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, chat := range []string{"222@s.whatsapp.net", "9@lid"} {
		if ok, err := s.InsertMessage(ctx, msg(chat)); err != nil || ok {
			t.Fatalf("%s: inserted=%v err=%v", chat, ok, err)
		}
	}
}
