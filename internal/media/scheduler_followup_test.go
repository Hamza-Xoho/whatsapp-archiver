package media

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
	"whatsapp-archiver/internal/wa"
)

type failingClaimStore struct {
	*store.Store
	claims atomic.Int32
}

func (s *failingClaimStore) ClaimMedia(context.Context, time.Time) (*store.Media, error) {
	s.claims.Add(1)
	return nil, errors.New("temporary claim failure")
}

func TestClaimFailureWaitsBeforeRetryingDueMedia(t *testing.T) {
	st := testkit.Store(t)
	clock := newClock()
	failed := &failingClaimStore{Store: st}
	q := newTestQueue(t, st, &fakeDown{}, config.MediaRules{Download: []string{"image"}, Workers: 1}, clock, nil)
	q.store = failed
	enqueue(t, q, store.Media{ChatJID: "g@g.us", MessageID: "DUE", Source: "live", Type: "image", RawProto: []byte("raw")})
	stop := start(t, q)
	eventually(t, "first failed claim", func() bool { return failed.claims.Load() >= 1 })
	time.Sleep(50 * time.Millisecond)
	if got := failed.claims.Load(); got != 1 {
		t.Fatalf("claims before retry delay = %d, want 1", got)
	}
	clock.Advance(time.Minute)
	eventually(t, "claim after retry delay", func() bool { return failed.claims.Load() == 2 })
	stop()
}

func TestReconnectWakesEveryConfiguredWorker(t *testing.T) {
	st := testkit.Store(t)
	var connected atomic.Bool
	var gateChecks atomic.Int32
	started := make(chan struct{}, 3)
	release := make(chan struct{})
	defer close(release)
	d := &fakeDown{download: func([]byte, string) ([]byte, error) {
		started <- struct{}{}
		<-release
		return []byte("ok"), nil
	}}
	q := newTestQueue(t, st, d, config.MediaRules{Download: []string{"image"}, Workers: 3}, newClock(), nil).
		WithConnected(func() bool {
			gateChecks.Add(1)
			return connected.Load()
		})
	for _, id := range []string{"A", "B", "C"} {
		enqueue(t, q, store.Media{ChatJID: "g@g.us", MessageID: id, Source: "live", Type: "image", RawProto: []byte(id)})
	}
	stop := start(t, q)
	eventually(t, "all workers to reach the disconnected gate", func() bool { return gateChecks.Load() >= 3 })
	connected.Store(true)
	q.Wake()
	for workers := 1; workers <= 3; workers++ {
		select {
		case <-started:
		case <-time.After(250 * time.Millisecond):
			t.Fatalf("downloads started after reconnect = %d, want 3 concurrent workers", workers-1)
		}
	}
	stop()
}

type cancelOnSecondClaimStore struct {
	*store.Store
	claims atomic.Int32
	cancel context.CancelFunc
}

func (s *cancelOnSecondClaimStore) ClaimMedia(ctx context.Context, now time.Time) (*store.Media, error) {
	if s.claims.Add(1) == 2 {
		s.cancel()
	}
	return s.Store.ClaimMedia(ctx, now)
}

func TestWorkDoesNotClaimAnotherRowAfterGateCloses(t *testing.T) {
	st := testkit.Store(t)
	ctx, cancel := context.WithCancel(t.Context())
	counted := &cancelOnSecondClaimStore{Store: st, cancel: cancel}
	var connected atomic.Bool
	connected.Store(true)
	d := &fakeDown{download: func([]byte, string) ([]byte, error) {
		connected.Store(false)
		return []byte("ok"), nil
	}}
	q := newTestQueue(t, st, d, config.MediaRules{Download: []string{"image"}, Workers: 1}, newClock(), nil).WithConnected(connected.Load)
	q.store = counted
	for _, id := range []string{"A", "B"} {
		if err := testkit.InsertMedia(t.Context(), st, store.Media{ChatJID: "g@g.us", MessageID: id, Source: "live", Type: "image", State: "pending", RawProto: []byte(id)}); err != nil {
			t.Fatal(err)
		}
	}
	var current *store.Media
	q.work(ctx, &current)
	if got := counted.claims.Load(); got != 1 {
		t.Fatalf("claims after connection gate closed = %d, want 1", got)
	}
}

