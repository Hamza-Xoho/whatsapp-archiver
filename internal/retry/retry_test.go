package retry_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"whatsapp-archiver/internal/retry"
)

func TestRetryStopsOnSuccess(t *testing.T) {
	var calls int
	var sleeps []time.Duration
	attempts, err := retry.Do(context.Background(), retry.Policy{Max: 5, Base: time.Second, Sleep: func(d time.Duration) { sleeps = append(sleeps, d) }}, func() error {
		calls++
		if calls < 3 {
			return errors.New("again")
		}
		return nil
	})
	if err != nil || attempts != 3 {
		t.Fatalf("attempts=%d err=%v", attempts, err)
	}
	if len(sleeps) != 2 || sleeps[0] != time.Second || sleeps[1] != 2*time.Second {
		t.Fatalf("sleeps=%v", sleeps)
	}
}

func TestRetryGivesUpAtMax(t *testing.T) {
	attempts, err := retry.Do(context.Background(), retry.Policy{Max: 5, Sleep: func(time.Duration) {}}, func() error { return errors.New("broken") })
	if attempts != 5 || err == nil {
		t.Fatalf("attempts=%d err=%v", attempts, err)
	}
}
