package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/view"
)

// viewerPageSize is how many messages one viewer page shows; a large chat is never loaded whole.
const viewerPageSize = 100

var safeDOMID = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

type textPart struct {
	Text string
	Mark bool
}

type viewMsg struct {
	store.ViewMessage
	ChatJID, Time, Day, From, DOMID, QuoteDOMID, Ephemeral, MapURL string
	NewDay, CurrentMatch                                           bool
	Body, Original                                                 []textPart
	Location                                                       *store.LocationDetails
	Contact                                                        *store.ContactDetails
	Contacts                                                       *store.ContactsDetails
	Event                                                          *store.EventDetails
	MediaSize                                                      string
}

type viewerData struct {
	page
	JID, Name, OlderURL, NewerURL, Query, Sender, Media, FromDate, ToDate string
	LiveCursor                                                            string
	Messages                                                              []viewMsg
	HasOlder, HasNewer, Filtered, DateJump, ViewerLive                    bool
	MatchPosition, MatchCount                                             int
	PreviousMatchURL, NextMatchURL                                        string
}

// viewer is the typed, bounded chat reader. Its URL owns cursors, filters, date jumps and in-chat search.
func (s *Server) viewer(w http.ResponseWriter, r *http.Request) {
	jid := r.PathValue("jid")
	for _, key := range []string{"before", "after"} {
		if raw := r.URL.Query().Get(key); raw != "" {
			if n, err := strconv.ParseInt(raw, 10, 64); err != nil || n < 0 {
				http.Error(w, key+" must be a message time", http.StatusBadRequest)
				return
			}
		}
	}
	if s.d.Store == nil {
		http.NotFound(w, r)
		return
	}
	ctx := r.Context()
	known, err := s.d.Store.ChatKnown(ctx, jid)
	if err != nil {
		http.Error(w, "the archive could not be read", http.StatusInternalServerError)
		return
	}
	if !known {
		http.NotFound(w, r)
		return
	}
	loc, err := view.Location(s.config())
	if err != nil {
		http.Error(w, "the page could not be rendered", http.StatusInternalServerError)
		return
	}
	query, err := parseViewerQuery(r.URL.Query(), loc)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	matches, matchIndex, err := s.viewerSearch(ctx, jid, r.URL.Query(), &query)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "the archive could not be read", http.StatusInternalServerError)
		return
	}
	dateJump := false
	if date := r.URL.Query().Get("date"); date != "" {
		start, _ := time.ParseInLocation("2006-01-02", date, loc)
		_, id, anchorErr := s.d.Store.ViewerDateAnchor(ctx, jid, start.Unix())
		if errors.Is(anchorErr, sql.ErrNoRows) {
			// An empty chat renders its normal empty state.
		} else if anchorErr != nil {
			http.Error(w, "the archive could not be read", http.StatusInternalServerError)
			return
		} else {
			query.FocusID, dateJump = id, true
		}
	}
	msgs, err := s.d.Store.ViewerPage(ctx, jid, query)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
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
	values := cloneValues(r.URL.Query())
	values.Del("date")
	data := viewerData{page: page{Title: name, Current: "Chats"}, JID: jid, Name: name,
		Query: values.Get("q"), Sender: values.Get("sender"), Media: values.Get("media"), FromDate: values.Get("from"), ToDate: values.Get("to"), DateJump: dateJump}
	if len(msgs) > viewerPageSize {
		msgs = msgs[:viewerPageSize]
		if query.AfterTS > 0 {
			data.HasNewer = true
		} else {
			data.HasOlder = true
		}
	}
	data.HasNewer = data.HasNewer || query.BeforeTS > 0
	data.HasOlder = data.HasOlder || query.AfterTS > 0
	data.Filtered = data.Query != "" || data.Sender != "" || data.Media != "" || data.FromDate != "" || data.ToDate != ""
	data.ViewerLive = query.BeforeTS == 0 && query.AfterTS == 0 && query.FocusID == "" && len(matches) == 0 && !data.Filtered
	if len(msgs) > 0 {
		oldest, newest := msgs[len(msgs)-1], msgs[0]
		data.LiveCursor = newest.ID
		if data.HasOlder {
			data.OlderURL = viewerURL(jid, values, "before", oldest.TS, oldest.ID)
		}
		if data.HasNewer {
			data.NewerURL = viewerURL(jid, values, "after", newest.TS, newest.ID)
		}
	}
	slices.Reverse(msgs)
	currentMatch := ""
	if len(matches) > 0 {
		currentMatch = matches[matchIndex]
		data.MatchCount, data.MatchPosition = len(matches), matchIndex+1
		if matchIndex > 0 {
			data.PreviousMatchURL = matchURL(jid, values, matchIndex-1)
		}
		if matchIndex+1 < len(matches) {
			data.NextMatchURL = matchURL(jid, values, matchIndex+1)
		}
	}
	var lastDay string
	for _, m := range msgs {
		v := makeViewMessage(loc, m, data.Query, m.ID == currentMatch)
		v.ChatJID = jid
		if v.From == "" {
			v.From = m.Sender
		}
		v.NewDay = v.Day != lastDay
		lastDay = v.Day
		data.Messages = append(data.Messages, v)
	}
	s.render(w, s.pages["viewer"], "layout", data)
}

