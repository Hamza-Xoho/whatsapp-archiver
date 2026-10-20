package web

import (
	"bytes"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"whatsapp-archiver/internal/activity"
)

// sseBuffer is a stream's subscription buffer. A browser that falls this far behind is dropped by the bus (capture
// never waits on it); its stream then ends and the browser reconnects and replays what it missed.
const sseBuffer = 64

// events is GET /events, the one SSE stream. It sends:
//   - "activity": one feed line (HTML rendered by the activity_item template), with the event's time in nanoseconds
//     as its id. A reconnecting browser sends the last id it saw as Last-Event-ID and gets the newer events still in
//     the bus's ring. A first connect replays nothing: the page renders the recent events itself.
//   - "health": the health state, on connect, after every state change, and every healthEvery while quiet; the
//     health panel re-renders on it. The repeat also keeps an SSH tunnel from idling out and makes a write that
//     shows a client has gone.
//
// The handler returns when the client goes, when Run's context ends, or when the bus closes the subscription.
// Payloads carry no message text, captions or URLs: activity events have none.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	feed, cancel := s.d.Activity.Subscribe(sseBuffer) // before Recent, so nothing falls between the replay and the feed
	defer cancel()
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	var replayed int64 // the newest event id sent by the replay; the feed skips anything not newer
	if last, err := strconv.ParseInt(r.Header.Get("Last-Event-ID"), 10, 64); err == nil {
		for _, e := range s.d.Activity.Recent() {
			if id := e.At.UnixNano(); id > last {
				if s.writeActivity(w, e) != nil {
					return
				}
				replayed = max(replayed, id)
			}
		}
	}
	if s.writeHealth(w) != nil {
		return
	}
	flusher.Flush()

	tick := time.NewTicker(s.healthEvery)
	defer tick.Stop()
	for {
		var err error
		select {
		case <-r.Context().Done():
			return
		case e, open := <-feed:
			if !open {
				return // dropped for falling behind: end the stream so the browser reconnects
			}
			if e.At.UnixNano() <= replayed {
				continue
			}
			err = s.writeActivity(w, e)
			if err == nil && e.Kind == "state" {
				err = s.writeHealth(w)
			}
		case <-tick.C:
			err = s.writeHealth(w)
		}
		if err != nil {
			return
		}
		flusher.Flush()
	}
}

func (s *Server) writeActivity(w http.ResponseWriter, e activity.Event) error {
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, "activity_item", e); err != nil {
		return err
	}
	return writeEvent(w, strconv.FormatInt(e.At.UnixNano(), 10), "activity", buf.String())
}

func (s *Server) writeHealth(w http.ResponseWriter) error {
	state := ""
	if s.d.Health != nil {
		state = s.d.Health().State
	}
	if state == "" {
		state = "unknown" // an event with no data is never dispatched by the browser
	}
	return writeEvent(w, "", "health", state)
}

// writeEvent writes one SSE event; data with line breaks becomes several data lines.
func writeEvent(w http.ResponseWriter, id, name, data string) error {
	var b strings.Builder
	if id != "" {
		fmt.Fprintf(&b, "id: %s\n", id)
	}
	fmt.Fprintf(&b, "event: %s\n", name)
	for _, line := range strings.Split(strings.ReplaceAll(data, "\r", ""), "\n") {
		fmt.Fprintf(&b, "data: %s\n", line)
	}
	b.WriteString("\n")
	_, err := w.Write([]byte(b.String()))
	return err
}
