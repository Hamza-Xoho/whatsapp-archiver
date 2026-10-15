package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// runService runs the background loops (media queue, health monitor, backup scheduler) and then main (the client's
// Run). When main returns, whether on SIGTERM or by itself, it cancels the shared context, waits for every loop to
// finish, and only then calls the closers in order. serve passes the handler drain first, then the client and the
// store: no loop writes to the store after it is closed, and an event handler still running (a history chunk being
// imported) is waited for up to handlerDrainBound; one still running after that can hit the closed store. The error
// is main's, joined with any closer errors.
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
	after   func(time.Duration) <-chan time.Time // the bound's timer; nil means time.After
	mu      sync.Mutex
	closing bool
	wg      sync.WaitGroup
}

// wrap returns h counted as in flight while it runs. Once drain has started, an arriving event is not handled: the
// store is about to close.
func (f *inFlight) wrap(h func(any)) func(any) {
	return func(evt any) {
		f.mu.Lock()
		if f.closing {
			f.mu.Unlock()
			return
		}
		f.wg.Add(1)
		f.mu.Unlock()
		defer f.wg.Done()
		h(evt)
	}
}

// drain waits for the handlers in flight, at most handlerDrainBound; a handler still running then is reported and
// keeps running while the client and the store are closed.
func (f *inFlight) drain() error {
	f.mu.Lock()
	f.closing = true // no Add after this point, so Wait never races an Add from zero
	f.mu.Unlock()
	done := make(chan struct{})
	go func() { f.wg.Wait(); close(done) }() // ends when the last handler does
	after := f.after
	if after == nil {
		after = time.After
	}
	select {
	case <-done:
		return nil
	case <-after(handlerDrainBound):
		return fmt.Errorf("an event handler was still running %s after shutdown began; closing anyway", handlerDrainBound)
	}
}
