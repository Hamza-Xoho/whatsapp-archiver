package wa

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types/events"
)

var t0 = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

func TestClientOutdatedSameVersionWaits30Minutes(t *testing.T) {
	var p policy
	p.next(startInput{}, t0)
	p.next(&events.Connected{}, t0)
	if got, want := p.next(&events.ClientOutdated{}, t0), (action{disconnect: true, refresh: true}); got != want {
		t.Fatalf("outdated: got %+v want %+v", got, want)
	}
	if got, want := p.next(versionRefreshed{changed: false}, t0), (action{wait: 30 * time.Minute, refresh: true}); got != want {
		t.Fatalf("same version: got %+v want %+v", got, want)
	}
	if got, want := p.next(versionRefreshed{changed: false}, t0), (action{connect: true}); got != want {
		t.Fatalf("after the 30 minute wait: got %+v want %+v", got, want)
	}
	// The next outdated event starts the cycle again: the wait is not skipped.
	p.next(&events.ClientOutdated{}, t0)
	if got, want := p.next(versionRefreshed{changed: false}, t0), (action{wait: 30 * time.Minute, refresh: true}); got != want {
		t.Fatalf("second outdated, same version: got %+v want %+v", got, want)
	}
}

func TestClientOutdatedNewVersionConnectsAtOnce(t *testing.T) {
	var p policy
	p.next(&events.ClientOutdated{}, t0)
	if got, want := p.next(versionRefreshed{changed: true}, t0), (action{connect: true}); got != want {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestTemporaryBanPausesCapture(t *testing.T) {
	var p policy
	if got, want := p.next(&events.TemporaryBan{Expire: 2 * time.Hour}, t0), (action{disconnect: true, wait: 2*time.Hour + time.Minute, connect: true}); got != want {
		t.Fatalf("ban with expiry: got %+v want %+v", got, want)
	}
	if got, want := p.next(&events.TemporaryBan{}, t0), (action{disconnect: true, wait: 24 * time.Hour, connect: true}); got != want {
		t.Fatalf("ban without expiry: got %+v want %+v", got, want)
	}
}

func TestLoggedOutStopsTheReconnectLoop(t *testing.T) {
	var p policy
	if got, want := p.next(&events.LoggedOut{}, t0), (action{stop: true}); got != want {
		t.Fatalf("policy: got %+v want %+v", got, want)
	}

	h := newHarness(t)
	h.send(t, &events.LoggedOut{})
	select {
	case err := <-h.done:
		t.Fatalf("reconnect returned %v before ctx was cancelled", err)
	default:
	}
	h.cancel()
	if err := <-h.done; err != nil {
		t.Fatalf("reconnect returned %v, want nil", err)
	}
	if n := h.sock.connects(); n != 1 {
		t.Fatalf("connects=%d after logout, want 1", n)
	}
	if n := h.refreshes(); n != 0 {
		t.Fatalf("refreshes=%d after logout, want 0", n)
	}
}

func TestStreamReplacedBacksOff(t *testing.T) {
	var p policy
	at := t0
	for _, want := range []time.Duration{5 * time.Minute, 10 * time.Minute, 20 * time.Minute, 40 * time.Minute, time.Hour, time.Hour} {
		if got := p.next(&events.StreamReplaced{}, at); got != (action{disconnect: true, wait: want, connect: true}) {
			t.Fatalf("at %v: got %+v want wait %v", at.Sub(t0), got, want)
		}
		at = at.Add(2 * time.Hour)
	}
	at = at.Add(5 * time.Hour) // more than 6 hours since the previous replace
	if got := p.next(&events.StreamReplaced{}, at); got.wait != 5*time.Minute {
		t.Fatalf("after a quiet spell: wait %v, want 5m", got.wait)
	}
}

func TestConnectFailureBacksOff(t *testing.T) {
	var p policy
	if got := p.next(startInput{}, t0); got != (action{connect: true}) {
		t.Fatalf("start: got %+v", got)
	}
	for _, want := range []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second, 40 * time.Second, 80 * time.Second, 160 * time.Second, 5 * time.Minute, 5 * time.Minute} {
		if got := p.next(connectFailed{}, t0); got != (action{wait: want, connect: true}) {
			t.Fatalf("got %+v want wait %v", got, want)
		}
	}
	for _, evt := range []any{&events.Connected{}, &events.Disconnected{}} {
		if got := p.next(evt, t0); got != (action{}) {
			t.Fatalf("%T: got %+v, want no action", evt, got)
		}
	}
	if got := p.next(connectFailed{}, t0); got.wait != 5*time.Second {
		t.Fatalf("after connected: wait %v, want reset to 5s", got.wait)
	}
}

func TestOutdatedAfterConnectedTriggersRefresh(t *testing.T) {
	h := newHarness(t)
	h.send(t, &events.Connected{})
	h.send(t, &events.ClientOutdated{})
	select {
	case <-h.refreshed:
	case <-time.After(5 * time.Second):
		t.Fatal("ClientOutdated after Connected never triggered a version refresh")
	}
	h.sock.waitConnects(t, 2)
	h.cancel()
	if err := <-h.done; err != nil {
		t.Fatalf("reconnect returned %v", err)
	}
	if h.sock.disconnects() < 1 {
		t.Fatal("outdated client was not disconnected before re-dialing")
	}
}

func TestReconnectLoopBacksOffFailedConnects(t *testing.T) {
	h := newHarness(t, errors.New("dial"), errors.New("dial"))
	h.sock.waitConnects(t, 3)
	h.cancel()
	if err := <-h.done; err != nil {
		t.Fatalf("reconnect returned %v", err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.slept) != 2 || h.slept[0] != 5*time.Second || h.slept[1] != 10*time.Second {
		t.Fatalf("slept %v, want [5s 10s]", h.slept)
	}
}

type fakeSocket struct {
	mu        sync.Mutex
	errs      []error
	nConnect  int
	nDisconn  int
	connected chan struct{}
}

func (f *fakeSocket) ConnectContext(context.Context) error {
	f.mu.Lock()
	f.nConnect++
	var err error
	if len(f.errs) > 0 {
		err, f.errs = f.errs[0], f.errs[1:]
	}
	f.mu.Unlock()
	f.connected <- struct{}{}
	return err
}
func (f *fakeSocket) Disconnect() { f.mu.Lock(); f.nDisconn++; f.mu.Unlock() }
func (f *fakeSocket) connects() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.nConnect
}
func (f *fakeSocket) disconnects() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.nDisconn
}
func (f *fakeSocket) waitConnects(t *testing.T, n int) {
	t.Helper()
	for f.connects() < n {
		select {
		case <-f.connected:
		case <-time.After(5 * time.Second):
			t.Fatalf("connects=%d, want %d", f.connects(), n)
		}
	}
}