func parseViewerQuery(values url.Values, loc *time.Location) (store.ViewerQuery, error) {
	q := store.ViewerQuery{BeforeID: values.Get("before_id"), AfterID: values.Get("after_id"), Sender: values.Get("sender"), MediaKind: values.Get("media"), FocusID: values.Get("focus"), Limit: viewerPageSize + 1}
	for key, dst := range map[string]*int64{"before": &q.BeforeTS, "after": &q.AfterTS} {
		if raw := values.Get(key); raw != "" {
			n, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || n < 0 {
				return q, fmt.Errorf("%s must be a message time", key)
			}
			*dst = n
		}
	}
	for _, key := range []string{"from", "to", "date"} {
		raw := values.Get(key)
		if raw == "" {
			continue
		}
		day, err := time.ParseInLocation("2006-01-02", raw, loc)
		if err != nil {
			return q, fmt.Errorf("%s must be a date", key)
		}
		switch key {
		case "from":
			q.FromTS = day.Unix()
		case "to":
			q.ToTS = day.AddDate(0, 0, 1).Add(-time.Second).Unix()
		}
	}
	return q, nil
}

func (s *Server) viewerSearch(ctx context.Context, jid string, values url.Values, q *store.ViewerQuery) ([]string, int, error) {
	text := strings.TrimSpace(values.Get("q"))
	if text == "" {
		return nil, 0, nil
	}
	ids, err := s.d.Store.SearchChat(ctx, jid, text, *q)
	if err != nil || len(ids) == 0 {
		return ids, 0, err
	}
	index := 0
	if raw := values.Get("match"); raw != "" {
		n, parseErr := strconv.Atoi(raw)
		if parseErr != nil || n < 0 || n >= len(ids) {
			return nil, 0, fmt.Errorf("match is out of range")
		}
		index = n
	}
	q.FocusID = ids[index]
	return ids, index, nil
}

func cloneValues(in url.Values) url.Values {
	out := make(url.Values, len(in))
	for key, values := range in {
		out[key] = slices.Clone(values)
	}
	return out
}

func viewerURL(jid string, values url.Values, direction string, ts int64, id string) string {
	values = cloneValues(values)
	for _, key := range []string{"before", "before_id", "after", "after_id", "focus", "match"} {
		values.Del(key)
	}
	values.Set(direction, strconv.FormatInt(ts, 10))
	values.Set(direction+"_id", id)
	return "/chats/" + url.PathEscape(jid) + "?" + values.Encode()
}

func matchURL(jid string, values url.Values, index int) string {
	values = cloneValues(values)
	values.Set("match", strconv.Itoa(index))
	values.Del("focus")
	return "/chats/" + url.PathEscape(jid) + "?" + values.Encode()
}

