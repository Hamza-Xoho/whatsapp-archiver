package web

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/health"
	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/store"
)

func TestLoggingReviewScaleIdentityBudgetsAndSlowestRead(t *testing.T) {
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
	trace, err := st.Trace(t.Context(), "100000@lid", "SEED-42-000000000")
	if err != nil || len(report.Chats) != 250 || messages != 300_000 || trace.Message.Timestamp != 1761436842 || trace.Message.Source != "live" {
		t.Fatalf("not exact seed-42/250/300k data: chats=%d messages=%d trace=%+v err=%v", len(report.Chats), messages, trace.Message, err)
	}

	h := New(Deps{Store: st, Rules: ingest.NewRuleSet(nil), Config: &config.Config{DataDir: data},
		Health: func() health.Snapshot { return health.Snapshot{State: "connected", LastEvent: dashNow} }, Now: func() time.Time { return dashNow }}).Handler()
	for _, tc := range []struct {
		path  string
		limit time.Duration
	}{{"/logging", 150 * time.Millisecond}, {"/logging/outages", 150 * time.Millisecond}, {"/logging/gaps-media", 150 * time.Millisecond},
		{"/logging/history", 150 * time.Millisecond}, {"/logging/alerts", 150 * time.Millisecond}, {"/logging/storage", 150 * time.Millisecond},
		{"/logging/feed", 150 * time.Millisecond}, {"/logging/settings", 150 * time.Millisecond}, {"/partials/health", 50 * time.Millisecond}, {"/partials/feed", 50 * time.Millisecond}} {
		var samples []time.Duration
		maxBytes := 0
		for i := range 35 {
			started := time.Now()
			body, code, _ := get(t, h, tc.path)
			took := time.Since(started)
			if code != 200 {
				t.Fatalf("GET %s = %d", tc.path, code)
			}
			maxBytes = max(maxBytes, len(body))
			if i >= 5 {
				samples = append(samples, took)
			}
		}
		slices.Sort(samples)
		p50, p95, maximum := samples[14], samples[28], samples[29]
		t.Logf("%s p50=%v p95=%v max=%v bytes=%d", tc.path, p50, p95, maximum, maxBytes)
		if p50 >= tc.limit || p95 >= tc.limit || maximum >= tc.limit || maxBytes > 100<<10 {
			t.Errorf("%s budget failed: p50=%v p95=%v max=%v bytes=%d limit=%v", tc.path, p50, p95, maximum, maxBytes, tc.limit)
		}
	}

	reads := map[string]func() error{
		"outages": func() error { _, err := st.LoggingOutages(t.Context(), 200); return err },
		"gaps":    func() error { _, err := st.LoggingGaps(t.Context(), 200); return err },
		"media":   func() error { _, err := st.LoggingMedia(t.Context(), 50); return err },
		"history": func() error { _, err := st.LoggingHistory(t.Context(), 250); return err },
		"alerts":  func() error { _, err := st.LoggingAlerts(t.Context(), 200); return err },
	}
	slowestName, slowest := "", time.Duration(0)
	for name, read := range reads {
		started := time.Now()
		if err := read(); err != nil {
			t.Fatalf("%s read: %v", name, err)
		}
		if took := time.Since(started); took > slowest {
			slowestName, slowest = name, took
		}
	}
	if slowestName == "" {
		t.Fatal("no logging read model was timed")
	}
	t.Logf("slowest SQL/read model: %s %v", slowestName, slowest)
}
