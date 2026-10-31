package store

import (
	"strings"
	"testing"
)

func TestExportSnapshotStartupIsThreeBoundedQueriesWithoutMediaScan(t *testing.T) {
	s := openInternal(t)
	ctx := t.Context()
	message := Message{ChatJID: "plan@g.us", ID: "one", Kind: "text", Source: "live", Timestamp: 1, ReceivedAt: 1, RawProto: []byte("x")}
	if _, err := s.InsertMessage(ctx, message); err != nil {
		t.Fatal(err)
	}
	snapshot, queries, err := s.newExportSnapshot(ctx, message.ChatJID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = snapshot.Close() }()
	if queries != 3 {
		t.Fatalf("export snapshot startup queries = %d, want exactly 3 (known, aliases, indexed bounds)", queries)
	}
	plan := planOf(t, s, exportSnapshotBoundsQuery("?1"), message.ChatJID)
	if strings.Contains(strings.ToLower(plan), "media") {
		t.Fatalf("export snapshot startup touches media:\n%s", plan)
	}
}