func makeViewMessage(loc *time.Location, m store.ViewMessage, search string, current bool) viewMsg {
	from := m.SenderName
	if m.FromMe {
		from = "You"
	}
	day := time.Unix(m.TS, 0).In(loc).Format("2 January 2006")
	v := viewMsg{ViewMessage: m, Time: view.FormatUnix(loc, m.TS), Day: day, From: from, DOMID: messageDOMID(m.ID), CurrentMatch: current}
	if m.QuotedID != "" {
		v.QuoteDOMID = messageDOMID(m.QuotedID)
	}
	body := m.Text
	if m.Caption != "" {
		body = m.Caption
	}
	if m.EditedText != "" {
		v.Original = highlightParts(m.Text, "", false)
		body = m.EditedText
	}
	v.Body = highlightParts(body, search, current)
	if m.EphemeralSecs > 0 {
		v.Ephemeral = ephemeralDuration(m.EphemeralSecs)
	}
	if m.Media != nil {
		v.MediaSize = view.HumanSize(m.Media.DeclaredSize)
	}
	switch m.Kind {
	case "location", "live_location":
		var d store.LocationDetails
		_ = json.Unmarshal([]byte(m.Details), &d)
		v.Location = &d
		if d.URL != "" {
			if parsed, err := url.Parse(d.URL); err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") {
				v.MapURL = d.URL
			}
		}
		if v.MapURL == "" && d.Lat != nil && d.Lon != nil {
			v.MapURL = fmt.Sprintf("https://www.openstreetmap.org/?mlat=%g&mlon=%g", *d.Lat, *d.Lon)
		}
	case "contact":
		var d store.ContactDetails
		_ = json.Unmarshal([]byte(m.Details), &d)
		v.Contact = &d
	case "contacts":
		var d store.ContactsDetails
		_ = json.Unmarshal([]byte(m.Details), &d)
		v.Contacts = &d
	case "event":
		var d store.EventDetails
		_ = json.Unmarshal([]byte(m.Details), &d)
		v.Event = &d
	}
	return v
}

func messageDOMID(id string) string {
	if safeDOMID.MatchString(id) {
		return "message-" + id
	}
	return fmt.Sprintf("message-%x", []byte(id))
}

func highlightParts(text, query string, current bool) []textPart {
	if text == "" {
		return nil
	}
	if current && query != "" && strings.Contains(store.Fold(text), store.Fold(query)) {
		return []textPart{{Text: text, Mark: true}}
	}
	return []textPart{{Text: text}}
}

func ephemeralDuration(seconds int) string {
	d := time.Duration(seconds) * time.Second
	switch {
	case d%time.Hour == 0:
		return fmt.Sprintf("%d hours", int(d/time.Hour))
	case d%time.Minute == 0:
		return fmt.Sprintf("%d minutes", int(d/time.Minute))
	default:
		return fmt.Sprintf("%d seconds", seconds)
	}
}

// mediaKinds are the message kinds that carry a file.
var mediaKinds = []string{"image", "video", "audio", "voice", "document", "sticker", "gif"}

type traceData struct {
	page
	JID, ID                string
	Trace                  store.Trace
	Time, Received, Raw    string
	MediaKind              string // the message's kind when it is a media kind, else ""
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
	data := traceData{page: page{Title: "Message details", Current: "Chats"}, JID: jid, ID: id, Trace: t, HasMessage: t.Message.ID != "",
		Time: view.FormatUnix(loc, t.Message.Timestamp), Received: view.FormatUnix(loc, t.Message.ReceivedAt),
		Raw: view.HumanSize(int64(len(t.Message.RawProto))), MediaReady: t.Media != nil && t.Media.State == "done"}
	if slices.Contains(mediaKinds, t.Message.Kind) {
		data.MediaKind = t.Message.Kind
	}
	for _, e := range t.Events {
		data.Events = append(data.Events, traceEvent{MessageEvent: e, Time: view.FormatUnix(loc, e.Timestamp)})
	}
	s.render(w, s.pages["trace"], "layout", data)
}
