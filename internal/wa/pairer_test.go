package wa

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	wmstore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// pairRig is a Pairer on a fake socket and fake QR channels: each GetQRChannel call takes the next channel.
type pairRig struct {
	p      *Pairer
	sock   *fakeSocket
	device *wmstore.Device
	mu     sync.Mutex
	qrs    []chan whatsmeow.QRChannelItem
	nQR    int
	cancel context.CancelFunc
	done   chan error
}

func newPairRig(t *testing.T, paired bool) *pairRig {
	t.Helper()
	r := &pairRig{sock: &fakeSocket{connected: make(chan struct{}, 64)}, device: &wmstore.Device{}, done: make(chan error, 1)}
	if paired {
		own := types.NewJID("15550001111", types.DefaultUserServer)
		r.device.ID = &own
	}
	for range 3 {
		r.qrs = append(r.qrs, make(chan whatsmeow.QRChannelItem, 8))
	}
	c := &Client{
		client:  whatsmeow.NewClient(r.device, nil),
		sock:    r.sock,
		log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		refresh: func(context.Context) (bool, error) { return false, nil },
		sleep:   func(context.Context, time.Duration) error { return nil },
		qrChannel: func(context.Context) (<-chan whatsmeow.QRChannelItem, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			ch := r.qrs[r.nQR]
			r.nQR++
			return ch, nil
		},
	}
	r.p = NewPairer(c)
	ctx, cancel := context.WithCancel(t.Context())
	r.cancel = cancel
	exited := make(chan struct{})
	go func() { defer close(exited); r.done <- r.p.Run(ctx, func(any) bool { return true }) }()
	t.Cleanup(func() { cancel(); <-exited })
	return r
}

func (r *pairRig) qrCalls() int { r.mu.Lock(); defer r.mu.Unlock(); return r.nQR }

func recv(t *testing.T, ch <-chan PairUpdate) (PairUpdate, bool) {
	t.Helper()
	select {
	case u, ok := <-ch:
		return u, ok
	case <-time.After(5 * time.Second):
		t.Fatal("no pairing update within 5 s")
		return PairUpdate{}, false
	}
}

func code(c string) whatsmeow.QRChannelItem {
	return whatsmeow.QRChannelItem{Event: whatsmeow.QRChannelEventCode, Code: c, Timeout: time.Minute}
}

