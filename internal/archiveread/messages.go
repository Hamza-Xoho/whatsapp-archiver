package archiveread

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"sort"
	"strings"
	"time"

	"whatsapp-archiver/internal/store"
)

type Attachment struct {
	Type         string  `json:"type"`
	MIME         string  `json:"mime"`
	DeclaredSize int64   `json:"declared_size"`
	Filename     *string `json:"filename"`
	State        string  `json:"state"`
}
type Message struct {
	ID             string      `json:"id"`
	MessageRef     string      `json:"message_ref"`
	ChatID         string      `json:"chat_id"`
	StoredChatID   string      `json:"stored_chat_id"`
	SenderID       string      `json:"sender_id"`
	StoredSenderID string      `json:"stored_sender_id"`
	Timestamp      int64       `json:"timestamp"`
	ReceivedAt     int64       `json:"received_at"`
	FromMe         *bool       `json:"from_me"`
	Kind           string      `json:"kind"`
	Text           string      `json:"text"`
	Caption        *string     `json:"caption"`
	Snippet        string      `json:"snippet,omitempty"`
	Revoked        bool        `json:"revoked"`
	Edited         bool        `json:"edited"`
	Attachment     *Attachment `json:"attachment"`
	QuotedRef      string      `json:"quoted_ref,omitempty"`
	ViewerURL      string      `json:"viewer_url,omitempty"`
	Clipped        []string    `json:"clipped"`
}

func (m Message) key() orderKey { return orderKey{TS: m.Timestamp, Stored: m.StoredChatID, ID: m.ID} }
func keyLess(a, b orderKey) bool {
	if a.TS != b.TS {
		return a.TS < b.TS
	}
	if a.Stored != b.Stored {
		return a.Stored < b.Stored
	}
	return a.ID < b.ID
}
func (m *Message) clipBody(n int) {
	var clipped bool
	m.Text, clipped = clip(m.Text, n)
	if clipped {
		m.Clipped = appendClip(m.Clipped, "text")
	}
	if m.Caption != nil {
		c, cut := clip(*m.Caption, n)
		m.Caption = &c
		if cut {
			m.Clipped = appendClip(m.Clipped, "caption")
		}
	}
}

type messageQuery struct {
	aliases    []string
	sender     []string
	from, to   *int64
	id         string
	key        *orderKey
	descending bool
	max        int
	search     bool
}

