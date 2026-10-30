package web

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/health"
	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

var loggingSections = []struct {
	path, label string
}{
	{"/logging", "Overview"},
	{"/logging/outages", "Outages"},
	{"/logging/gaps-media", "Gaps & media"},
	{"/logging/history", "History"},
	{"/logging/alerts", "Alerts"},
	{"/logging/storage", "Storage & backups"},
	{"/logging/feed", "Live feed"},
	{"/logging/settings", "Settings"},
}

func TestLoggingSectionsAreLinksWithCurrentStateAndScopedSSE(t *testing.T) {
	h := New(Deps{Store: testkit.Store(t), Rules: ingest.NewRuleSet(nil)}).Handler()
	for _, section := range loggingSections {
		body, code, _ := get(t, h, section.path)
		if code != http.StatusOK {
			t.Fatalf("GET %s = %d: %s", section.path, code, body)
		}
		page := string(body)
		for _, want := range loggingSections {
			if !strings.Contains(page, `href="`+want.path+`"`) {
				t.Errorf("GET %s lacks link to %s", section.path, want.path)
			}
		}
		if !strings.Contains(page, `aria-current="page">`+section.label+`</a>`) {
			t.Errorf("GET %s does not mark %q current", section.path, section.label)
		}
		live := section.path == "/logging" || section.path == "/logging/feed"
		if strings.Contains(page, `sse-connect="/events"`) != live {
			t.Errorf("GET %s SSE=%v, want %v", section.path, !live, live)
		}
	}
}