// FR-1: nothing connects until pairing is started; then each QR code reaches the channel (not the terminal), then
// Done on success, and capture carries on in the same run.
func TestPairingStreamsQRThenDone(t *testing.T) {
	r := newPairRig(t, false)
	time.Sleep(100 * time.Millisecond)
	if n := r.sock.connects(); n != 0 {
		t.Fatalf("connects=%d before pairing was started, want 0", n)
	}
	select {
	case <-r.p.Ready():
		t.Fatal("ready before pairing")
	default:
	}
	ch, err := r.p.StartPairing(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	r.sock.waitConnects(t, 1)
	qr := r.qrs[0]
	qr <- code("2@one")
	qr <- code("2@two")
	own := types.NewJID("15550001111", types.DefaultUserServer)
	r.device.ID = &own
	qr <- whatsmeow.QRChannelSuccess
	close(qr)
	for _, want := range []PairUpdate{{QR: "2@one"}, {QR: "2@two"}, {Done: true}} {
		if got, ok := recv(t, ch); !ok || got != want {
			t.Fatalf("update=%+v open=%v, want %+v", got, ok, want)
		}
	}
	if u, ok := recv(t, ch); ok {
		t.Fatalf("update %+v after Done, want the channel closed", u)
	}
	select {
	case <-r.p.Ready():
	case <-time.After(5 * time.Second):
		t.Fatal("not ready after Done")
	}
	if !r.p.Paired() {
		t.Fatal("Paired()=false after Done")
	}
	select {
	case err := <-r.done:
		t.Fatalf("Run returned %v after pairing; capture must carry on", err)
	case <-time.After(100 * time.Millisecond):
	}
	if n := r.sock.connects(); n != 1 {
		t.Fatalf("connects=%d, want 1", n)
	}
	r.cancel()
	if err := <-r.done; err != nil {
		t.Fatalf("Run returned %v on stop, want nil", err)
	}
}

// Only one pairing runs at a time: a second call gets the running channel and starts nothing.
func TestSecondStartPairingReusesRun(t *testing.T) {
	r := newPairRig(t, false)
	ch1, err := r.p.StartPairing(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	r.sock.waitConnects(t, 1)
	ch2, err := r.p.StartPairing(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if ch1 != ch2 {
		t.Fatal("second StartPairing returned a new channel")
	}
	r.qrs[0] <- code("2@one")
	if got, _ := recv(t, ch1); got.QR != "2@one" {
		t.Fatalf("update=%+v", got)
	}
	time.Sleep(100 * time.Millisecond)
	if n, q := r.sock.connects(), r.qrCalls(); n != 1 || q != 1 {
		t.Fatalf("connects=%d QR channels=%d after a second start, want 1 and 1", n, q)
	}
}

// A failed pairing ends its channel with pairLine's text, disconnects, and can be started again.
func TestFailedPairingCanBeStartedAgain(t *testing.T) {
	r := newPairRig(t, false)
	ch, err := r.p.StartPairing(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	r.sock.waitConnects(t, 1)
	r.qrs[0] <- whatsmeow.QRChannelTimeout
	close(r.qrs[0])
	if got, ok := recv(t, ch); !ok || got != (PairUpdate{Err: "QR code timed out; run `archiver pair` again."}) {
		t.Fatalf("update=%+v open=%v", got, ok)
	}
	if u, ok := recv(t, ch); ok {
		t.Fatalf("update %+v after the error, want the channel closed", u)
	}
	ch2, err := r.p.StartPairing(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if ch2 == ch {
		t.Fatal("a new pairing reused the failed channel")
	}
	r.sock.waitConnects(t, 2)
	r.qrs[1] <- code("2@again")
	if got, _ := recv(t, ch2); got.QR != "2@again" {
		t.Fatalf("update=%+v", got)
	}
	select {
	case <-r.p.Ready():
		t.Fatal("ready after a failed pairing")
	default:
	}
}

// Already paired: Run captures at once, Ready is closed, and pairing cannot be started.
func TestPairerRunCapturesAtOnceWhenPaired(t *testing.T) {
	r := newPairRig(t, true)
	r.sock.waitConnects(t, 1)
	select {
	case <-r.p.Ready():
	case <-time.After(5 * time.Second):
		t.Fatal("not ready when already paired")
	}
	if _, err := r.p.StartPairing(t.Context()); err == nil {
		t.Fatal("StartPairing on a paired device succeeded")
	}
	if n := r.qrCalls(); n != 0 {
		t.Fatalf("QR channels=%d when paired, want 0", n)
	}
}

// Pairing ends when Run stops: the channel is closed and a later start is refused.
func TestPairingChannelClosesWhenRunStops(t *testing.T) {
	r := newPairRig(t, false)
	ch, err := r.p.StartPairing(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	r.sock.waitConnects(t, 1)
	r.cancel()
	if err := <-r.done; err != nil {
		t.Fatalf("Run returned %v", err)
	}
	for {
		if _, ok := recv(t, ch); !ok {
			break
		}
	}
	if _, err := r.p.StartPairing(t.Context()); err == nil {
		t.Fatal("StartPairing after Run stopped succeeded")
	}
}

// Fix round 1: Paired is read from HTTP goroutines while pairing completes; with -race the reads must not touch the
// device whatsmeow writes.
func TestPairedIsSafeToReadWhilePairingCompletes(t *testing.T) {
	r := newPairRig(t, false)
	if r.p.Paired() {
		t.Fatal("paired before pairing")
	}
	ch, err := r.p.StartPairing(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	r.sock.waitConnects(t, 1)
	stop := make(chan struct{})
	readers := make(chan struct{})
	go func() {
		defer close(readers)
		for {
			select {
			case <-stop:
				return
			default:
				_ = r.p.Paired()
			}
		}
	}()
	time.Sleep(10 * time.Millisecond)
	own := types.NewJID("15550001111", types.DefaultUserServer)
	r.device.ID = &own // what handlePair does, on whatsmeow's goroutine
	r.qrs[0] <- whatsmeow.QRChannelSuccess
	for {
		if u, ok := recv(t, ch); !ok || u.Done {
			break
		}
	}
	close(stop)
	<-readers
	if !r.p.Paired() {
		t.Fatal("not paired after Done")
	}
}

// Fix round 1: Paired follows PairSuccess and LoggedOut seen by run's handler, and passes the verdict on.
func TestTrackPairingFollowsPairSuccessAndLogout(t *testing.T) {
	c := &Client{client: whatsmeow.NewClient(&wmstore.Device{}, nil)}
	h := c.trackPairing(func(any) bool { return false })
	if c.Paired() {
		t.Fatal("paired at start")
	}
	if h(&events.PairSuccess{}) || !c.Paired() {
		t.Fatalf("after PairSuccess: paired=%v (verdict must stay false)", c.Paired())
	}
	h(&events.LoggedOut{})
	if c.Paired() {
		t.Fatal("still paired after LoggedOut")
	}
}
