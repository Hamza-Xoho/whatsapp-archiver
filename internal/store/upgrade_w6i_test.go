package store

import (
	"path/filepath"
	"testing"
)

func TestOpenRepairsInterruptedMessageRowsUpgrade(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archive.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range []Message{
		{ChatJID: "upgrade@g.us", ID: "one", Kind: "text", Source: "live", Timestamp: 1, ReceivedAt: 1, RawProto: []byte("x")},
		{ChatJID: "upgrade@g.us", ID: "two", Kind: "text", Source: "history", Timestamp: 2, ReceivedAt: 2, RawProto: []byte("x")},
	} {
		if _, err = s.InsertMessage(t.Context(), message); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}

	db := openSQL(t, path)
	if _, err = db.Exec(`UPDATE chat_stats SET message_rows=0;
		DELETE FROM archive_meta WHERE key='message_rows_ready'`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	var rows int
	if err = s.read.QueryRow(`SELECT message_rows FROM chat_stats WHERE canonical='upgrade@g.us'`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Fatalf("message_rows after interrupted upgrade = %d, want exact raw count 2", rows)
	}
	var marker string
	if err = s.read.QueryRow(`SELECT value FROM archive_meta WHERE key='message_rows_ready'`).Scan(&marker); err != nil || marker != "1" {
		t.Fatalf("message_rows completion marker = %q, %v; want 1", marker, err)
	}
}
