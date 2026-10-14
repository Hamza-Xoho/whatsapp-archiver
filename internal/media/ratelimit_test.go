package media

import (
	"fmt"
	"testing"
	"time"

	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
	"whatsapp-archiver/internal/wa"
)

func expiredDown() *fakeDown {
	return &fakeDown{download: func([]byte, string) ([]byte, error) { return nil, wa.ErrMediaExpired }}
}

// waitRequests waits until n re-upload requests were sent, then lets 20 more clock reads (later worker passes at
// the same fake time) happen and checks that no further request was sent.
func waitRequests(t *testing.T, d *fakeDown, c *testClock, n int) {
	t.Helper()
	eventually(t, fmt.Sprintf("%d re-upload requests", n), func() bool { return len(d.requests()) >= n })
	reads := c.Reads()
	eventually(t, "later worker passes", func() bool { return c.Reads() >= reads+20 })
	if got := len(d.requests()); got != n {
		t.Fatalf("re-upload requests = %d, want %d", got, n)
	}
}

// H4: 30 expired items within one minute → exactly 10 requests; the rest are sent as the limiter refills on the
// injected clock, and a restart revisits rows still expired.
func TestExpiredRequestsAreRateLimited(t *testing.T) {
	st := testkit.Store(t)
	clock := newClock()
	d := expiredDown()
	rules := config.MediaRules{Download: []string{"image"}, Workers: 2, RetryRequestsPerMinute: 10}
	q := newTestQueue(t, st, d, rules, clock, nil)
	for i := 0; i < 30; i++ {
		enqueue(t, q, store.Media{ChatJID: "123@g.us", MessageID: fmt.Sprintf("M%02d", i), SenderJID: "5@lid", Type: "image", RawProto: []byte(fmt.Sprintf("M%02d", i)), MessageTimestamp: int64(i)})
	}
	stop := start(t, q)
	waitRequests(t, d, clock, 10)
	clock.Advance(time.Minute)
	waitRequests(t, d, clock, 20)
	stop()
	clock.Advance(time.Minute)
	stop2 := start(t, newTestQueue(t, st, d, rules, clock, nil))
	waitRequests(t, d, clock, 30)
	stop2()
	seen := map[string]bool{}
	for _, ref := range d.requests() {
		seen[ref.ID] = true
	}
	for i := 0; i < 30; i++ {
		id := fmt.Sprintf("M%02d", i)
		if m := row(t, st, "123@g.us", id); m.State != "retry_requested" || !seen[id] {
			t.Fatalf("row %s state=%q requested=%v", id, m.State, seen[id])
		}
	}
}

// A re-upload request unanswered for 24 hours fails with reason "no re-upload" without waiting for a restart.
func TestUnansweredRetryRequestFailsAfterADay(t *testing.T) {
	st := testkit.Store(t)
	clock := newClock()
	q := newTestQueue(t, st, expiredDown(), config.MediaRules{Download: []string{"image"}, Workers: 1}, clock, nil)
	enqueue(t, q, store.Media{ChatJID: "123@g.us", MessageID: "OLD", SenderJID: "5@lid", Type: "image", RawProto: []byte("x")})
	stop := start(t, q)
	eventually(t, "re-upload requested", func() bool { return row(t, st, "123@g.us", "OLD").State == "retry_requested" })
	clock.Advance(25 * time.Hour)
	eventually(t, "row failed", func() bool { return row(t, st, "123@g.us", "OLD").State == "failed" })
	stop()
	if m := row(t, st, "123@g.us", "OLD"); m.Reason != "no re-upload" {
		t.Fatalf("row=%+v", m)
	}
}
