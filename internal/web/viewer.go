package web

import (
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"strconv"

	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/view"
)

// viewerPageSize is how many messages one viewer page shows; a large chat is never loaded whole.
const viewerPageSize = 200

type viewMsg struct {
	store.ViewMessage
	Time, From string
	ShowMedia  bool // the file is downloaded and can be fetched from /media
	IsImage    bool
}

type viewerData struct {
	page
	JID, Name   string
	Messages    []viewMsg // oldest first, as a chat reads
	HasOlder    bool
	OlderBefore int64
	OlderID     string
	Before      int64
}

// viewer is GET /chats/{jid}?before=, one read-only page of a chat: the newest viewerPageSize messages older than
// before, with an "Older" link when more remain.
func (s *Server) viewer(w http.ResponseWriter, r *http.Request) {
	jid := r.PathValue("jid")
	var before int64
	if v := r.URL.Query().Get("before"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			http.Error(w, "before must be a message time", http.StatusBadRequest)
			return
		}
		before = n
	}
	beforeID := r.URL.Query().Get("before_id")
	if s.d.Store == nil {
		http.NotFound(w, r)
		return
	}
	ctx := r.Context()
	loc, err := view.Location(s.config())
	if err != nil {
		http.Error(w, "the page could not be rendered", http.StatusInternalServerError)
		return
	}
	msgs, err := s.d.Store.ChatPage(ctx, jid, before, beforeID, viewerPageSize+1)
	if err != nil {
		http.Error(w, "the archive could not be read", http.StatusInternalServerError)
		return
	}
	name, err := s.d.Store.ChatName(ctx, jid)
	if err != nil {
		http.Error(w, "the archive could not be read", http.StatusInternalServerError)
		return
	}
	if name == "" {
		name = jid
	}
	data := viewerData{page: page{Title: name}, JID: jid, Name: name, Before: before}
	if len(msgs) > viewerPageSize {
		msgs = msgs[:viewerPageSize]
		data.HasOlder, data.OlderBefore, data.OlderID = true, msgs[viewerPageSize-1].TS, msgs[viewerPageSize-1].ID
	}
	for _, m := range msgs {
		v := viewMsg{ViewMessage: m, Time: view.FormatUnix(loc, m.TS), From: m.SenderName, ShowMedia: m.MediaState == "done"}
		if v.From == "" {
			v.From = m.Sender
		}
		v.IsImage = v.ShowMedia && (m.MediaType == "image" || m.MediaType == "sticker" || m.MediaType == "")
		data.Messages = append(data.Messages, v)
	}
	slices.Reverse(data.Messages)
	s.render(w, s.pages["viewer"], "layout", data)
}

type traceData struct {
	page
	JID, ID                string
	Trace                  store.Trace
	Time, Received, Raw    string
	Events                 []traceEvent
	HasMessage, MediaReady bool
}

type traceEvent struct {
	store.MessageEvent
	Time string
}

// trace is GET /chats/{jid}/m/{id}: everything the archive holds about one message.
func (s *Server) trace(w http.ResponseWriter, r *http.Request) {
	jid, id := r.PathValue("jid"), r.PathValue("id")
	if s.d.Store == nil {
		http.NotFound(w, r)
		return
	}
	loc, err := view.Location(s.config())
	if err != nil {
		http.Error(w, "the page could not be rendered", http.StatusInternalServerError)
		return
	}
	t, err := s.d.Store.Trace(r.Context(), jid, id)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "the archive could not be read", http.StatusInternalServerError)
		return
	}
	data := traceData{page: page{Title: "Message details"}, JID: jid, ID: id, Trace: t, HasMessage: t.Message.ID != "",
		Time: view.FormatUnix(loc, t.Message.Timestamp), Received: view.FormatUnix(loc, t.Message.ReceivedAt),
		Raw: view.HumanSize(int64(len(t.Message.RawProto))), MediaReady: t.Media != nil && t.Media.State == "done"}
	for _, e := range t.Events {
		data.Events = append(data.Events, traceEvent{MessageEvent: e, Time: view.FormatUnix(loc, e.Timestamp)})
	}
	s.render(w, s.pages["trace"], "layout", data)
}
