package media

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types/events"
	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
	"whatsapp-archiver/internal/wa"
)

// fakeDown is a goroutine-safe Downloader; the hooks decide each call's result.
type fakeDown struct {
	mu       sync.Mutex
	order    []string // raw proto of every Download call, in call order
	paths    []string // direct path override of every Download call
	refs     []wa.MediaRef
	download func(raw []byte, path string) ([]byte, error)
	request  func(ref wa.MediaRef, raw []byte) error
}

func (d *fakeDown) Download(_ context.Context, raw []byte, path string) ([]byte, error) {
	d.mu.Lock()
	d.order = append(d.order, string(raw))
	d.paths = append(d.paths, path)
	fn := d.download
	d.mu.Unlock()
	if fn == nil {
		return []byte("data"), nil
	}
	return fn(raw, path)
}
func (d *fakeDown) RequestMediaRetry(_ context.Context, ref wa.MediaRef, raw []byte) error {
	d.mu.Lock()
	d.refs = append(d.refs, ref)
	fn := d.request
	d.mu.Unlock()
	if fn == nil {
		return nil
	}
	return fn(ref, raw)
}
func (d *fakeDown) ApplyMediaRetry(*events.MediaRetry, []byte) (string, error) { return "new", nil }
func (d *fakeDown) calls() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.order...)
}
func (d *fakeDown) requests() []wa.MediaRef {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]wa.MediaRef(nil), d.refs...)
}

// testClock is a goroutine-safe fake clock that counts reads, so a test can wait for later worker passes.
type testClock struct {
	mu    sync.Mutex
	now   time.Time
	reads int
}

func newClock() *testClock { return &testClock{now: time.Unix(1_700_000_000, 0)} }
func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reads++
	return c.now
}
func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}
func (c *testClock) Reads() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reads
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newTestQueue(t *testing.T, st *store.Store, d Downloader, r config.MediaRules, c Clock, onPanic func(string)) *Queue {
	t.Helper()
	if st == nil {
		st = testkit.Store(t)
	}
	return NewQueue(t.TempDir(), r, st, d, c, quietLog(), onPanic)
}

// start runs q until the returned stop is called (or the test ends); stop waits for Run to return.
func start(t *testing.T, q *Queue) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { q.Run(ctx); close(done) }()
	var once sync.Once
	stop := func() { once.Do(func() { cancel(); <-done }) }
	t.Cleanup(stop)
	return stop
}

// eventually polls cond until it holds; it fails the test after 5 s.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	tick := time.NewTicker(2 * time.Millisecond)
	defer tick.Stop()
	for !cond() {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
		case <-tick.C:
		}
	}
}

func row(t *testing.T, st *store.Store, chat, id string) store.Media {
	t.Helper()
	m, err := st.MediaByID(context.Background(), chat, id)
	if err != nil {
		t.Fatalf("media %s/%s: %v", chat, id, err)
	}
	return m
}

func enqueue(t *testing.T, q *Queue, m store.Media) {
	t.Helper()
	if m.State == "" {
		m.State = "pending"
	}
	if err := q.Enqueue(t.Context(), m); err != nil {
		t.Fatal(err)
	}
}
