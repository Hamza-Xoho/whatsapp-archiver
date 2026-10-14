package media

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

var errReset = errors.New("connection reset")

// sleepLog records the backoff waits of a queue.
type sleepLog struct {
	mu sync.Mutex
	d  []time.Duration
}

func (s *sleepLog) sleep(d time.Duration) {
	s.mu.Lock()
	s.d = append(s.d, d)
	s.mu.Unlock()
}
func (s *sleepLog) waits() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Duration(nil), s.d...)
}

func equalDurations(a, b []time.Duration) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// M4: a transient failure is retried with seconds-scale backoff (1+2+4+8 s); after the round the row waits as
// pending for 15 minutes on the queue clock, is not claimed before, and is downloaded once that time has come.
func TestTransientFailureIsRetriedAfterBackoff(t *testing.T) {
	st := testkit.Store(t)
	clock := newClock()
	var failing atomic.Bool
	failing.Store(true)
	d := &fakeDown{download: func([]byte, string) ([]byte, error) {
		if failing.Load() {
			return nil, errReset
		}
		return []byte("ok"), nil
	}}
	q := newTestQueue(t, st, d, config.MediaRules{Download: []string{"image"}, Workers: 1}, clock, nil)
	waits := &sleepLog{}
	q.sleep = waits.sleep
	enqueue(t, q, store.Media{ChatJID: "123@g.us", MessageID: "NET", Type: "image", RawProto: []byte("net")})
	stop := start(t, q)
	due := clock.Now().Add(15 * time.Minute).Unix()
	eventually(t, "row re-queued after one round", func() bool {
		m := row(t, st, "123@g.us", "NET")
		return m.State == "pending" && m.Attempts == 5
	})
	if m := row(t, st, "123@g.us", "NET"); m.NextAttemptAt != due || !strings.Contains(m.Reason, "connection reset") {
		t.Fatalf("row=%+v, want next attempt at %d", m, due)
	}
	if w := waits.waits(); !equalDurations(w, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}) {
		t.Fatalf("backoff waits = %v", w)
	}
	reads := clock.Reads()
	eventually(t, "later worker passes", func() bool { return clock.Reads() >= reads+20 })
	if n := len(d.calls()); n != 5 {
		t.Fatalf("downloads before the re-queue time = %d, want 5", n)
	}
	failing.Store(false)
	clock.Advance(15 * time.Minute)
	eventually(t, "row done", func() bool { return row(t, st, "123@g.us", "NET").State == "done" })
	stop()
	if m := row(t, st, "123@g.us", "NET"); m.Attempts != 6 {
		t.Fatalf("attempts = %d, want 6", m.Attempts)
	}
}

// Task 7: fails twice then succeeds → done with attempts 3.
func TestAttemptsCountedAcrossRetries(t *testing.T) {
	st := testkit.Store(t)
	var n atomic.Int32
	d := &fakeDown{download: func([]byte, string) ([]byte, error) {
		if n.Add(1) <= 2 {
			return nil, errReset
		}
		return []byte("ok"), nil
	}}
	q := newTestQueue(t, st, d, config.MediaRules{Download: []string{"image"}, Workers: 1}, newClock(), nil)
	enqueue(t, q, store.Media{ChatJID: "123@g.us", MessageID: "TWICE", Type: "image", RawProto: []byte("x")})
	stop := start(t, q)
	eventually(t, "row done", func() bool { return row(t, st, "123@g.us", "TWICE").State == "done" })
	stop()
	if m := row(t, st, "123@g.us", "TWICE"); m.Attempts != 3 || m.Path == "" {
		t.Fatalf("row=%+v", m)
	}
}

// Task 7 (M4 ruling): a download that always fails ends failed with its reason after 20 attempts (4 rounds of 5).
func TestAlwaysFailingDownloadFailsAfterTwentyAttempts(t *testing.T) {
	st := testkit.Store(t)
	clock := newClock()
	d := &fakeDown{download: func([]byte, string) ([]byte, error) { return nil, errReset }}
	q := newTestQueue(t, st, d, config.MediaRules{Download: []string{"image"}, Workers: 1}, clock, nil)
	enqueue(t, q, store.Media{ChatJID: "123@g.us", MessageID: "BAD", Type: "image", RawProto: []byte("x")})
	stop := start(t, q)
	for round := 1; round <= 4; round++ {
		eventually(t, "round finished", func() bool {
			m := row(t, st, "123@g.us", "BAD")
			return m.Attempts == 5*round && (m.State == "pending" || m.State == "failed")
		})
		clock.Advance(15 * time.Minute)
	}
	eventually(t, "row failed", func() bool { return row(t, st, "123@g.us", "BAD").State == "failed" })
	reads := clock.Reads()
	eventually(t, "later worker passes", func() bool { return clock.Reads() >= reads+20 })
	stop()
	m := row(t, st, "123@g.us", "BAD")
	if m.Attempts != 20 || !strings.Contains(m.Reason, "connection reset") || len(d.calls()) != 20 {
		t.Fatalf("row=%+v downloads=%d", m, len(d.calls()))
	}
}

// Expired media is not retried within a round: one download, then the re-upload path.
func TestExpiredMediaIsNotRetriedWithinARound(t *testing.T) {
	st := testkit.Store(t)
	q := newTestQueue(t, st, expiredDown(), config.MediaRules{Download: []string{"image"}, Workers: 1}, newClock(), nil)
	d := q.down.(*fakeDown)
	enqueue(t, q, store.Media{ChatJID: "123@g.us", MessageID: "GONE", SenderJID: "5@lid", Type: "image", RawProto: []byte("x")})
	stop := start(t, q)
	eventually(t, "re-upload requested", func() bool { return row(t, st, "123@g.us", "GONE").State == "retry_requested" })
	stop()
	if m := row(t, st, "123@g.us", "GONE"); m.Attempts != 1 || len(d.calls()) != 1 {
		t.Fatalf("row=%+v downloads=%d", m, len(d.calls()))
	}
}
