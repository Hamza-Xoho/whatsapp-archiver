package retry

import (
	"context"
	"fmt"
	"time"
)

type Policy struct {
	Max   int
	Base  time.Duration
	Sleep func(time.Duration)
	// Permanent reports errors that retrying cannot fix; Do returns them at once.
	Permanent func(error) bool
}

func Do(ctx context.Context, policy Policy, fn func() error) (int, error) {
	if policy.Max < 1 {
		policy.Max = 1
	}
	if policy.Base <= 0 {
		policy.Base = time.Second
	}
	for attempt := 1; attempt <= policy.Max; attempt++ {
		if err := fn(); err != nil {
			if policy.Permanent != nil && policy.Permanent(err) {
				return attempt, err
			}
			if attempt == policy.Max {
				return attempt, fmt.Errorf("after %d attempts: %w", attempt, err)
			}
			delay := policy.Base << (attempt - 1)
			if policy.Sleep != nil {
				policy.Sleep(delay)
			} else {
				timer := time.NewTimer(delay)
				select {
				case <-ctx.Done():
					timer.Stop()
					return attempt, ctx.Err()
				case <-timer.C:
				}
			}
			continue
		}
		return attempt, nil
	}
	return 0, nil
}
