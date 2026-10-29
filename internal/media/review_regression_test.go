package media

import (
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
	"whatsapp-archiver/internal/wa"
)

func TestMixedNetworkRoundCountsOnlyNonNetworkAttempts(t *testing.T) {
	for _, final := range []error{wa.ErrMediaExpired, nil} {
		t.Run(map[bool]string{true: "expired", false: "success"}[final != nil], func(t *testing.T) {
			st := testkit.Store(t)
			var calls atomic.Int32
			d := &fakeDown{download: func([]byte, string) ([]byte, error) {
				if calls.Add(1) <= 4 {
					return nil, &net.DNSError{Err: "no such host", Name: "mmg.whatsapp.net"}
				}
				return []byte("ok"), final
			}}
			q := newTestQueue(t, st, d, config.MediaRules{Download: []string{"image"}}, newClock(), nil)
			m := store.Media{ChatJID: "g@g.us", MessageID: "M", Source: "live", Type: "image", State: "pending", RawProto: []byte("raw"), Attempts: 19}
			if err := testkit.InsertMedia(t.Context(), st, m); err != nil {
				t.Fatal(err)
			}
			q.download(t.Context(), &m)
			if m.Attempts != 20 {
				t.Fatalf("attempts after four network failures and one non-network attempt = %d, want 20", m.Attempts)
			}
		})
	}
}

func TestExpiredRequestPassYieldsToNewLiveMedia(t *testing.T) {
	st := testkit.Store(t)
	clock := newClock()
	d := &fakeDown{download: func(raw []byte, _ string) ([]byte, error) {
		if string(raw) == "live" {
			return []byte("ok"), nil
		}
		return nil, wa.ErrMediaExpired
	}}
	q := newTestQueue(t, st, d, config.MediaRules{Download: []string{"image"}, Workers: 1, RetryRequestsPerMinute: 10}, clock, nil)
	for i := 0; i < 30; i++ {
		if err := testkit.InsertMedia(t.Context(), st, store.Media{ChatJID: "g@g.us", MessageID: time.Unix(int64(i), 0).String(), SenderJID: "s@lid", Source: "history", Type: "image", State: "expired", RawProto: []byte("expired")}); err != nil {
			t.Fatal(err)
		}
	}
	stop := start(t, q)
	eventually(t, "initial expired request burst", func() bool { return len(d.requests()) == 10 })
	enqueue(t, q, store.Media{ChatJID: "g@g.us", MessageID: "LIVE", Source: "live", Type: "image", RawProto: []byte("live")})
	eventually(t, "live media while expired limiter waits", func() bool { return row(t, st, "g@g.us", "LIVE").State == "done" })
	stop()
}

func TestWorkStopsClaimingWhenConnectionGateCloses(t *testing.T) {
	st := testkit.Store(t)
	var connected atomic.Bool
	connected.Store(true)
	d := &fakeDown{download: func([]byte, string) ([]byte, error) {
		connected.Store(false)
		return []byte("ok"), nil
	}}
	q := newTestQueue(t, st, d, config.MediaRules{Download: []string{"image"}, Workers: 1}, newClock(), nil).WithConnected(connected.Load)
	enqueue(t, q, store.Media{ChatJID: "g@g.us", MessageID: "OLD", Source: "live", Type: "image", RawProto: []byte("old"), MessageTimestamp: 1})
	enqueue(t, q, store.Media{ChatJID: "g@g.us", MessageID: "NEW", Source: "live", Type: "image", RawProto: []byte("new"), MessageTimestamp: 2})
	stop := start(t, q)
	eventually(t, "first media done", func() bool { return row(t, st, "g@g.us", "NEW").State == "done" })
	time.Sleep(100 * time.Millisecond)
	if got := row(t, st, "g@g.us", "OLD").State; got != "pending" {
		t.Fatalf("second row while disconnected = %q, want pending", got)
	}
	connected.Store(true)
	q.Wake()
	eventually(t, "second media after reconnect", func() bool { return row(t, st, "g@g.us", "OLD").State == "done" })
	stop()
}

func TestFailedMediaRetryRequestIsScheduledAgainBeforeHourlyCleanup(t *testing.T) {
	st := testkit.Store(t)
	clock := newClock()
	var calls atomic.Int32
	d := &fakeDown{request: func(wa.MediaRef, []byte) error {
		if calls.Add(1) == 1 {
			return errors.New("temporary request failure")
		}
		return nil
	}}
	if err := testkit.InsertMedia(t.Context(), st, store.Media{ChatJID: "g@g.us", MessageID: "E", SenderJID: "s@lid", Source: "live", Type: "image", State: "expired", RawProto: []byte("raw")}); err != nil {
		t.Fatal(err)
	}
	q := newTestQueue(t, st, d, config.MediaRules{Download: []string{"image"}, Workers: 1, RetryRequestsPerMinute: 10}, clock, nil)
	stop := start(t, q)
	eventually(t, "first failed request", func() bool { return calls.Load() == 1 })
	time.Sleep(50 * time.Millisecond)
	clock.Advance(time.Minute)
	eventually(t, "scheduled request retry", func() bool { return row(t, st, "g@g.us", "E").State == "retry_requested" })
	stop()
}
