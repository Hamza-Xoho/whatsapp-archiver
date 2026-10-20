package web

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"whatsapp-archiver/internal/activity"
	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/health"
	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/testkit"
)

// sseEvent is one parsed Server-Sent Event.
type sseEvent struct{ id, name, data string }

// readEvents parses the stream into events until the body ends or the test is over.
func readEvents(ctx context.Context, body io.Reader) <-chan sseEvent {
	out := make(chan sseEvent, 64)
	go func() {
		defer close(out)
		sc := bufio.NewScanner(body)
		var e sseEvent
		var data []string
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				if e.name != "" || len(data) > 0 {
					e.data = strings.Join(data, "\n")
					select {
					case out <- e:
					case <-ctx.Done():
						return
					}
				}
				e, data = sseEvent{}, nil
			case strings.HasPrefix(line, "id: "):
				e.id = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "event: "):
				e.name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				data = append(data, strings.TrimPrefix(line, "data: "))
			}
		}
	}()
	return out
}

// next returns the next event called name within d, failing the test otherwise.
func next(t *testing.T, events <-chan sseEvent, name string, d time.Duration) sseEvent {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case e, ok := <-events:
			if !ok {
				t.Fatalf("stream ended before a %q event", name)
			}
			if e.name == name {
				return e
			}
		case <-deadline:
			t.Fatalf("no %q event within %s", name, d)
		}
	}
}

// openStream starts an httptest server for s and opens GET /events; everything is closed when the test ends.
func openStream(t *testing.T, s *Server, header http.Header) <-chan sseEvent {
	t.Helper()
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithCancel(t.Context())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("status %d, Content-Type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	return readEvents(ctx, resp.Body)
}

// SC-5: an event published by capture reaches an open dashboard within 2 seconds.
func TestEventsStreamSendsPublishedEvent(t *testing.T) {
	bus := activity.NewBus(500)
	events := openStream(t, New(Deps{Activity: bus}), nil)
	bus.Publish(activity.Event{Kind: "message", Chat: "120363000000000001@g.us", Detail: "image"})
	e := next(t, events, "activity", 2*time.Second)
	if !strings.Contains(e.data, "120363000000000001@g.us") || !strings.Contains(e.data, "image") || !strings.Contains(e.data, "message") {
		t.Fatalf("activity data %q", e.data)
	}
	if e.id == "" {
		t.Fatal("an activity event has no id, so a reconnect cannot say what it last saw")
	}
}

// The feed line is rendered through html/template: a crafted chat id cannot inject markup.
func TestEventsStreamEscapesFields(t *testing.T) {
	bus := activity.NewBus(500)
	events := openStream(t, New(Deps{Activity: bus}), nil)
	bus.Publish(activity.Event{Kind: "message", Chat: "<script>x</script>@g.us", Detail: "text"})
	if e := next(t, events, "activity", 2*time.Second); strings.Contains(e.data, "<script>") {
		t.Fatalf("unescaped markup in %q", e.data)
	}
}

// A browser that reconnects (Last-Event-ID) gets the events it missed from the ring, and not the ones it saw.
func TestEventsReconnectReplaysMissedEvents(t *testing.T) {
	bus := activity.NewBus(500)
	t0 := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	bus.Publish(activity.Event{At: t0, Kind: "message", Chat: "seen@s.whatsapp.net", Detail: "text"})
	bus.Publish(activity.Event{At: t0.Add(time.Second), Kind: "message", Chat: "missed@s.whatsapp.net", Detail: "text"})
	events := openStream(t, New(Deps{Activity: bus}), http.Header{"Last-Event-Id": {strconv.FormatInt(t0.UnixNano(), 10)}})
	e := next(t, events, "activity", 2*time.Second)
	if !strings.Contains(e.data, "missed@") {
		t.Fatalf("first replayed event %q, want the missed one", e.data)
	}
	bus.Publish(activity.Event{At: t0.Add(2 * time.Second), Kind: "message", Chat: "live@s.whatsapp.net", Detail: "text"})
	if e := next(t, events, "activity", 2*time.Second); !strings.Contains(e.data, "live@") {
		t.Fatalf("after the replay got %q, want the live event (the seen one must not be resent)", e.data)
	}
}

// A first connect (no Last-Event-ID) does not replay the ring: the page renders the recent events itself.
func TestEventsFirstConnectDoesNotReplay(t *testing.T) {
	bus := activity.NewBus(500)
	bus.Publish(activity.Event{Kind: "message", Chat: "old@s.whatsapp.net", Detail: "text"})
	events := openStream(t, New(Deps{Activity: bus}), nil)
	select { // the first thing on a first connect is the health event, not a replay of the ring
	case e := <-events:
		if e.name != "health" {
			t.Fatalf("first event %q %q, want health", e.name, e.data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no first event")
	}
	bus.Publish(activity.Event{Kind: "message", Chat: "new@s.whatsapp.net", Detail: "text"})
	if e := next(t, events, "activity", 2*time.Second); !strings.Contains(e.data, "new@") {
		t.Fatalf("got %q, want only the new event", e.data)
	}
}

// fakeHealth is a health snapshot the test can change.
type fakeHealth struct {
	mu    sync.Mutex
	state string
}

func (f *fakeHealth) set(s string) { f.mu.Lock(); f.state = s; f.mu.Unlock() }
func (f *fakeHealth) get() health.Snapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return health.Snapshot{State: f.state}
}

// The health panel is re-rendered on a health event: one on connect and one on every state change.
func TestEventsStreamSendsHealthOnConnectAndOnStateChange(t *testing.T) {
	bus := activity.NewBus(500)
	h := &fakeHealth{state: "connected"}
	events := openStream(t, New(Deps{Activity: bus, Health: h.get}), nil)
	if e := next(t, events, "health", 2*time.Second); e.data != "connected" {
		t.Fatalf("health on connect %q", e.data)
	}
	h.set("disconnected")
	bus.Publish(activity.Event{Kind: "state", Detail: "disconnected"})
	if e := next(t, events, "health", 2*time.Second); e.data != "disconnected" {
		t.Fatalf("health after the state change %q", e.data)
	}
}

// A quiet stream still sends a health event now and then: it keeps an SSH tunnel from idling out, and the write
// shows the handler that a client has gone.
func TestEventsStreamRepeatsHealthPeriodically(t *testing.T) {
	s := New(Deps{Activity: activity.NewBus(500)})
	s.healthEvery = 20 * time.Millisecond
	events := openStream(t, s, nil)
	for range 3 {
		if e := next(t, events, "health", 2*time.Second); e.data != "unknown" {
			t.Fatalf("health without a monitor %q, want unknown", e.data)
		}
	}
}

// A tab closed or a tunnel dropped mid-stream: the handler returns, its subscription is gone, nothing leaks.
func TestEventsStreamEndsWhenClientGoes(t *testing.T) {
	bus := activity.NewBus(500)
	returned := make(chan struct{})
	h := New(Deps{Activity: bus}).Handler()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r)
		if r.URL.Path == "/events" {
			close(returned)
		}
	}))
	ctx, cancel := context.WithCancel(t.Context())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/events", nil)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	_ = resp.Body.Close()
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("the SSE handler kept running after the client went away")
	}
	srv.Close()
	goleak.VerifyNone(t)
}