type harness struct {
	sock      *fakeSocket
	ctl       chan any
	cancel    context.CancelFunc
	done      chan error
	refreshed chan struct{}
	mu        sync.Mutex
	slept     []time.Duration
	nRefresh  int
}

func (h *harness) send(t *testing.T, evt any) {
	t.Helper()
	select {
	case h.ctl <- evt:
	case <-time.After(5 * time.Second):
		t.Fatalf("reconnect loop is no longer reading control events (sending %T)", evt)
	}
}

func (h *harness) refreshes() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.nRefresh
}

// newHarness runs the reconnect loop against a fake socket whose connects fail with errs in turn.
// The control channel is unbuffered, so a completed send means the loop has taken the event.
func newHarness(t *testing.T, errs ...error) *harness {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	h := &harness{
		sock:      &fakeSocket{errs: errs, connected: make(chan struct{}, 64)},
		ctl:       make(chan any),
		cancel:    cancel,
		done:      make(chan error, 1),
		refreshed: make(chan struct{}, 64),
	}
	c := &Client{
		sock: h.sock,
		log:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		refresh: func(context.Context) (bool, error) {
			h.mu.Lock()
			h.nRefresh++
			h.mu.Unlock()
			h.refreshed <- struct{}{}
			return true, nil
		},
		sleep: func(_ context.Context, d time.Duration) error {
			h.mu.Lock()
			h.slept = append(h.slept, d)
			h.mu.Unlock()
			return nil
		},
	}
	exited := make(chan struct{})
	go func() { defer close(exited); h.done <- c.reconnect(ctx, h.ctl) }()
	t.Cleanup(func() { cancel(); <-exited })
	h.sock.waitConnects(t, 1)
	return h
}
