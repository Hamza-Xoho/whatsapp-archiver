package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/view"
)

const exportPageSize = 1000

type exportClientError string

func (e exportClientError) Error() string { return string(e) }

const (
	errExportFailed exportClientError = "The chat could not be exported. Try again."
	errExportEmpty  exportClientError = "There are no archived messages in this date range."
)

type exportPageData struct {
	page
	JID, Name, From, To string
}

func (s *Server) exportPage(w http.ResponseWriter, r *http.Request) {
	jid := r.PathValue("jid")
	if s.d.Store == nil {
		http.NotFound(w, r)
		return
	}
	known, err := s.d.Store.ChatKnown(r.Context(), jid)
	if err != nil {
		http.Error(w, "the archive could not be read", 500)
		return
	}
	if !known {
		http.NotFound(w, r)
		return
	}
	name, err := s.d.Store.ChatName(r.Context(), jid)
	if err != nil {
		http.Error(w, "the archive could not be read", 500)
		return
	}
	if name == "" {
		name = jid
	}
	s.render(w, s.pages["export"], "layout", exportPageData{page: page{Title: "Export " + name, Current: "Chats"}, JID: jid, Name: name, From: r.URL.Query().Get("from"), To: r.URL.Query().Get("to")})
}

type exportScope struct {
	jid, name, format string
	origin            string
	from, to          int64
	loc               *time.Location
	aliases           []string
	snapshot          *store.ExportSnapshot
	generated         time.Time
}

func (s *Server) exportDownload(w http.ResponseWriter, r *http.Request) {
	scope, status, err := s.validateExport(r)
	if err != nil {
		if status == 404 {
			http.NotFound(w, r)
		} else {
			http.Error(w, err.Error(), status)
		}
		return
	}
	spool, err := os.CreateTemp("", "whatsapp-archiver-export-*")
	if err != nil {
		_ = scope.snapshot.Close()
		http.Error(w, errExportFailed.Error(), http.StatusInternalServerError)
		return
	}
	spoolName := spool.Name()
	defer func() {
		_ = spool.Close()
		_ = os.Remove(spoolName)
	}()
	ext := map[string]string{"json": "json", "html": "html", "txt": "_chat.txt", "zip": "zip"}[scope.format]
	filename := safeExportName(scope.name) + "." + ext
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	switch scope.format {
	case "json":
		h.Set("Content-Type", "application/json; charset=utf-8")
	case "html":
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("Content-Security-Policy", "sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src data:; media-src data:")
	case "txt":
		h.Set("Content-Type", "text/plain; charset=utf-8")
	case "zip":
		h.Set("Content-Type", "application/zip")
	}
	w.WriteHeader(http.StatusOK)
	err = s.writeExport(r.Context(), spool, scope)
	closeErr := scope.snapshot.Close()
	scope.snapshot = nil
	if err == nil {
		err = closeErr
	}
	if err == nil {
		_, err = spool.Seek(0, io.SeekStart)
	}
	if err == nil {
		err = copyContext(r.Context(), w, spool)
	}
	if err != nil {
		s.d.Logger.Error("chat export ended", "format", scope.format, "err", err)
	}
}

func (s *Server) writeExport(ctx context.Context, w io.Writer, scope exportScope) error {
	switch scope.format {
	case "json":
		return s.streamJSON(ctx, w, scope)
	case "html":
		return s.streamHTML(ctx, w, scope)
	case "txt":
		return s.streamText(ctx, w, scope)
	case "zip":
		return s.streamZIP(ctx, w, scope)
	default:
		return errExportFailed
	}
}