func makeQuery(s *snapshot, q messageQuery) (string, []any, error) {
	if len(q.aliases) == 0 {
		return "", nil, nil
	}
	if len(q.aliases) > 10000 {
		return "", nil, failure("QUERY_TOO_BROAD")
	}
	vals := make([]string, 0, len(q.aliases))
	args := []any{}
	for _, alias := range q.aliases {
		vals = append(vals, "(?,?)")
		args = append(args, alias, s.ids.resolve(alias))
	}
	// Candidate selection touches only keys and sender metadata. Authorization is
	// explicit here; edit/media/body projection happens after the bounded ordering.
	where := []string{"m.chat_jid IN (" + placeholders(len(q.aliases)) + ")"}
	args = append(args, stringsAny(q.aliases)...)
	if len(q.aliases) > 1 {
		where = append(where, "NOT EXISTS(SELECT 1 FROM messages d WHERE d.chat_jid IN (SELECT da.alias FROM aliases da WHERE da.canonical=(SELECT ca.canonical FROM aliases ca WHERE ca.alias=m.chat_jid)) AND d.id=m.id AND (d.received_at<m.received_at OR (d.received_at=m.received_at AND d.chat_jid<m.chat_jid)))")
	}
	if q.id != "" {
		where = append(where, "m.id=?")
		args = append(args, q.id)
	}
	if q.from != nil {
		where = append(where, "m.ts>=?")
		args = append(args, *q.from)
	}
	if q.to != nil {
		where = append(where, "m.ts<?")
		args = append(args, *q.to)
	}
	if len(q.sender) > 0 {
		where = append(where, "m.sender_jid IN ("+placeholders(len(q.sender))+")")
		args = append(args, stringsAny(q.sender)...)
	}
	if q.key != nil {
		op, bound := ">", ">="
		if q.descending {
			op, bound = "<", "<="
		}
		where = append(where, "m.ts "+bound+" ? AND (m.ts,m.chat_jid,m.id) "+op+" (?,?,?)")
		args = append(args, q.key.TS, q.key.TS, q.key.Stored, q.key.ID)
	}
	direction := " ASC"
	if q.descending {
		direction = " DESC"
	}
	order := "m.ts" + direction + ",m.chat_jid" + direction + ",m.id" + direction
	candidate := "SELECT m.chat_jid,m.id,m.ts FROM messages m WHERE " + strings.Join(where, " AND ") + " ORDER BY " + order
	if q.max > 0 {
		candidate += " LIMIT ?"
		args = append(args, q.max)
	}
	// The materialized key page prevents SQLite from evaluating bodies/events for
	// every row before its ORDER BY/LIMIT. Existing chat_ts indexes remain usable.
	prefix := "WITH aliases(alias,canonical) AS (VALUES " + strings.Join(vals, ",") + "), candidates AS MATERIALIZED (" + candidate + ") "
	revoke := "EXISTS(SELECT 1 FROM message_events e WHERE e.chat_jid IN (SELECT ae.alias FROM aliases ae WHERE ae.canonical=a.canonical) AND e.target_id=m.id AND e.kind='revoke')"
	edit := "(SELECT e.new_text FROM message_events e WHERE e.chat_jid IN (SELECT ae.alias FROM aliases ae WHERE ae.canonical=a.canonical) AND e.target_id=m.id AND e.kind='edit' ORDER BY e.ts DESC,e.received_at DESC,e.event_id DESC LIMIT 1)"
	body := "CASE WHEN " + revoke + " THEN '' ELSE COALESCE(" + edit + ",m.text) END"
	caption, kind, fromMe := "NULL", "CASE WHEN m.media_type='' THEN 'text' ELSE m.media_type END", "NULL"
	if s.family == "v1" {
		caption = "CASE WHEN " + revoke + " OR " + edit + " IS NOT NULL THEN '' ELSE m.caption END"
		kind = "m.kind"
		fromMe = "m.from_me"
	}
	safeMedia := func(col string) string {
		return "CASE WHEN " + revoke + " THEN NULL ELSE (SELECT md." + col + " FROM media md WHERE md.chat_jid=m.chat_jid AND md.message_id=m.id LIMIT 1) END"
	}
	filename := "NULL"
	if s.family == "v1" {
		filename = safeMedia("filename")
	}
	cap := 8000
	if q.search {
		cap = 65536
	}
	// Byte prefixes preserve NUL and cap transfer before rune-aware Go clipping.
	bounded := boundedSQL
	selected := []string{bounded("m.chat_jid", 512), bounded("m.id", 1024), bounded("m.sender_jid", 512), "m.ts", "m.received_at", fromMe, bounded(kind, 64), bounded(body, cap), bounded(caption, cap), bounded("CASE WHEN "+revoke+" THEN '' ELSE m.quoted_id END", 1024), revoke, edit + " IS NOT NULL", bounded(safeMedia("type"), 64), bounded(safeMedia("mime"), 512), safeMedia("declared_size"), bounded(filename, cap), bounded(safeMedia("state"), 64)}
	return prefix + "SELECT " + strings.Join(selected, ",") + " FROM candidates k JOIN messages m ON m.chat_jid=k.chat_jid AND m.id=k.id JOIN aliases a ON a.alias=m.chat_jid ORDER BY " + order, args, nil
}
func (s *snapshot) query(ctx context.Context, q messageQuery) (*sql.Rows, error) {
	text, args, err := makeQuery(s, q)
	if err != nil || text == "" {
		return nil, err
	}
	return s.tx.QueryContext(ctx, text, args...)
}
func (s *snapshot) scan(rows *sql.Rows, search bool) (Message, error) {
	var m Message
	var fromMe sql.NullInt64
	var caption, mediaType, mime, filename, state sql.NullString
	var size sql.NullInt64
	var quoted string
	var chatCut, idCut, senderCut, kindCut, textCut, captionCut, quoteCut, typeCut, mimeCut, filenameCut, stateCut bool
	err := rows.Scan(&m.StoredChatID, &chatCut, &m.ID, &idCut, &m.StoredSenderID, &senderCut, &m.Timestamp, &m.ReceivedAt, &fromMe, &m.Kind, &kindCut, &m.Text, &textCut, &caption, &captionCut, &quoted, &quoteCut, &m.Revoked, &m.Edited, &mediaType, &typeCut, &mime, &mimeCut, &size, &filename, &filenameCut, &state, &stateCut)
	if err != nil {
		return m, err
	}
	cap := 8000
	if search {
		cap = 65536
	}
	for _, field := range []struct {
		value *string
		cut   *bool
		limit int
	}{{&m.StoredChatID, &chatCut, 512}, {&m.ID, &idCut, 1024}, {&m.StoredSenderID, &senderCut, 512}, {&m.Kind, &kindCut, 64}, {&m.Text, &textCut, cap}, {&caption.String, &captionCut, cap}, {&quoted, &quoteCut, 1024}, {&mediaType.String, &typeCut, 64}, {&mime.String, &mimeCut, 512}, {&filename.String, &filenameCut, cap}, {&state.String, &stateCut, 64}} {
		value, cut, finishErr := finishPrefix(*field.value, *field.cut, field.limit)
		if finishErr != nil {
			return m, finishErr
		}
		*field.value = value
		*field.cut = cut
	}
	if chatCut || idCut || senderCut || quoteCut {
		return m, failure("QUERY_TOO_BROAD")
	}
	m.Clipped = []string{}
	for _, field := range []struct {
		cut  bool
		name string
	}{{kindCut, "kind"}, {textCut, "text"}, {captionCut, "caption"}, {typeCut, "attachment.type"}, {mimeCut, "attachment.mime"}, {filenameCut, "attachment.filename"}, {stateCut, "attachment.state"}} {
		if field.cut {
			m.addClip(field.name)
		}
	}
	if search && (textCut || captionCut || filenameCut) {
		return m, failure("QUERY_TOO_BROAD")
	}
	m.ChatID = s.ids.resolve(m.StoredChatID)
	m.SenderID = s.ids.resolve(m.StoredSenderID)
	m.MessageRef = messageRef(m.StoredChatID, m.ID)
	if m.Revoked {
		m.Kind = "deleted"
	}
	if fromMe.Valid {
		v := fromMe.Int64 != 0
		m.FromMe = &v
	}
	if caption.Valid {
		v := caption.String
		m.Caption = &v
	}
	if quoted != "" && !m.Revoked {
		m.QuotedRef = messageRef(m.StoredChatID, quoted)
	}
	if mediaType.Valid && !m.Revoked {
		m.Attachment = &Attachment{Type: mediaType.String, MIME: mime.String, DeclaredSize: size.Int64, State: state.String}
		if filename.Valid {
			v := filename.String
			m.Attachment.Filename = &v
		}
	}
	if s.cfg.ViewerBaseURL != "" {
		m.ViewerURL = strings.TrimRight(s.cfg.ViewerBaseURL, "/") + "/chats/" + url.PathEscape(m.StoredChatID) + "/m/" + url.PathEscape(m.ID)
	}
	return m, nil
}
func (m *Message) addClip(field string) {
	for _, f := range m.Clipped {
		if f == field {
			return
		}
	}
	m.Clipped = append(m.Clipped, field)
}
func (m *Message) present() {
	m.clipBody(8000)
	if m.Attachment != nil && m.Attachment.Filename != nil {
		v, cut := clip(*m.Attachment.Filename, 512)
		m.Attachment.Filename = &v
		if cut {
			m.addClip("attachment.filename")
		}
	}
}

