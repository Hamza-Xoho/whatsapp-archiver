package devseed_test

import (
	"context"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/web"
)

func TestW6IPerformanceTargetIsPinned(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"check-w6i:", "W6I_PERF=1", "W6I_DEVSEED_DATA", "W6B_DEVSEED_DATA", "W6F_DEVSEED_DATA",
		"--seed 42", "--chats 250", "--messages 300000", "--large-chat-messages 50000", "ChatsW6BAtScale", "LoggingW6FAtScale"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("Makefile lacks %q", want)
		}
	}
}

func TestW6IPerformanceBudgets(t *testing.T) {
	if os.Getenv("W6I_PERF") != "1" {
		t.Skip("run with make check-w6i")
	}
	data := os.Getenv("W6I_DEVSEED_DATA")
	if data == "" {
		t.Fatal("make check-w6i did not provide its exact shared seed")
	}
	st, err := store.Open(filepath.Join(data, "archive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	trace, err := st.Trace(t.Context(), "100000@lid", "SEED-42-000000000")
	if err != nil || trace.Message.ID != "SEED-42-000000000" || trace.Message.Timestamp != 1761436842 {
		t.Fatalf("seed identity mismatch: trace=%+v err=%v", trace.Message, err)
	}
	report, err := st.StatusReport(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	messages, large := 0, 0
	for _, chat := range report.Chats {
		messages += chat.MessageCount
		if chat.CanonicalJID == "100000@lid" {
			large = chat.MessageCount
		}
	}
	if len(report.Chats) != 250 || messages != 300000 || large < 50000 {
		t.Fatalf("seed identity chats/messages/large = %d/%d/%d, want 250/300000/>=50000", len(report.Chats), messages, large)
	}

	cfg := &config.Config{DataDir: data, TimeZone: "UTC"}
	h := web.New(web.Deps{Store: st, Rules: ingest.NewRuleSet(nil), Config: cfg, Now: time.Now}).Handler()
	chatsBody := timedPath(t, h, "/chats", 150*time.Millisecond)
	if len(chatsBody) > 100<<10 {
		t.Fatalf("GET /chats response = %d bytes, want <=100 KiB", len(chatsBody))
	}
	_ = timedPath(t, h, "/chats?q=Synthetic&type=direct", 150*time.Millisecond)
	if next := regexp.MustCompile(`href="([^"]+)"[^>]*>Next</a>`).FindStringSubmatch(chatsBody); len(next) == 2 {
		_ = timedPath(t, h, html.UnescapeString(next[1]), 150*time.Millisecond)
	} else {
		t.Fatal("GET /chats has no next page")
	}
	initialBody := timedPath(t, h, "/chats/100000@lid", 150*time.Millisecond)
	older := edgePath(t, initialBody, "older")
	olderBody := timedPath(t, h, older, 150*time.Millisecond)
	newer := edgePath(t, olderBody, "newer")
	_ = timedPath(t, h, newer, 150*time.Millisecond)
	_ = timedPath(t, h, "/chats/100000@lid?date=2025-10-26", 150*time.Millisecond)
	_ = timedPath(t, h, "/chats/100000@lid?q=archive&match=100", 300*time.Millisecond)
	_ = timedPath(t, h, "/search?q="+url.QueryEscape("مرحبا"), 300*time.Millisecond)
	_ = timedPath(t, h, "/pair", 100*time.Millisecond)
	for _, path := range []string{"/logging", "/logging/outages", "/logging/gaps-media", "/logging/history", "/logging/alerts", "/logging/storage", "/logging/feed", "/logging/settings"} {
		_ = timedPath(t, h, path, 150*time.Millisecond)
	}
	for _, path := range []string{"/partials/health", "/partials/feed"} {
		body := timedPath(t, h, path, 50*time.Millisecond)
		if len(body) > 16<<10 {
			t.Fatalf("GET %s response = %d bytes, want <=16 KiB SSE fragment bound", path, len(body))
		}
	}

	headerSamples := make([]time.Duration, 0, 30)
	for i := 0; i < 35; i++ {
		ctx, cancel := context.WithCancel(t.Context())
		req := httptest.NewRequest(http.MethodGet, "/chats/100000@lid/export/download?format=json", nil).WithContext(ctx)
		req.Host = "localhost:8080"
		w := &discardWriter{header: make(http.Header), started: time.Now(), cancel: cancel}
		h.ServeHTTP(w, req)
		cancel()
		if w.status != http.StatusOK || w.first.IsZero() {
			t.Fatalf("export start status=%d first=%v", w.status, w.first)
		}
		if i >= 5 {
			headerSamples = append(headerSamples, w.first.Sub(w.started))
		}
	}
	if got := percentile95(headerSamples); got >= 150*time.Millisecond {
		t.Fatalf("export start p95 = %s, want <150ms", got)
	}

	const mediaBytes = int64(20 << 20)
	mediaPath := filepath.Join(data, "media", "devseed", "throughput.bin")
	file, err := os.Create(mediaPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.CopyN(file, zeroReader{}, mediaBytes); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = st.Apply(t.Context(), store.Batch{Media: &store.Media{ChatJID: "100000@lid", MessageID: "SEED-42-000000049", SenderJID: "800000@lid", Source: "live", Type: "image", MIME: "application/octet-stream", Filename: "throughput.bin", Path: mediaPath, State: "done", DeclaredSize: mediaBytes, MessageTimestamp: 1761436891, EnqueuedAt: 1761436891, UpdatedAt: 1761436892, DoneAt: 1761436892}}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/chats/100000@lid/export/download?format=zip", nil)
	req.Host = "localhost:8080"
	w := &discardWriter{header: make(http.Header), started: time.Now()}
	h.ServeHTTP(w, req)
	elapsed := time.Since(w.started)
	if elapsed >= time.Second {
		t.Fatalf("20 MiB local ZIP stream took %s, want >=20 MiB/s", elapsed)
	}
	t.Logf("20 MiB ZIP stream %s (%.1f MiB/s), export headers p95 %s", elapsed, 20/elapsed.Seconds(), percentile95(headerSamples))
}

func timedPath(t *testing.T, h http.Handler, path string, budget time.Duration) string {
	t.Helper()
	var samples []time.Duration
	var body string
	for i := 0; i < 35; i++ {
		start := time.Now()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Host = "localhost:8080"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		elapsed := time.Since(start)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s: %d %s", path, w.Code, w.Body.String())
		}
		body = w.Body.String()
		if i >= 5 {
			samples = append(samples, elapsed)
		}
	}
	p50, p95, maximum := percentile(samples, 50), percentile(samples, 95), samples[len(samples)-1]
	if p95 >= budget {
		t.Fatalf("GET %s p50/p95/max = %s/%s/%s, want p95 <%s", path, p50, p95, maximum, budget)
	} else {
		t.Logf("GET %s p50=%s p95=%s max=%s bytes=%d", path, p50, p95, maximum, len(body))
	}
	return body
}

func edgePath(t *testing.T, body, edge string) string {
	t.Helper()
	re := regexp.MustCompile(`hx-get="([^"]*(?:\?|&amp;|&)edge=` + edge + `(?:&amp;[^"]*)?)"`)
	match := re.FindStringSubmatch(body)
	if len(match) != 2 {
		t.Fatalf("viewer has no %s fragment path", edge)
	}
	return html.UnescapeString(match[1])
}

func percentile95(samples []time.Duration) time.Duration {
	return percentile(samples, 95)
}

func percentile(samples []time.Duration, n int) time.Duration {
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	return samples[(len(samples)*n-1)/100]
}

type discardWriter struct {
	header  http.Header
	status  int
	started time.Time
	first   time.Time
	bytes   int64
	cancel  context.CancelFunc
}

func (w *discardWriter) Header() http.Header { return w.header }
func (w *discardWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status, w.first = status, time.Now()
	if w.cancel != nil {
		w.cancel()
	}
}
func (w *discardWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	w.bytes += int64(len(body))
	return len(body), nil
}

type zeroReader struct{}

func (zeroReader) Read(body []byte) (int, error) {
	clear(body)
	return len(body), nil
}