func (s *Server) validateExport(r *http.Request) (exportScope, int, error) {
	var x exportScope
	x.jid = r.PathValue("jid")
	x.format = r.URL.Query().Get("format")
	x.generated = s.d.Now()
	x.origin = requestOrigin(r)
	if s.d.Store == nil {
		return x, 404, sql.ErrNoRows
	}
	if _, ok := map[string]bool{"json": true, "html": true, "txt": true, "zip": true}[x.format]; !ok {
		return x, 400, errors.New("format must be json, html, txt, or zip")
	}
	loc, err := view.Location(s.config())
	if err != nil {
		return x, 500, errExportFailed
	}
	x.loc = loc
	if x.from, err = parseExportDate(r.URL.Query().Get("from"), false, loc); err != nil {
		return x, 400, errors.New("from must be a date")
	}
	if x.to, err = parseExportDate(r.URL.Query().Get("to"), true, loc); err != nil {
		return x, 400, errors.New("to must be a date")
	}
	if x.from > 0 && x.to > 0 && x.from > x.to {
		return x, 400, errors.New("from must not be after to")
	}
	x.snapshot, err = s.d.Store.NewExportSnapshot(r.Context(), x.jid)
	if errors.Is(err, sql.ErrNoRows) {
		return x, 404, err
	}
	if err != nil {
		return x, 500, errExportFailed
	}
	keepSnapshot := false
	defer func() {
		if !keepSnapshot {
			_ = x.snapshot.Close()
		}
	}()
	x.aliases = x.snapshot.Aliases()
	x.name, err = s.d.Store.ChatName(r.Context(), x.jid)
	if err != nil {
		return x, 500, errExportFailed
	}
	if x.name == "" {
		x.name = x.jid
	}
	found, err := s.d.Store.ExportSnapshotHasMessages(r.Context(), x.snapshot, x.from, x.to)
	if err != nil {
		return x, 500, errExportFailed
	}
	if !found {
		return x, 400, errExportEmpty
	}
	keepSnapshot = true
	return x, 200, nil
}

func requestOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]); forwarded == "http" || forwarded == "https" {
		scheme = forwarded
	}
	return scheme + "://" + r.Host
}

func parseExportDate(raw string, end bool, loc *time.Location) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	t, err := time.ParseInLocation("2006-01-02", raw, loc)
	if err != nil {
		return 0, err
	}
	if end {
		t = t.AddDate(0, 0, 1).Add(-time.Second)
	}
	return t.Unix(), nil
}
func safeExportName(s string) string {
	s = strings.Trim(unsafeName.ReplaceAllString(s, "_"), "_")
	if s == "" {
		return "chat"
	}
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}

func (s *Server) eachExport(ctx context.Context, x exportScope, fn func(store.ExportRecord) error) error {
	var ts int64
	var id string
	hasCursor := false
	for {
		page, err := s.d.Store.ExportPage(ctx, store.ExportParams{CanonicalJID: x.jid, Snapshot: x.snapshot, From: x.from, To: x.to, AfterTS: ts, AfterID: id, HasCursor: hasCursor, Limit: exportPageSize})
		if err != nil {
			return err
		}
		if len(page) == 0 {
			return nil
		}
		for _, r := range page {
			if err = ctx.Err(); err != nil {
				return err
			}
			if err = fn(r); err != nil {
				return err
			}
			ts, id = r.Message.Timestamp, r.Message.ID
			hasCursor = true
		}
		if len(page) < exportPageSize {
			return nil
		}
	}
}

type jsonMessage struct {
	ID            string          `json:"id"`
	Sender        string          `json:"sender"`
	SenderName    string          `json:"sender_name,omitempty"`
	Kind          string          `json:"kind"`
	Text          string          `json:"text,omitempty"`
	Caption       string          `json:"caption,omitempty"`
	QuotedID      string          `json:"quoted_id,omitempty"`
	QuotedSender  string          `json:"quoted_sender,omitempty"`
	Source        string          `json:"source"`
	Details       json.RawMessage `json:"details"`
	Mentions      []string        `json:"mentions,omitempty"`
	Timestamp     int64           `json:"timestamp"`
	ReceivedAt    int64           `json:"received_at"`
	FromMe        bool            `json:"from_me"`
	Forwarded     bool            `json:"forwarded"`
	ViewOnce      bool            `json:"view_once"`
	ForwardScore  int             `json:"forward_score"`
	EphemeralSecs int             `json:"ephemeral_secs"`
	Media         *jsonMedia      `json:"media,omitempty"`
}
type jsonEvent struct {
	EventID    string `json:"event_id"`
	Kind       string `json:"kind"`
	SourceID   string `json:"source_id,omitempty"`
	Cause      string `json:"cause,omitempty"`
	Sender     string `json:"sender,omitempty"`
	Source     string `json:"source"`
	NewText    string `json:"new_text,omitempty"`
	Value      string `json:"value,omitempty"`
	Timestamp  int64  `json:"timestamp"`
	ReceivedAt int64  `json:"received_at"`
}
type jsonMedia struct {
	Type       string `json:"type"`
	MIME       string `json:"mime,omitempty"`
	Filename   string `json:"filename,omitempty"`
	SHA256     string `json:"sha256,omitempty"`
	State      string `json:"state"`
	Reason     string `json:"reason,omitempty"`
	ErrorClass string `json:"error_class,omitempty"`
	Size       int64  `json:"size"`
}
type jsonChatEvent struct {
	EventID    string `json:"event_id"`
	Kind       string `json:"kind"`
	Sender     string `json:"sender,omitempty"`
	Value      string `json:"value,omitempty"`
	Timestamp  int64  `json:"timestamp"`
	ReceivedAt int64  `json:"received_at"`
}

