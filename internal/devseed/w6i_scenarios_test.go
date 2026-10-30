package devseed

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestW6IScenarioSeedArtifacts(t *testing.T) {
	data := filepath.Join(t.TempDir(), "w6i")
	if _, err := Seed(t.Context(), Options{DataDir: data, Chats: 4, Messages: 1200, LargeChatMessages: 1000, Seed: 42}); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(data, "archive.db"))+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	checks := []struct {
		name, query string
		want        int
	}{
		{"large chat", `SELECT messages>=1000 FROM chat_stats WHERE canonical='100000@lid'`, 1},
		{"quoted reply", `SELECT count(*) FROM messages WHERE quoted_id='SEED-42-000000000'`, 1},
		{"edit/revoke/reaction", `SELECT count(DISTINCT kind) FROM message_events WHERE kind IN ('edit','revoke','reaction')`, 3},
		{"stalled history", `SELECT count(*) FROM history_sync_chunks WHERE sync_type='FULL' AND progress=99`, 1},
		{"failed media", `SELECT count(*) FROM media WHERE state='failed'`, 1},
		{"expired media", `SELECT count(*) FROM media WHERE state='expired'`, 1},
	}
	for _, check := range checks {
		var got int
		if err = db.QueryRow(check.query).Scan(&got); err != nil {
			t.Errorf("%s query: %v", check.name, err)
		} else if got != check.want {
			t.Errorf("%s = %d, want %d", check.name, got, check.want)
		}
	}

	g := newGenerator(Options{Chats: 4, Messages: 16, Seed: 42})
	for i := 0; i < 8; i++ {
		result, _, _ := g.liveMessage(i, 100)
		want, _ := chatIDs(i % 4)
		if result.Message.ChatJID != want {
			t.Errorf("live message %d chat = %q, want deterministic %q", i, result.Message.ChatJID, want)
		}
	}
}

func TestW6IDevPairerUsesOnlySyntheticUpdatesAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p := newDevPairer(ctx)
	updates, err := p.StartPairing(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	select {
	case update := <-updates:
		if update.QR != "synthetic-devseed-qr-1" || update.Done || update.Err != "" {
			t.Fatalf("first fake pairing update = %+v", update)
		}
	case <-time.After(time.Second):
		t.Fatal("fake pairer did not produce its synthetic QR")
	}
	cancel()
	select {
	case _, ok := <-updates:
		if ok {
			t.Fatal("fake pairer emitted an unexpected update after cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("fake pairer did not stop with its owner context")
	}
}
