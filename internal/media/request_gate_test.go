package media

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

func TestRequestPassRechecksConnectionAfterLimiterWait(t *testing.T) {
	st := testkit.Store(t)
	clock := newClock()
	d := expiredDown()
	var connected atomic.Bool
	connected.Store(true)
	q := newTestQueue(t, st, d, config.MediaRules{Download: []string{"image"}, Workers: 1, RetryRequestsPerMinute: 10}, clock, nil).WithConnected(connected.Load)
	for i := 0; i < 11; i++ {
		enqueue(t, q, store.Media{ChatJID: "g@g.us", MessageID: fmt.Sprintf("E%02d", i), SenderJID: "s@lid", Source: "history", Type: "image", RawProto: []byte("raw")})
	}
	stop := start(t, q)
	eventually(t, "initial request burst", func() bool { return len(d.requests()) == 10 })
	eventually(t, "limiter timer", func() bool {
		clock.mu.Lock()
		defer clock.mu.Unlock()
		return len(clock.timers) > 0
	})

	connected.Store(false)
	clock.Advance(time.Minute)
	time.Sleep(100 * time.Millisecond)
	if got := len(d.requests()); got != 10 {
		t.Fatalf("requests after disconnect during limiter wait = %d, want 10", got)
	}

	connected.Store(true)
	q.Wake()
	eventually(t, "request after reconnect", func() bool { return len(d.requests()) == 11 })
	stop()
}
