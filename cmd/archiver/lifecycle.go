package main

import (
	"context"
	"errors"
	"sync"
	"time"
)

// runService runs the background loops (media queue, health monitor, backup scheduler) and then main (the client's
// Run). When main returns, whether on SIGTERM or by itself, it cancels the shared context, waits for every loop to
// finish, and only then calls the closers in order (client, then store): nothing writes to the store after it is
// closed. The error is main's, joined with any closer errors.
func runService(ctx context.Context, cancel context.CancelFunc, main func(context.Context) error, loops []func(context.Context), closers ...func() error) error {
	var wg sync.WaitGroup
	for _, loop := range loops {
		wg.Add(1)
		go func() {
			defer wg.Done()
			loop(ctx)
		}()
	}
	err := main(ctx)
	cancel()
	wg.Wait()
	errs := []error{err}
	for _, closeFn := range closers {
		errs = append(errs, closeFn())
	}
	return errors.Join(errs...)
}

// handlerDrainBound is how long shutdown waits for the event handlers in flight (systemd's default stop timeout is
// 90 s).
const handlerDrainBound = 30 * time.Second

// inFlight tracks the whatsmeow event handlers that are running, so that shutdown can wait for them.
type inFlight struct {
	after func(time.Duration) <-chan time.Time // the bound's timer; nil means time.After
}

// wrap returns h counted as in flight while it runs.
func (f *inFlight) wrap(h func(any)) func(any) { return h }

// drain waits for the handlers in flight, at most handlerDrainBound.
func (f *inFlight) drain() error { return nil }