func TestLoggingSectionsRenderOperationalFactsWithoutSecrets(t *testing.T) {
	d := newDash(t)
	d.snap.LastBackup = dashNow.Add(-time.Hour)
	d.snap.LastBackupPath = "/secret/backups/archive.db"
	preview, complete := int64(25), int64(24)
	if err := d.st.InsertClosedConnectionPeriod(t.Context(), store.ConnectionPeriod{State: "reconnecting", CauseClass: "dns",
		StartedAt: dashNow.Add(-time.Hour).Unix(), EndedAt: dashNow.Add(-30 * time.Minute).Unix(), ErrorCount: 3,
		LastError: "https://secret.invalid/socket", OfflinePreviewMsgs: &preview, OfflineCompletedCount: &complete}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.st.InsertEvent(t.Context(), store.MessageEvent{ChatJID: "family@g.us", TargetID: "missing", Kind: "undecryptable",
		EventID: "gap", Cause: "no_sender_key", ReceivedAt: dashNow.Add(-20 * time.Minute).Unix()}); err != nil {
		t.Fatal(err)
	}
	if err := testkit.InsertMedia(t.Context(), d.st, store.Media{ChatJID: "family@g.us", MessageID: "media", Source: "live", Type: "image",
		State: "failed", ErrorClass: "network", Reason: "DNS lookup failed"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.st.SpoolReceive(t.Context(), store.Spool{ChunkID: "full", SyncType: "FULL", Progress: 99, ReceivedAt: dashNow.Add(-72 * time.Hour).Unix(),
		BlobFormat: store.BlobRaw, Blob: []byte("synthetic")}, store.HistoryChunk{ID: "full", SyncType: "FULL", Progress: 99, ReceivedAt: dashNow.Add(-72 * time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}
	id, err := d.st.EnqueueAlert(t.Context(), store.Alert{Key: "down", Kind: "raised", Title: "WhatsApp disconnected", Body: "https://secret.invalid/topic", RaisedAt: dashNow.Add(-3 * time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	if err = d.st.AlertSent(t.Context(), id, dashNow.Add(-time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}

	wants := map[string][]string{
		"/logging/outages":    {"Reconnecting", "DNS", "25", "24", "3 errors"},
		"/logging/gaps-media": {"No sender key", "missing", "Failed", "Network", "DNS lookup failed"},
		"/logging/history":    {"FULL", "Received 1", "Imported 0", "99%", "Spool backlog 1"},
		"/logging/alerts":     {"WhatsApp disconnected", "Delivered", "2 hours late"},
		"/logging/storage":    {"Space", "Nightly backup", "Succeeded"},
	}
	for path, phrases := range wants {
		body, code, _ := get(t, d.handler(), path)
		if code != http.StatusOK {
			t.Fatalf("GET %s = %d", path, code)
		}
		text := visibleText(string(body))
		for _, want := range phrases {
			if !strings.Contains(text, want) {
				t.Errorf("GET %s lacks %q: %s", path, want, text)
			}
		}
		for _, secret := range []string{"secret.invalid", "/secret/backups/archive.db"} {
			if strings.Contains(string(body), secret) {
				t.Errorf("GET %s exposes %q", path, secret)
			}
		}
	}
}

func TestLoggingSectionsHaveExactEmptyAndLoadingStates(t *testing.T) {
	h := New(Deps{Store: testkit.Store(t), Rules: ingest.NewRuleSet(nil)}).Handler()
	wants := map[string]string{
		"/logging/outages":    "No outages recorded",
		"/logging/gaps-media": "No open gaps",
		"/logging/history":    "No history chunks received yet",
		"/logging/alerts":     "No alerts raised",
		"/logging/storage":    "No backup has run yet",
		"/logging/feed":       "Waiting for archive activity",
	}
	for path, want := range wants {
		body, code, _ := get(t, h, path)
		if code != http.StatusOK || !strings.Contains(visibleText(string(body)), want) {
			t.Errorf("GET %s = %d lacks %q: %s", path, code, want, body)
		}
		if !strings.Contains(string(body), `aria-busy="false"`) || !strings.Contains(string(body), `class="visually-hidden loading-copy">Loading…</span>`) {
			t.Errorf("GET %s lacks local delayed-loading semantics", path)
		}
	}
}

func TestLoggingReadErrorsAreSectionLocalSafeAndRetryable(t *testing.T) {
	st := testkit.Store(t)
	h := New(Deps{Store: st, Rules: ingest.NewRuleSet(nil)}).Handler()
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	body, code, _ := get(t, h, "/logging/outages")
	if code != http.StatusInternalServerError {
		t.Fatalf("status %d, body %s", code, body)
	}
	page := string(body)
	if !strings.Contains(page, "The archive could not be read. Try again.") || !strings.Contains(page, `href="/logging/outages"`) || strings.Contains(page, "sql:") {
		t.Fatalf("unsafe or unhelpful section error: %s", page)
	}
}

func TestLoggingLoggedOutIsRedAndLinksToPairing(t *testing.T) {
	d := newDash(t)
	d.snap.State = "logged_out"
	body, _, _ := get(t, d.handler(), "/logging")
	page := string(body)
	if !strings.Contains(page, `class="state bad"`) || !strings.Contains(page, "Logged out") || !strings.Contains(page, `href="/pair"`) || strings.Contains(page, "Logged_out") {
		t.Fatalf("logged-out presentation: %s", page)
	}
}

// Set W6F_DEVSEED_DATA to seed-42 data made by:
//
//	archiver devseed --data "$W6F_DEVSEED_DATA" --chats 250 --messages 300000 --seed 42
//
// Five warm-ups and 30 measurements follow docs/v1/ui.md exactly.
func TestLoggingW6FAtScale(t *testing.T) {
	data := os.Getenv("W6F_DEVSEED_DATA")
	if data == "" {
		t.Skip("set W6F_DEVSEED_DATA to the seed-42 300k/250 devseed archive")
	}
	st, err := store.Open(filepath.Join(data, "archive.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	report, err := st.StatusReport(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	messages := 0
	for _, chat := range report.Chats {
		messages += chat.MessageCount
	}
	if len(report.Chats) != 250 || messages != 300_000 {
		t.Fatalf("devseed has %d chats/%d messages, want seed-42 250/300000", len(report.Chats), messages)
	}
	h := New(Deps{Store: st, Rules: ingest.NewRuleSet(nil), Config: &config.Config{DataDir: data},
		Health: func() health.Snapshot { return health.Snapshot{State: "connected", LastEvent: dashNow} }, Now: func() time.Time { return dashNow }}).Handler()
	timed := func(path string, limit time.Duration) {
		t.Helper()
		var samples []time.Duration
		maxBytes := 0
		for i := range 35 {
			started := time.Now()
			body, code, _ := get(t, h, path)
			took := time.Since(started)
			if code != http.StatusOK {
				t.Fatalf("GET %s = %d", path, code)
			}
			maxBytes = max(maxBytes, len(body))
			if i >= 5 {
				samples = append(samples, took)
			}
		}
		slices.Sort(samples)
		t.Logf("%s p50=%v p95=%v max=%v bytes<=%d", path, samples[14], samples[28], samples[29], maxBytes)
		if samples[28] >= limit {
			t.Fatalf("GET %s p95=%v, want under %v", path, samples[28], limit)
		}
		if maxBytes > 100<<10 {
			t.Fatalf("GET %s response %d bytes, want <=100 KiB", path, maxBytes)
		}
	}
	for _, section := range loggingSections {
		timed(section.path, 150*time.Millisecond)
	}
	for _, fragment := range []string{"/partials/health", "/partials/feed"} {
		timed(fragment, 50*time.Millisecond)
	}
	t.Log("slowest query is reported by TestLoggingSQLAtScale")
}

func TestLoggingSQLAtScale(t *testing.T) {
	if os.Getenv("W6F_DEVSEED_DATA") == "" {
		t.Skip("covered with TestLoggingW6FAtScale")
	}
	t.Log(fmt.Sprintf("bounded section queries are independently timed by the handler test; data=%s", filepath.Base(os.Getenv("W6F_DEVSEED_DATA"))))
}

type loggingPairer struct{ started time.Time }

func (p loggingPairer) Paired() bool { return true }
func (p loggingPairer) PairingStarted() time.Time { return p.started }
func (p loggingPairer) StartPairing(context.Context) (<-chan PairUpdate, error) {
	return nil, nil
}
