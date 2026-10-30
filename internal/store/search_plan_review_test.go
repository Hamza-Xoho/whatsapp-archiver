package store

import (
	"strings"
	"testing"
)

// A common query must walk messages newest-first and stop at the page limit, probing FTS by stable rowid. Sorting the
// complete FTS hit set misses the 300 ms budget at 300k messages.
func TestSearchPlanUsesGlobalMessageOrderAndCorrelatedFTSProbe(t *testing.T) {
	s := openInternal(t)
	rows, err := s.read.QueryContext(t.Context(), `EXPLAIN QUERY PLAN
SELECT m.seq FROM messages m INDEXED BY messages_global_ts
WHERE EXISTS(SELECT 1 FROM search_tri f WHERE f.rowid=m.seq AND search_tri MATCH '"archive"')
ORDER BY m.ts DESC,m.seq DESC LIMIT 50`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var plan strings.Builder
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(detail)
		plan.WriteByte('\n')
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	got := plan.String()
	if !strings.Contains(got, "messages_global_ts") || strings.Contains(got, "USE TEMP B-TREE FOR ORDER BY") {
		t.Fatalf("search plan must scan the global time index without a sort:\n%s", got)
	}
	if !strings.Contains(got, "VIRTUAL TABLE INDEX 0:=M1") {
		t.Fatalf("search plan must probe the FTS table by rowid and MATCH:\n%s", got)
	}
}