func exportJSONRecord(r store.ExportRecord) jsonMessage {
	m := r.Message
	j := jsonMessage{ID: m.ID, Sender: m.SenderJID, SenderName: r.SenderName, Kind: m.Kind, Text: m.Text, Caption: m.Caption, QuotedID: m.QuotedID, QuotedSender: m.QuotedSender, Source: m.Source, Details: json.RawMessage(m.Details), Mentions: m.Mentions, Timestamp: m.Timestamp, ReceivedAt: m.ReceivedAt, FromMe: m.FromMe, Forwarded: m.Forwarded, ViewOnce: m.ViewOnce, ForwardScore: m.ForwardScore, EphemeralSecs: m.EphemeralSecs}
	if r.Media != nil {
		v := r.Media
		j.Media = &jsonMedia{v.Type, v.MIME, v.Filename, v.SHA256, v.State, v.Reason, v.ErrorClass, v.DeclaredSize}
	}
	return j
}
func (s *Server) streamJSON(ctx context.Context, w io.Writer, x exportScope) error {
	meta := map[string]any{"schema_version": "v1", "archive_timezone": x.loc.String(), "canonical_jid": x.jid, "aliases": x.aliases, "generated_at": x.generated.In(x.loc).Format(time.RFC3339), "filters": map[string]int64{"from": x.from, "to": x.to}}
	b, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	if _, err = fmt.Fprintf(w, "{\"metadata\":%s,\"messages\":[", b); err != nil {
		return err
	}
	first := true
	count := 0
	err = s.eachExport(ctx, x, func(r store.ExportRecord) error {
		if !first {
			if _, e := io.WriteString(w, ","); e != nil {
				return e
			}
		}
		first = false
		count++
		return json.NewEncoder(w).Encode(exportJSONRecord(r))
	})
	if err != nil {
		return err
	}
	if _, err = io.WriteString(w, "],\"message_events\":["); err != nil {
		return err
	}
	eventCount, chatCount := 0, 0
	first = true
	err = s.eachEvent(ctx, x, func(e store.ExportEvent) error {
		if e.Message == nil {
			return nil
		}
		if !first {
			if _, z := io.WriteString(w, ","); z != nil {
				return z
			}
		}
		first = false
		eventCount++
		m := e.Message
		return json.NewEncoder(w).Encode(jsonEvent{m.EventID, m.Kind, m.SourceID, m.Cause, m.SenderJID, m.Source, m.NewText, m.Value, m.Timestamp, m.ReceivedAt})
	})
	if err != nil {
		return err
	}
	if _, err = io.WriteString(w, "],\"chat_events\":["); err != nil {
		return err
	}
	first = true
	err = s.eachEvent(ctx, x, func(e store.ExportEvent) error {
		if e.Chat == nil {
			return nil
		}
		if !first {
			if _, z := io.WriteString(w, ","); z != nil {
				return z
			}
		}
		first = false
		chatCount++
		c := e.Chat
		return json.NewEncoder(w).Encode(jsonChatEvent{c.EventID, c.Kind, c.SenderJID, c.Value, c.Timestamp, c.ReceivedAt})
	})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "],\"counts\":{\"messages\":%d,\"message_events\":%d,\"chat_events\":%d,\"events\":%d}}", count, eventCount, chatCount, eventCount+chatCount)
	return err
}

