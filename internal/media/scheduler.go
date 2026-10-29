package media

import (
	"context"
	"time"

	"whatsapp-archiver/internal/store"
)

type mediaStore interface {
	Apply(context.Context, store.Batch) (store.Applied, error)
	ResetMediaOnStart(context.Context, time.Time) error
	ClaimMedia(context.Context, time.Time) (*store.Media, error)
	NextMediaDue(context.Context) (time.Time, bool, error)
	NextExpiredMedia(context.Context) (*store.Media, error)
	FailStaleRetries(context.Context, time.Time) error
	UpdateMedia(context.Context, store.Media) error
	MediaByID(context.Context, string, string) (store.Media, error)
}

type timerClock interface {
	After(time.Duration) <-chan time.Time
}

func (q *Queue) after(delay time.Duration) <-chan time.Time {
	if delay < 0 {
		delay = 0
	}
	if clock, ok := q.clock.(timerClock); ok {
		return clock.After(delay)
	}
	return time.After(delay)
}

// waitForWork blocks until a row is due, a producer wakes the queue, or the context ends.
func (q *Queue) waitForWork(ctx context.Context) bool {
	due, ok, err := q.store.NextMediaDue(ctx)
	if err != nil {
		q.log.Error("next media due time not read", "err", err)
		return q.waitDelay(ctx, time.Minute)
	}
	if staleAt := q.nextStale.Load(); staleAt > 0 {
		stale := time.Unix(staleAt, 0)
		if !ok || stale.Before(due) {
			due, ok = stale, true
		}
	}
	if requestAt := q.nextRequest.Load(); requestAt > 0 {
		retry := time.Unix(0, requestAt)
		if !ok || retry.Before(due) {
			due, ok = retry, true
		}
	}
	if ok {
		delay := due.Sub(q.clock.Now())
		if delay <= 0 {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-q.wake:
			return true
		case <-q.after(delay):
			return true
		}
	}
	select {
	case <-ctx.Done():
		return false
	case <-q.wake:
		return true
	}
}

func (q *Queue) waitDelay(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		return true
	}
	select {
	case <-ctx.Done():
		return false
	case <-q.after(delay):
		return true
	}
}

// waitDelayOrWake lets newly queued live media and connection-state changes preempt a limiter reservation.
func (q *Queue) waitDelayOrWake(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		return true
	}
	select {
	case <-ctx.Done():
		return false
	case <-q.wake:
		return false
	case <-q.after(delay):
		return true
	}
}
