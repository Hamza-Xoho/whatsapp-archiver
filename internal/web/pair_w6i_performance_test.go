package web

import (
	"sort"
	"strings"
	"testing"
	"time"
)

// The pair SSE fragment has the same release budget as health/feed fragments.
// Measuring the encoded event (rather than GET /pair) pins the actual work the
// stream performs and makes the devseed integration measurement comparable.
func TestPairFragmentW6IAtScale(t *testing.T) {
	f := newFakePairer(t, false)
	s := newPairUI(t, f)
	s.beginPairing(f.ch)
	f.send(PairUpdate{QR: "2@performance"})
	waitPage(t, s.Handler(), `src="/pair/qr.png`)

	samples := make([]time.Duration, 0, 30)
	for i := 0; i < 35; i++ {
		start := time.Now()
		wire, err := s.renderPairEvent(t.Context())
		elapsed := time.Since(start)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(wire), "event: pair\n") || !strings.Contains(string(wire), `src="/pair/qr.png`) {
			t.Fatalf("pair fragment is not a complete encoded pair event: %q", wire)
		}
		if i >= 5 {
			samples = append(samples, elapsed)
		}
	}
	p50, p95 := percentileDuration(samples, 50), percentileDuration(samples, 95)
	if p95 >= 50*time.Millisecond {
		t.Fatalf("pair SSE fragment p50/p95 = %s/%s, want p95 <50ms", p50, p95)
	}
	t.Logf("pair SSE fragment p50=%s p95=%s max=%s bytes=%d", p50, p95, samples[len(samples)-1], len(mustPairWire(t, s)))
}

func percentileDuration(samples []time.Duration, n int) time.Duration {
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	return samples[(len(samples)*n-1)/100]
}

func mustPairWire(t *testing.T, s *Server) []byte {
	t.Helper()
	wire, err := s.renderPairEvent(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return wire
}