// A subscriber that fell behind has its channel closed by the bus; the handler ends the stream so the browser
// reconnects (and replays what it missed) instead of spinning on a closed channel. A nil bus hands out a closed one.
func TestEventsStreamEndsWhenFeedCloses(t *testing.T) {
	srv := httptest.NewServer(New(Deps{}).Handler())
	defer srv.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/events", nil)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatalf("the stream did not end on a closed feed: %v", err)
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

// Run serves until its context ends, then stops within its 5-second bound even with an SSE stream open.
func TestRunStopsOnContextCancel(t *testing.T) {
	addr := freeAddr(t)
	s := New(Deps{Activity: activity.NewBus(500)})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	errc := make(chan error, 1)
	go func() { errc <- s.Run(ctx, addr) }()
	client := &http.Client{Transport: &http.Transport{}}
	defer client.CloseIdleConnections()
	var resp *http.Response
	for deadline := time.Now().Add(2 * time.Second); ; {
		var err error
		resp, err = client.Get("http://" + addr + "/events")
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Run never served: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /events status %d", resp.StatusCode)
	}
	start := time.Now()
	cancel()
	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("Run returned %v after a clean stop", err)
		}
		if took := time.Since(start); took > 2*time.Second {
			t.Fatalf("Run took %s to stop; an open stream must not hold it up", took)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("Run did not stop after its context ended")
	}
	if _, err := io.ReadAll(resp.Body); err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Logf("stream ended with %v", err)
	}
}

func TestRunReportsListenError(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	errc := make(chan error, 1)
	go func() { errc <- New(Deps{}).Run(t.Context(), l.Addr().String()) }()
	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("Run on an address in use returned nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run on an address in use did not return")
	}
}

// F2: Listening is called only once the port is held; a taken port gets the error and no call.
func TestRunReportsListeningOnlyAfterListen(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = taken.Close() })
	var calls []string
	s := New(Deps{Listening: func(addr string) { calls = append(calls, addr) }})
	if err := s.Run(t.Context(), taken.Addr().String()); err == nil {
		t.Fatal("Run on a taken port returned nil")
	}
	if len(calls) != 0 {
		t.Fatalf("Listening called %v for a port that could not be listened on", calls)
	}

	free, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := free.Addr().String()
	_ = free.Close()
	got := make(chan string, 1)
	s = New(Deps{Listening: func(a string) { got <- a }})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx, addr) }()
	select {
	case a := <-got:
		if a != addr {
			t.Fatalf("Listening(%q), want %q", a, addr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Listening was not called for a free port")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// F8: a feed line shows its time in the configured time zone, like every other page.
func TestFeedLineUsesConfiguredTimeZone(t *testing.T) {
	bus := activity.NewBus(500)
	events := openStream(t, New(Deps{Activity: bus, Config: &config.Config{TimeZone: "Asia/Riyadh"}}), nil)
	bus.Publish(activity.Event{At: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC), Kind: "message", Chat: "a@g.us", Detail: "text"})
	e := next(t, events, "activity", 2*time.Second)
	if !strings.Contains(e.data, ">15:00:00<") || strings.Contains(e.data, "12:00:00") {
		t.Fatalf("activity line %q, want the time as 15:00:00 (Asia/Riyadh)", e.data)
	}
}

// F8: the feed on an open page keeps the newest 200 lines: the page carries the cap and the script that applies it
// after each streamed line.
func TestFeedPageCapsItsLength(t *testing.T) {
	body, code, _ := get(t, New(Deps{Activity: activity.NewBus(10), Config: &config.Config{}, Store: testkit.Store(t), Rules: ingest.NewRuleSet(nil)}).Handler(), "/")
	if code != http.StatusOK {
		t.Fatalf("GET /: %d", code)
	}
	for _, want := range []string{`id="feed"`, `data-max="200"`, "htmx:sseMessage", "lastElementChild.remove()"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("dashboard page lacks %q", want)
		}
	}
}
