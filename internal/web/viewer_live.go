package web

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"whatsapp-archiver/internal/view"
)

const maxViewerSSEPayload = 16 << 10

// viewerEvents appends newly archived messages while a browser is at an unfiltered chat's live edge. The activity
// bus remains metadata-only: this handler re-reads the stored message and renders it only into this chat's response.
func (s *Server) viewerEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	if s.d.Store == nil {
		http.NotFound(w, r)
		return
	}
	jid := r.PathValue("jid")
	known, err := s.d.Store.ChatKnown(r.Context(), jid)
	if err != nil {
		http.Error(w, "the archive could not be read", http.StatusInternalServerError)
		return
	}
	if !known {
		http.NotFound(w, r)
		return
	}
	aliases, err := s.d.Store.Aliases(r.Context(), jid)
	if err != nil {
		http.Error(w, "the archive could not be read", http.StatusInternalServerError)
		return
	}
	chatForms := map[string]bool{jid: true}
	for _, alias := range aliases {
		chatForms[alias] = true
	}
	loc, err := view.Location(s.config())
	if err != nil {
		http.Error(w, "the page could not be rendered", http.StatusInternalServerError)
		return
	}

	feed, cancel := s.d.Activity.Subscribe(sseBuffer)
	defer cancel()
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	last, _ := strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
	if resumed, parseErr := strconv.ParseInt(r.Header.Get("Last-Event-ID"), 10, 64); parseErr == nil && resumed > last {
		last = resumed
	}
	lastDay := r.URL.Query().Get("day")
	countOnly := r.URL.Query().Get("mode") == "count"
	appendNew := func() error {
		for {
			messages, loadErr := s.d.Store.ViewerLiveAfter(r.Context(), jid, last, viewerPageSize+1)
			if loadErr != nil {
				return loadErr
			}
			for _, message := range messages {
				last = message.Seq
				lastID := strconv.FormatInt(last, 10)
				if countOnly {
					if writeErr := writeEvent(w, lastID, "viewer-new", `<span class="new-message-marker" hidden></span>`); writeErr != nil {
						return writeErr
					}
					continue
				}
				v := makeViewMessage(loc, message, "", false)
				v.ChatJID = jid
				v.NewDay = v.Day != lastDay
				lastDay = v.Day
				var buf bytes.Buffer
				if renderErr := s.pages["viewer"].ExecuteTemplate(&buf, "bubble", v); renderErr != nil {
					return renderErr
				}
				wire := encodeEvent(lastID, "viewer", buf.String())
				if len(wire) >= maxViewerSSEPayload {
					path := "/chats/" + url.PathEscape(jid) + "/m/" + url.PathEscape(message.ID)
					marker := fmt.Sprintf(`<li class="live-refetch"><a href="%s">New archived message. Open the full message</a></li>`, path)
					if writeErr := writeEvent(w, lastID, "viewer-refetch", marker); writeErr != nil {
						return writeErr
					}
					continue
				}
				if _, writeErr := w.Write(wire); writeErr != nil {
					return writeErr
				}
			}
			if len(messages) < viewerPageSize+1 {
				return nil
			}
		}
	}
	if err = appendNew(); err != nil {
		return
	}
	flusher.Flush()

	keepalive := time.NewTicker(s.healthEvery)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case event, open := <-feed:
			if !open {
				return
			}
			if event.Kind != "message" || !chatForms[event.Chat] {
				continue
			}
			if err = appendNew(); err != nil {
				return
			}
			flusher.Flush()
		case <-keepalive.C:
			if _, err = w.Write([]byte(": keepalive\n\n")); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