func TestDownloadRetryStopsWhenConnectionGateCloses(t *testing.T) {
	st := testkit.Store(t)
	var connected atomic.Bool
	connected.Store(true)
	var calls atomic.Int32
	clock := newClock()
	d := &fakeDown{download: func([]byte, string) ([]byte, error) {
		calls.Add(1)
		connected.Store(false)
		return nil, &net.DNSError{Err: "no such host", Name: "mmg.whatsapp.net"}
	}}
	q := newTestQueue(t, st, d, config.MediaRules{Download: []string{"image"}, Workers: 1}, clock, nil).WithConnected(connected.Load)
	enqueue(t, q, store.Media{ChatJID: "g@g.us", MessageID: "NET", Source: "live", Type: "image", RawProto: []byte("raw")})
	stop := start(t, q)
	eventually(t, "download yielded after disconnect", func() bool {
		return calls.Load() == 1 && row(t, st, "g@g.us", "NET").State == "pending"
	})
	stop()
	if got := calls.Load(); got != 1 {
		t.Fatalf("download calls after gate closed = %d, want 1", got)
	}
	if got := row(t, st, "g@g.us", "NET").Attempts; got != 0 {
		t.Fatalf("attempts after disconnect = %d, want 0", got)
	}
	if got := row(t, st, "g@g.us", "NET").NextAttemptAt; got != clock.Now().Unix() {
		t.Fatalf("next attempt after disconnect = %d, want immediate %d", got, clock.Now().Unix())
	}
}

func TestBufferedWakeDoesNotStrandExpiredBacklog(t *testing.T) {
	st := testkit.Store(t)
	clock := newClock()
	d := expiredDown()
	q := newTestQueue(t, st, d, config.MediaRules{Download: []string{"image"}, Workers: 1, RetryRequestsPerMinute: 10}, clock, nil)
	for i := 0; i < 11; i++ {
		enqueue(t, q, store.Media{ChatJID: "g@g.us", MessageID: fmt.Sprintf("P%02d", i), SenderJID: "s@lid", Source: "history", Type: "image", RawProto: []byte("raw")})
	}
	stop := start(t, q)
	eventually(t, "initial request burst", func() bool { return len(d.requests()) == 10 })
	time.Sleep(50 * time.Millisecond)
	clock.Advance(time.Minute)
	eventually(t, "request after buffered wake", func() bool { return len(d.requests()) == 11 })
	stop()
}

func TestPendingDownloadDueDuringRequestWaitRunsFirst(t *testing.T) {
	st := testkit.Store(t)
	clock := newClock()
	d := &fakeDown{download: func(raw []byte, _ string) ([]byte, error) {
		if string(raw) == "pending" {
			return []byte("ok"), nil
		}
		return nil, wa.ErrMediaExpired
	}}
	for i := 0; i < 11; i++ {
		m := store.Media{ChatJID: "g@g.us", MessageID: fmt.Sprintf("E%02d", i), SenderJID: "s@lid", Source: "history", Type: "image", State: "expired", RawProto: []byte("expired")}
		if err := testkit.InsertMedia(t.Context(), st, m); err != nil {
			t.Fatal(err)
		}
	}
	pending := store.Media{ChatJID: "g@g.us", MessageID: "DUE", Source: "live", Type: "image", State: "pending", RawProto: []byte("pending"), NextAttemptAt: clock.Now().Add(3 * time.Second).Unix()}
	if err := testkit.InsertMedia(t.Context(), st, pending); err != nil {
		t.Fatal(err)
	}

	q := newTestQueue(t, st, d, config.MediaRules{Download: []string{"image"}, Workers: 1, RetryRequestsPerMinute: 10}, clock, nil)
	stop := start(t, q)
	eventually(t, "initial request burst", func() bool { return len(d.requests()) == 10 })
	time.Sleep(50 * time.Millisecond)
	clock.Advance(3 * time.Second)
	eventually(t, "pending download at its due time", func() bool { return row(t, st, "g@g.us", "DUE").State == "done" })
	if got := len(d.requests()); got != 10 {
		t.Fatalf("re-upload requests before pending download = %d, want 10", got)
	}
	stop()
}
