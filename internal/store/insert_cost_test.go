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

	const total, window = 20000, 1000
	var first, last time.Duration
	start := time.Now()
	for i := 0; i < total; i++ {
		if i == total-window {
			start = time.Now()
		}
		m := Message{ChatJID: fmt.Sprintf("chat%d@g.us", i%50), ID: fmt.Sprintf("M%07d", i), Source: "history", Timestamp: int64(i), ReceivedAt: 1, RawProto: []byte("x")}
		if ok, err := s.InsertMessage(ctx, m); err != nil || !ok {
			t.Fatalf("insert %d: ok=%v err=%v", i, ok, err)
		}
		if i == window-1 {
			first = time.Since(start)
			start = time.Now()
		}
	}
	last = time.Since(start)
	t.Logf("first %d inserts %v, last %d inserts %v", window, first, window, last)
	if last > 4*first {
		t.Fatalf("insert cost grew with table size: first %d took %v, last %d took %v", window, first, window, last)
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
