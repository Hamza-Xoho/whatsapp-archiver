package store

import (
	"database/sql"
	"strings"
	"testing"
)

func TestExportSnapshotBoundsPlanHasNoArchiveWideDistinctAggregate(t *testing.T) {
	s := openInternal(t)
	ctx := t.Context()
	message := Message{ChatJID: "bounded@g.us", ID: "one", Kind: "text", Source: "live", Timestamp: 1, ReceivedAt: 1, RawProto: []byte("x")}
	if _, err := s.InsertMessage(ctx, message); err != nil {
		t.Fatal(err)
	}

	query := exportSnapshotBoundsQuery("?1")
	if strings.Contains(strings.ToUpper(query), "MAX(") {
		t.Fatalf("snapshot bounds query uses MAX, whose plan can become an unindexed archive scan:\n%s", query)
	}
	plan := strings.ToLower(planOf(t, s, query, message.ChatJID))
	if strings.Contains(plan, "use temp b-tree") {
		t.Fatalf("snapshot startup builds archive-sized temporary state:\n%s", plan)
	}

	rows, err := s.read.QueryContext(ctx, "EXPLAIN "+query, message.ChatJID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	lastSeeks := 0
	for rows.Next() {
		var address, p1, p2, p3, p5 int
		var opcode string
		var p4, comment sql.NullString
		if err = rows.Scan(&address, &opcode, &p1, &p2, &p3, &p4, &p5, &comment); err != nil {
			t.Fatal(err)
		}
		if opcode == "Last" {
			lastSeeks++
		}
		if opcode == "OpenEphemeral" || opcode == "SorterOpen" || opcode == "Sort" || opcode == "Rewind" || opcode == "Next" || opcode == "AggStep" {
			t.Fatalf("snapshot startup opcode %d %s %q permits archive-row iteration", address, opcode, p4.String)
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if lastSeeks != 3 {
		t.Fatalf("snapshot startup has %d primary-key tail seeks, want exactly 3", lastSeeks)
	}
}
