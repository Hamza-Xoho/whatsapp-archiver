package web

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"time"

	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/view"
)

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

	last := r.URL.Query().Get("since")
	lastDay := ""
	if last != "" {
		if ts, _, anchorErr := s.d.Store.ViewerAnchor(r.Context(), jid, last); anchorErr == nil {
			lastDay = time.Unix(ts, 0).In(loc).Format("2 January 2006")
		}
	}
	appendNew := func() error {
		for {
			messages, loadErr := messagesAfter(r.Context(), s.d.Store, jid, last)
			if loadErr != nil {
				return loadErr
			}
			slices.Reverse(messages)
			for _, message := range messages {
				v := makeViewMessage(loc, message, "", false)
				v.ChatJID = jid
				v.NewDay = v.Day != lastDay
				lastDay, last = v.Day, v.ID
				var buf bytes.Buffer
				if renderErr := s.pages["viewer"].ExecuteTemplate(&buf, "bubble", v); renderErr != nil {
					return renderErr
				}
				if writeErr := writeEvent(w, "", "viewer", buf.String()); writeErr != nil {
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

// messagesAfter returns newest first, matching ViewerPage. An empty cursor is the live edge of an initially empty chat.
func messagesAfter(ctx context.Context, st *store.Store, jid, id string) ([]store.ViewMessage, error) {
	if id == "" {
		return st.ViewerPage(ctx, jid, store.ViewerQuery{Limit: 1})
	}
	ts, _, err := st.ViewerAnchor(ctx, jid, id)
	if errors.Is(err, sql.ErrNoRows) {
		return st.ViewerPage(ctx, jid, store.ViewerQuery{Limit: 1})
	}
	if err != nil {
		return nil, err
	}
	return st.ViewerPage(ctx, jid, store.ViewerQuery{AfterTS: ts, AfterID: id, Limit: viewerPageSize + 1})
}
