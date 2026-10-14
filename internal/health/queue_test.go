package health

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"whatsapp-archiver/internal/config"
)

// stuck is an unreachable ntfy: Notify returns only after its context ends and finish is closed.
type stuck struct {
	started      chan string
	finish, gone chan struct{}
	mu           sync.Mutex
	errs         []error
}

func newStuck(t *testing.T) *stuck {
	s := &stuck{started: make(chan string, 128), finish: make(chan struct{}), gone: make(chan struct{})}
	t.Cleanup(func() { close(s.gone) })
	return s
}
func (s *stuck) Notify(ctx context.Context, _, body string) error {
	s.started <- body
	select {
	case <-ctx.Done():
	case <-s.gone:
		return errors.New("test over")
	}
	<-s.finish
	s.mu.Lock()
	s.errs = append(s.errs, ctx.Err())
	s.mu.Unlock()
	return ctx.Err()
}

func TestSlowNotifierDoesNotBlockSetState(t *testing.T) {
	s := newStuck(t)
	m := NewMonitor(&config.Config{DataDir: t.TempDir()}, s, &clock{time.Unix(1_700_000_000, 0)}, nil, func() string { return "v" })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ran := make(chan struct{})
	go func() { defer close(ran); m.Run(ctx) }()
	returned := make(chan struct{})
	go func() {
		defer close(returned)
		m.SetState("logged_out", "401")
		m.WriteFailed(errors.New("disk I/O error"))
		for i := 0; i < 40; i++ { // more alerts than the queue holds
			m.Panic(fmt.Sprint("worker", i))
		}
	}()
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("SetState/WriteFailed/Panic blocked on a notifier that never answers")
	}
	<-s.started // the sender is inside Notify
	cancel()    // SIGTERM
	select {
	case <-ran:
		t.Fatal("Run returned while its alert sender was still inside Notify")
	case <-time.After(100 * time.Millisecond):
	}
	close(s.finish)
	select {
	case <-ran:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.errs) == 0 || !errors.Is(s.errs[0], context.Canceled) {
		t.Fatalf("Notify must get Run's context, which SIGTERM cancels; errs=%v", s.errs)
	}
}

func TestAlertsRaisedBeforeRunAreSent(t *testing.T) {
	n := &note{}
	m := NewMonitor(&config.Config{DataDir: t.TempDir()}, n, &clock{time.Unix(1_700_000_000, 0)}, nil, func() string { return "v" })
	m.SetState("logged_out", "401")
	stop := running(t, m)
	stop()
	wantBodies(t, n.bodies, "401")
}

func TestFullAlertQueueDropsAndLogs(t *testing.T) {
	var logs bytes.Buffer
	m := NewMonitor(&config.Config{DataDir: t.TempDir()}, newStuck(t), &clock{time.Unix(1_700_000_000, 0)}, nil, func() string { return "v" }).
		WithLogger(slog.New(slog.NewTextHandler(&logs, nil)))
	returned := make(chan struct{})
	go func() {
		defer close(returned)
		for i := 0; i < 20; i++ { // Run is not started, so nothing leaves the queue
			m.Panic(fmt.Sprint("worker", i))
		}
	}()
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("Panic blocked on a full alert queue")
	}
	if got := strings.Count(logs.String(), "level=ERROR"); got != 4 {
		t.Fatalf("want 4 dropped alerts logged at Error (queue holds 16), got %d:\n%s", got, logs.String())
	}
	if !strings.Contains(logs.String(), "worker19") {
		t.Fatalf("drop log must say which alert was dropped:\n%s", logs.String())
	}
}

type failing struct{}

func (failing) Notify(context.Context, string, string) error { return errors.New("ntfy status 503") }

func TestFailedNotifyIsLoggedOncePerSend(t *testing.T) {
	var logs bytes.Buffer
	m := NewMonitor(&config.Config{DataDir: t.TempDir()}, failing{}, &clock{time.Unix(1_700_000_000, 0)}, nil, func() string { return "v" }).
		WithLogger(slog.New(slog.NewTextHandler(&logs, nil)))
	stop := running(t, m)
	m.SetState("logged_out", "401")
	m.WriteFailed(errors.New("disk I/O error"))
	m.Panic("handler")
	stop()
	out := logs.String()
	if got := strings.Count(out, "level=ERROR"); got != 3 {
		t.Fatalf("want 3 Error logs, one per failed send, got %d:\n%s", got, out)
	}
	for _, title := range []string{"WhatsApp logged out", "Archive write failed", "Recovered panic"} {
		if !strings.Contains(out, title) {
			t.Fatalf("log must name the alert %q:\n%s", title, out)
		}
	}
	if strings.Count(out, "ntfy status 503") != 3 {
		t.Fatalf("log must carry the error:\n%s", out)
	}
}

func TestDroppedAlertFiresAgainOnNextTrigger(t *testing.T) {
	n := &note{}
	m := NewMonitor(&config.Config{DataDir: t.TempDir()}, n, &clock{time.Unix(1_700_000_000, 0)}, nil, func() string { return "v" }).
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
	for i := 0; i < 16; i++ { // Run is not started: fill the queue
		m.Panic(fmt.Sprint("worker", i))
	}
	m.SetState("logged_out", "401") // dropped
	running(t, m)()                 // drain the queue
	m.SetState("logged_out", "401") // next trigger
	running(t, m)()
	if len(n.bodies) != 17 || !strings.Contains(n.bodies[16], "401") {
		t.Fatalf("a dropped logged_out must be sent on its next trigger; sent %d: %q", len(n.bodies), n.bodies)
	}
}
