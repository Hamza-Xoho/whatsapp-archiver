package devseed

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/health"
	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/web"
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

func TestW6ILaunchConfigurationsAreDevseedOnly(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".claude", "launch.json"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if strings.Contains(text, `"args": ["run"`) {
		t.Fatal("launch.json exposes archiver run; W6i launches must be devseed-only")
	}
	if got := strings.Count(text, `"args": ["devseed"`); got < 2 {
		t.Fatalf("devseed launch count = %d, want at least 2", got)
	}
}

func TestW6IDevPairerHTTPScenarioCoversEverySafeStateWithoutArchiveWrites(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	p := newDevPairerWithTiming(ctx, devPairTiming{
		Waiting: 5 * time.Millisecond, QR: 8 * time.Millisecond, Finish: 8 * time.Millisecond, LoggedOutDelay: 8 * time.Millisecond,
	})
	data := filepath.Join(t.TempDir(), "pairing")
	st, err := store.Open(filepath.Join(data, "archive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err = st.Apply(t.Context(), store.Batch{Message: &store.Message{ChatJID: "safe@g.us", ID: "kept", Kind: "text", Text: "unchanged", Source: "live", Timestamp: 1, ReceivedAt: 1}}); err != nil {
		t.Fatal(err)
	}
	before := archiveShape(t, filepath.Join(data, "archive.db"))
	h := web.New(web.Deps{
		Store: st, Rules: ingest.NewRuleSet(nil), Pairer: p, Config: &config.Config{DataDir: data, TimeZone: "UTC"},
		Health: func() health.Snapshot { return health.Snapshot{State: p.HealthState()} },
	}).Handler()

	wantPage(t, h, "Pair WhatsApp") // not paired
	postPair(t, h, "/pair/start")
	wantPage(t, h, "Preparing a QR code") // starting/waiting
	wantPage(t, h, `src="/pair/qr.png?n=1"`)
	wantPage(t, h, `src="/pair/qr.png?n=2"`) // renewed QR
	wantPage(t, h, "Pairing failed safely")

	postPair(t, h, "/pair/start")
	wantPage(t, h, "Paired. Capture is running.")
	wantPage(t, h, "WhatsApp logged this archiver out")
	postPair(t, h, "/pair/restart") // LoggedOut / Pair again
	wantPage(t, h, "Paired. Capture is running.")
	postPair(t, h, "/pair/restart") // voluntary re-pair
	wantPage(t, h, "Preparing a QR code")
	postPair(t, h, "/pair/cancel")
	wantPage(t, h, "Pairing cancelled")
	postPair(t, h, "/pair/start") // Cancel -> immediate Start must not reuse the cancelled run.
	wantPage(t, h, `src="/pair/qr.png?n=5"`)

	if after := archiveShape(t, filepath.Join(data, "archive.db")); after != before {
		t.Fatalf("fake pairing mutated archive: before=%v after=%v", before, after)
	}
	for _, path := range []string{"pairer.go", "devseed.go"} {
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if strings.Contains(string(body), "internal/wa") || strings.Contains(string(body), "whatsmeow") {
			t.Fatalf("%s imports a WhatsApp boundary", path)
		}
	}
}

func archiveShape(t *testing.T, path string) string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var messages, events, mappings int
	if err = db.QueryRowContext(t.Context(), `SELECT
	 (SELECT COUNT(*) FROM messages),(SELECT COUNT(*) FROM message_events),(SELECT COUNT(*) FROM id_mappings)`).Scan(&messages, &events, &mappings); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%d/%d/%d", messages, events, mappings)
}

func postPair(t *testing.T, h http.Handler, path string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(""))
	req.Host = "localhost:8090"
	req.Header.Set("Origin", "http://localhost:8090")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("POST %s = %d, want 303: %s", path, w.Code, w.Body.String())
	}
}

func wantPage(t *testing.T, h http.Handler, want string) string {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/pair", nil)
		req.Host = "localhost:8090"
		h.ServeHTTP(w, req)
		body, _ := io.ReadAll(w.Result().Body)
		if strings.Contains(string(body), want) {
			return string(body)
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("pair page never contained %q", want)
	return ""
}
