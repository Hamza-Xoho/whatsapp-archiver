package store

import (
	"context"
	"strings"
	"testing"
)

func planOf(t *testing.T, s *Store, query string, args ...any) string {
	t.Helper()
	rows, err := s.read.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(plan, "\n")
}

// Root cause 5 / schema 4.4: without statistics SQLite picks media_due plus a temp b-tree sort of every due row. The
// queue queries are written INDEXED BY media_pick, and this pins that the plan has no sort and reads media_pick.
func TestMediaQueueQueriesUseMediaPickWithoutASort(t *testing.T) {
	s := openInternal(t)
	for name, q := range map[string]struct {
		query string
		args  []any
	}{
		"claim":        {claimMediaQuery, []any{int64(1000)}},
		"next expired": {nextExpiredQuery, nil},
	} {
		plan := planOf(t, s, q.query, q.args...)
		if strings.Contains(plan, "TEMP B-TREE") {
			t.Errorf("%s plans a sort:\n%s", name, plan)
		}
		if !strings.Contains(plan, "media_pick") {
			t.Errorf("%s does not use media_pick:\n%s", name, plan)
		}
	}
}

// Both queries stay on the narrow index: no blob is read to order or filter the rows (the raw proto comes from a
// primary-key lookup of the one claimed message).
func TestClaimMediaDoesNotScanMessages(t *testing.T) {
	s := openInternal(t)
	plan := planOf(t, s, claimMediaQuery, int64(1000))
	if strings.Contains(plan, "SCAN messages") || strings.Contains(plan, "SCAN m") {
		t.Fatalf("claim scans messages:\n%s", plan)
	}
}
