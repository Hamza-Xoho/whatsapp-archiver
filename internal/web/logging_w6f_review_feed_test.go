package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"whatsapp-archiver/internal/activity"
)

func TestLoggingFilteredFeedKeepsStableEmptyTarget(t *testing.T) {
	d := newDash(t)
	d.bus.Publish(activity.Event{At: dashNow, Kind: "message", Chat: "work@g.us", Detail: "text"})
	body, code, _ := get(t, d.handler(), "/logging/feed?chat=family%40g.us&kind=message")
	if code != http.StatusOK {
		t.Fatalf("status %d: %s", code, body)
	}
	page := string(body)
	for _, want := range []string{`id="feed"`, `role="log"`, "Waiting for archive activity", `sse-connect="/events?chat=family%40g.us&amp;kind=message"`, "feed-empty", "remove()"} {
		if !strings.Contains(page, want) {
			t.Errorf("empty filtered feed lacks %q", want)
		}
	}
	if strings.Contains(page, "work@g.us") {
		t.Fatalf("filtered initial feed contains nonmatching chat: %s", page)
	}
}

func TestLoggingFeedSSEFiltersSubsequentEvents(t *testing.T) {
	bus := activity.NewBus(20)
	events := openReviewStream(t, New(Deps{Activity: bus}), "/events?chat=family%40g.us&kind=message")
	_ = next(t, events, "health", 2*time.Second)
	bus.Publish(activity.Event{Kind: "message", Chat: "work@g.us", Detail: "wrong-chat"})
	bus.Publish(activity.Event{Kind: "media", Chat: "family@g.us", Detail: "wrong-kind"})
	bus.Publish(activity.Event{Kind: "message", Chat: "family@g.us", Detail: "matching"})
	e := next(t, events, "activity", 2*time.Second)
	if !strings.Contains(e.data, "family@g.us") || !strings.Contains(e.data, "matching") {
		t.Fatalf("filtered event = %q", e.data)
	}
	if strings.Contains(e.data, "wrong-chat") || strings.Contains(e.data, "wrong-kind") {
		t.Fatalf("filter leaked a nonmatching event: %q", e.data)
	}
}

func openReviewStream(t *testing.T, s *Server, path string) <-chan sseEvent {
	t.Helper()
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithCancel(t.Context())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
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
