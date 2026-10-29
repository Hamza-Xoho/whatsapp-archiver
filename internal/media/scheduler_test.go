package media

import (
	"context"
	"database/sql"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

// Ten requests at start plus ten over the first minute require a read and a state write each; five calls cover
// startup and due-time scheduling without allowing a polling loop.
const idleExpiredStoreCallsPerMinute = 45

type countingMediaStore struct {
	*store.Store
	calls      atomic.Int64
	staleCalls atomic.Int64
}

func (s *countingMediaStore) ResetMediaOnStart(ctx context.Context, now time.Time) error {
	s.calls.Add(1)
	return s.Store.ResetMediaOnStart(ctx, now)
}
func (s *countingMediaStore) ClaimMedia(ctx context.Context, now time.Time) (*store.Media, error) {
	s.calls.Add(1)
	return s.Store.ClaimMedia(ctx, now)
}
func (s *countingMediaStore) NextMediaDue(ctx context.Context) (time.Time, bool, error) {
	s.calls.Add(1)
	return s.Store.NextMediaDue(ctx)
}
func (s *countingMediaStore) NextExpiredMedia(ctx context.Context) (*store.Media, error) {
	s.calls.Add(1)
	return s.Store.NextExpiredMedia(ctx)
}
func (s *countingMediaStore) FailStaleRetries(ctx context.Context, now time.Time) error {
	s.calls.Add(1)
	s.staleCalls.Add(1)
	return s.Store.FailStaleRetries(ctx, now)
}
func (s *countingMediaStore) UpdateMedia(ctx context.Context, m store.Media) error {
	s.calls.Add(1)
	return s.Store.UpdateMedia(ctx, m)
}

func TestStaleRetryCleanupWakesAnOtherwiseIdleQueueHourly(t *testing.T) {
	st := testkit.Store(t)
	clock := newClock()
	counted := &countingMediaStore{Store: st}
	q := newTestQueue(t, st, &fakeDown{}, config.MediaRules{Download: []string{"image"}, Workers: 1}, clock, nil)
	q.store = counted
	stop := start(t, q)
	eventually(t, "scheduler to become idle", func() bool { return counted.calls.Load() >= 2 })
	clock.Advance(time.Hour)
	eventually(t, "hourly stale cleanup", func() bool { return counted.staleCalls.Load() == 1 })
	stop()
}
func (s *countingMediaStore) MediaByID(ctx context.Context, chat, id string) (store.Media, error) {
	s.calls.Add(1)
	return s.Store.MediaByID(ctx, chat, id)
}

func TestIdleExpiredBacklogHasBoundedStoreCalls(t *testing.T) {
	st := testkit.Store(t)
	db, err := sql.Open("sqlite", "file:"+st.Path()+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	stmt, err := tx.Prepare(`INSERT INTO media(chat_jid,message_id,source_rank,type,state) VALUES('g@g.us',?,1,'image','expired')`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10_000; i++ {
		if _, err = stmt.Exec(fmt.Sprintf("E%05d", i)); err != nil {
			t.Fatal(err)
		}
	}
	_ = stmt.Close()
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}

	clock := newClock()
	d := expiredDown()
	counted := &countingMediaStore{Store: st}
	q := newTestQueue(t, st, d, config.MediaRules{Download: []string{"image"}, Workers: 1, RetryRequestsPerMinute: 10}, clock, nil)
	q.store = counted
	q.Wake()
	stop := start(t, q)
	eventually(t, "initial request burst", func() bool { return len(d.requests()) == 10 })
	for requests := 11; requests <= 20; requests++ {
		eventually(t, "next limiter timer", func() bool {
			clock.mu.Lock()
			defer clock.mu.Unlock()
			for _, timer := range clock.timers {
				if !timer.at.After(clock.now.Add(6 * time.Second)) {
					return true
				}
			}
			return false
		})
		clock.Advance(6 * time.Second)
		eventually(t, fmt.Sprintf("%d requests through normal limiter ticks", requests), func() bool { return len(d.requests()) >= requests })
	}
	eventually(t, "reservation after twentieth request", func() bool {
		clock.mu.Lock()
		defer clock.mu.Unlock()
		for _, timer := range clock.timers {
			if !timer.at.After(clock.now.Add(6 * time.Second)) {
				return true
			}
		}
		return false
	})
	stop()
	if got := counted.calls.Load(); got > idleExpiredStoreCallsPerMinute {
		t.Fatalf("store calls in one fake minute = %d, want <= %d", got, idleExpiredStoreCallsPerMinute)
	}
}
