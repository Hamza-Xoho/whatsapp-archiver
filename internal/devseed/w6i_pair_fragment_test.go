package devseed

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"whatsapp-archiver/internal/activity"
	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/health"
	"whatsapp-archiver/internal/web"
)

var errPairMeasured = errors.New("pair fragment measured")

// This is the release-build, fake-only integration measurement for the pair
// fragment. It exercises the exact devseed Pairer and fully encoded SSE write.
func TestW6IDevseedPairFragmentBudget(t *testing.T) {
	if os.Getenv("W6I_PERF") != "1" {
		t.Skip("run with make check-w6i")
	}
	p := newDevPairerWithTiming(t.Context(), devPairTiming{Waiting: time.Millisecond, QR: time.Hour, Finish: time.Hour})
	s := web.New(web.Deps{
		Activity: activity.NewBus(10), Pairer: p, Config: &config.Config{TimeZone: "UTC"},
		Health: func() health.Snapshot { return health.Snapshot{State: p.HealthState()} },
	})
	postPair(t, s.Handler(), "/pair/start")
	wantPage(t, s.Handler(), `src="/pair/qr.png?n=1"`)

	samples := make([]time.Duration, 0, 30)
	var bytes int
	for i := 0; i < 35; i++ {
		w := newPairStopWriter()
		req := httptest.NewRequest(http.MethodGet, "/events", nil)
		start := time.Now()
		s.Handler().ServeHTTP(w, req)
		elapsed := time.Since(start)
		if w.status != http.StatusOK || !strings.Contains(w.body.String(), "event: pair\n") {
			t.Fatalf("pair stream status/body = %d/%q", w.status, w.body.String())
		}
		bytes = w.body.Len()
		if i >= 5 {
			samples = append(samples, elapsed)
		}
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	p50, p95 := samples[14], samples[28]
	if p95 >= 50*time.Millisecond {
		t.Fatalf("devseed pair SSE p50/p95 = %s/%s, want p95 <50ms", p50, p95)
	}
	t.Logf("devseed pair SSE p50=%s p95=%s max=%s bytes=%d", p50, p95, samples[29], bytes)
}

type pairStopWriter struct {
	header http.Header
	body   strings.Builder
	status int
}

func newPairStopWriter() *pairStopWriter { return &pairStopWriter{header: make(http.Header)} }
func (w *pairStopWriter) Header() http.Header { return w.header }
func (w *pairStopWriter) WriteHeader(status int) { w.status = status }
func (w *pairStopWriter) Flush() {}
func (w *pairStopWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	_, _ = w.body.WriteString(string(p))
	if strings.Contains(w.body.String(), "event: pair\n") {
		return len(p), errPairMeasured
	}
	return len(p), nil
}