func (s *Server) streamHTML(ctx context.Context, w io.Writer, x exportScope) error {
	rangeLabel := "All archived messages"
	if x.from > 0 || x.to > 0 {
		from, to := "start of archive", "end of archive"
		if x.from > 0 {
			from = time.Unix(x.from, 0).In(x.loc).Format("2006-01-02")
		}
		if x.to > 0 {
			to = time.Unix(x.to, 0).In(x.loc).Format("2006-01-02")
		}
		rangeLabel = "Applied range: " + from + " to " + to
	}
	if _, err := fmt.Fprintf(w, "<!doctype html><meta charset=utf-8><title>%s</title><style>body{font:16px system-ui;max-width:60rem;margin:auto}.m{border-bottom:1px solid #ccc;padding:1rem;white-space:pre-wrap}.event{color:#555}img,video{max-width:100%%}</style><h1>%s</h1><p>Schema version: v1 · Canonical chat: %s · Time zone: %s · Generated: %s</p><p>Aliases included: %s</p><p>%s</p>", template.HTMLEscapeString(x.name), template.HTMLEscapeString(x.name), template.HTMLEscapeString(x.jid), template.HTMLEscapeString(x.loc.String()), x.generated.In(x.loc).Format(time.RFC3339), template.HTMLEscapeString(strings.Join(x.aliases, ", ")), template.HTMLEscapeString(rangeLabel)); err != nil {
		return err
	}
	var embedded int64
	var messages, messageEvents, chatEvents int
	err := s.eachTimeline(ctx, x, func(r *store.ExportRecord, e *store.ExportEvent) error {
		if e != nil {
			if e.Message != nil {
				messageEvents++
			} else if e.Chat != nil {
				chatEvents++
			}
			label := exportEventLabel(*e)
			if e.Message != nil {
				label = htmlEventLabel(*e.Message)
			}
			_, err := fmt.Fprintf(w, "<article class=\"m event\"><header>%s</header><div dir=auto>%s</div></article>", mTime(e.TS, x.loc), template.HTMLEscapeString(label))
			return err
		}
		messages++
		m := r.Message
		sender := r.SenderName
		if sender == "" {
			sender = m.SenderJID
		}
		body := messageDisplay(m)
		if _, err := fmt.Fprintf(w, "<article class=m><header>%s · %s</header><div dir=auto>%s</div>", template.HTMLEscapeString(sender), mTime(m.Timestamp, x.loc), template.HTMLEscapeString(body)); err != nil {
			return err
		}
		if r.Media != nil {
			if err := s.htmlMediaWithOriginAndLimits(w, x.jid, x.origin, r.Media, &embedded, defaultHTMLEmbedLimits); err != nil {
				return err
			}
		}
		_, err := io.WriteString(w, "</article>")
		return err
	})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "<footer><p>Messages: %d · Message events: %d · Chat events: %d</p></footer>", messages, messageEvents, chatEvents)
	return err
}

func (s *Server) streamText(ctx context.Context, w io.Writer, x exportScope) error {
	x.snapshot.ResetMediaNames()
	return s.eachTextTimeline(ctx, x, func(r *store.ExportTextRecord, e *store.ExportEvent) error {
		if e != nil {
			return writeWALine(w, time.Unix(e.TS, 0).In(x.loc), exportEventSender(*e), exportEventLabel(*e))
		}
		m := r.Message
		sender := m.PushName
		if sender == "" {
			sender = m.SenderJID
		}
		body := messageDisplay(m)
		if r.Media != nil && r.Media.Filename != "" {
			if body != "" {
				body += " "
			}
			name := exportArchiveName(x, store.ExportRecord{Message: m, Media: r.Media, MediaNameCount: r.MediaNameCount})
			body += "[attachment: " + name + "]"
		}
		if err := writeWALine(w, time.Unix(m.Timestamp, 0).In(x.loc), sender, body); err != nil {
			return err
		}
		return nil
	})
}

var unsafeMediaName = regexp.MustCompile(`[^A-Za-z0-9._-]`)

func safeMediaName(filename, id string) string {
	name := filepath.Base(strings.ReplaceAll(filename, "\\", "/"))
	name = strings.Trim(unsafeMediaName.ReplaceAllString(name, "_"), "_.")
	if name == "" {
		name = safeExportName(id)
	}
	const maxNameBytes = 120
	if len(name) > maxNameBytes {
		ext := filepath.Ext(name)
		if len(ext) > 16 {
			ext = ext[:16]
		}
		name = strings.TrimRight(name[:maxNameBytes-len(ext)], "_.") + ext
	}
	return name
}
