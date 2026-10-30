package devseed_test

import (
	"context"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/devseed"
	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/web"
)

func TestW6IPerformanceTargetIsPinned(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"check-w6i:", "W6I_PERF=1", "-run W6I"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("Makefile lacks %q", want)
		}
	}
}

func TestW6IPerformanceBudgets(t *testing.T) {
	if os.Getenv("W6I_PERF") != "1" {
		t.Skip("run with make check-w6i")
	}
	data := filepath.Join(t.TempDir(), "seed-42")
	if _, err := devseed.Seed(t.Context(), devseed.Options{DataDir: data, Chats: 250, Messages: 50000, LargeChatMessages: 50000, Seed: 42}); err != nil {
		t.Fatal(err)
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

	cfg := &config.Config{DataDir: data, TimeZone: "UTC"}
	h := web.New(web.Deps{Store: st, Rules: ingest.NewRuleSet(nil), Config: cfg, Now: time.Now}).Handler()
	initialBody := timedPath(t, h, "/chats/100000@lid", 150*time.Millisecond)
	older := edgePath(t, initialBody, "older")
	olderBody := timedPath(t, h, older, 150*time.Millisecond)
	newer := edgePath(t, olderBody, "newer")
	_ = timedPath(t, h, newer, 150*time.Millisecond)
	_ = timedPath(t, h, "/chats/100000@lid?date=2025-10-26", 150*time.Millisecond)
	_ = timedPath(t, h, "/chats/100000@lid?q=archive&match=100", 300*time.Millisecond)
	_ = timedPath(t, h, "/pair", 100*time.Millisecond)

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
	if got := percentile95(samples); got >= budget {
		t.Fatalf("GET %s p95 = %s, want <%s", path, got, budget)
	} else {
		t.Logf("GET %s p95=%s", path, got)
	}
	return body
}

func edgePath(t *testing.T, body, edge string) string {
	t.Helper()
	re := regexp.MustCompile(`hx-get="([^"]+[?&](?:amp;)?edge=` + edge + `(?:[&"])[^"]*)"`)
	match := re.FindStringSubmatch(body)
	if len(match) != 2 {
		t.Fatalf("viewer has no %s fragment path", edge)
	}
	return html.UnescapeString(match[1])
}

func percentile95(samples []time.Duration) time.Duration {
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	return samples[(len(samples)*95-1)/100]
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