func (s *snapshot) find(ctx context.Context, chat, id string) (Message, error) {
	if !s.ids.permitted(chat) {
		return Message{}, failure("NOT_FOUND_OR_NOT_ALLOWED")
	}
	q := messageQuery{aliases: s.ids.groups[s.ids.resolve(chat)], id: id, max: 1}
	rows, err := s.query(ctx, q)
	if err != nil {
		return Message{}, err
	}
	if rows == nil {
		return Message{}, failure("NOT_FOUND_OR_NOT_ALLOWED")
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err = rows.Err(); err != nil {
			return Message{}, err
		}
		return Message{}, failure("NOT_FOUND_OR_NOT_ALLOWED")
	}
	return s.scan(rows, false)
}

func (e *Engine) readMessages(ctx context.Context, s *snapshot, tool string, a arguments, r *Result) error {
	if tool == "get_message" || tool == "get_context" {
		chat, id, err := parseRef(a.MessageRef)
		if err != nil {
			return err
		}
		anchor, err := s.find(ctx, chat, id)
		if err != nil {
			return err
		}
		r.Scope["chat_id"] = anchor.ChatID
		if tool == "get_message" {
			anchor.present()
			for n := 4000; ; n /= 2 {
				r.Data = map[string]any{"message": anchor}
				b, _ := json.Marshal(r)
				if len(b) <= responseBudget {
					return nil
				}
				if n < 50 {
					return failure("QUERY_TOO_BROAD")
				}
				anchor.clipBody(n)
			}
		}
		return e.context(ctx, s, a, r, anchor)
	}
	hash := filterHash(tool, a)
	cursor, err := e.decodeCursor(a.Cursor, s, hash)
	if err != nil {
		return err
	}
	q := messageQuery{aliases: s.ids.permittedAliases(), descending: tool == "search_messages" || (a.From == "" && a.To == "")}
	limit := a.limit(20)
	if tool == "read_conversation" {
		limit = a.limit(50)
	}
	if a.ChatID != "" {
		if !s.ids.permitted(a.ChatID) {
			return failure("NOT_FOUND_OR_NOT_ALLOWED")
		}
		c := s.ids.resolve(a.ChatID)
		q.aliases = s.ids.groups[c]
		r.Scope["chat_id"] = c
	}
	if a.SenderID != "" {
		sender := s.ids.resolve(a.SenderID)
		q.sender = s.ids.groups[sender]
		if len(q.sender) == 0 {
			q.sender = []string{a.SenderID}
		}
		r.Scope["sender_id"] = a.SenderID
	}
	var from, to int64
	if a.From != "" {
		from = ceilInstant(a.From)
		q.from = &from
		r.Scope["from"] = a.From
	}
	if a.To != "" {
		to = ceilInstant(a.To)
		q.to = &to
		r.Scope["to"] = a.To
	}
	if tool == "search_messages" && a.From == "" && a.To == "" && !a.AllTime {
		from = time.Now().Add(-30 * 24 * time.Hour).Unix()
		if a.Cursor != "" {
			from = cursor.From
		}
		q.from = &from
		r.Scope["from"] = time.Unix(from, 0).UTC().Format(time.RFC3339)
	}
	r.Scope["all_time"] = a.AllTime
	direction := "forward"
	if q.descending {
		direction = "older"
	}
	r.Scope["direction"] = direction
	if a.Cursor != "" {
		q.key = &cursor.Last
	}
	q.max = limit + 1
	q.search = tool == "search_messages"
	if q.search {
		q.max = 128
	}
	out := []Message{}
	more := false
	var last orderKey
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rows, queryErr := s.query(ctx, q)
		if queryErr != nil {
			return queryErr
		}
		if rows == nil {
			break
		}
		scanned := 0
		stop := false
		var scannedKey orderKey
		for rows.Next() {
			if ctx.Err() != nil {
				_ = rows.Close()
				return ctx.Err()
			}
			m, scanErr := s.scan(rows, q.search)
			if scanErr != nil {
				_ = rows.Close()
				return scanErr
			}
			scanned++
			scannedKey = m.key()
			if q.search {
				text := m.Text
				if m.Caption != nil {
					text += "\n" + *m.Caption
				}
				if m.Attachment != nil && m.Attachment.Filename != nil {
					text += "\n" + *m.Attachment.Filename
				}
				if ctx.Err() != nil {
					_ = rows.Close()
					return ctx.Err()
				}
				folded := store.Fold(text)
				if ctx.Err() != nil {
					_ = rows.Close()
					return ctx.Err()
				}
				if m.Revoked || !strings.Contains(folded, store.Fold(a.Query)) {
					continue
				}
				var clipped bool
				m.Snippet, clipped = clip(text, 500)
				if clipped {
					m.addClip("snippet")
				}
			}
			if len(out) >= limit {
				more = true
				stop = true
				break
			}
			m.present()
			candidate := append(out, m)
			r.Data = map[string]any{"messages": candidate}
			encoded, _ := json.Marshal(r)
			if len(encoded) > responseBudget-2048 {
				if len(out) > 0 {
					more = true
					stop = true
					break
				}
				for n := 4000; n >= 50; n /= 2 {
					m.clipBody(n)
					candidate = []Message{m}
					r.Data = map[string]any{"messages": candidate}
					encoded, _ = json.Marshal(r)
					if len(encoded) <= responseBudget-2048 {
						break
					}
				}
				if len(encoded) > responseBudget-2048 {
					_ = rows.Close()
					return failure("QUERY_TOO_BROAD")
				}
			}
			out = candidate
			last = m.key()
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return err
		}
		if stop || !q.search || scanned < q.max {
			break
		}
		q.key = &scannedKey
	}

	if tool == "read_conversation" && q.descending {
		sort.Slice(out, func(i, j int) bool { return keyLess(out[i].key(), out[j].key()) })
	}
	r.Data = map[string]any{"messages": out}
	r.HasMore = more
	if more {
		r.NextCursor = e.encodeCursor(s, hash, last, from, to)
	}
	return nil
}
func (e *Engine) context(ctx context.Context, s *snapshot, a arguments, r *Result, anchor Message) error {
	before, after := 5, 5
	if a.Before != nil {
		before = *a.Before
	}
	if a.After != nil {
		after = *a.After
	}
	out := []Message{anchor}
	key := anchor.key()
	for _, side := range []struct {
		n    int
		desc bool
	}{{before, true}, {after, false}} {
		if side.n == 0 {
			continue
		}
		rows, err := s.query(ctx, messageQuery{aliases: s.ids.groups[anchor.ChatID], key: &key, descending: side.desc, max: side.n})
		if err != nil {
			return err
		}
		if rows == nil {
			continue
		}
		for rows.Next() {
			m, scanErr := s.scan(rows, false)
			if scanErr != nil {
				_ = rows.Close()
				return scanErr
			}
			out = append(out, m)
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return err
		}
	}
	sort.Slice(out, func(i, j int) bool { return keyLess(out[i].key(), out[j].key()) })
	for j := range out {
		out[j].present()
	}
	for n := 4000; ; n /= 2 {
		r.Data = map[string]any{"messages": out, "anchor_ref": anchor.MessageRef}
		encoded, _ := json.Marshal(r)
		if len(encoded) <= responseBudget {
			return nil
		}
		if n < 50 {
			return failure("QUERY_TOO_BROAD")
		}
		for j := range out {
			out[j].clipBody(n)
		}
	}
}

func appendClip(fields []string, field string) []string {
	for _, f := range fields {
		if f == field {
			return fields
		}
	}
	return append(fields, field)
}
