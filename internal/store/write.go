package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// Batch is everything one event writes (R2): its chat and sender directory rows, at most one of Message, Event and
// ChatEvent, and the media row of a message. Apply writes it in one transaction. Media may also stand alone (a queue
// row rebuilt without its message).
type Batch struct {
	Chat      *Chat
	Sender    *Participant
	Message   *Message
	Event     *MessageEvent
	ChatEvent *ChatEvent
	Media     *Media
}

// Applied reports what Apply inserted. New is true when the batch's message, event or chat event was a new row (false
// for a duplicate, which writes nothing); Seq is its messages.seq or message_events.seq (0 for a chat event or a
// duplicate). MediaNew is true when a media row was inserted.
type Applied struct {
	New, MediaNew bool
	Seq           int64
}

// Apply writes the batch in one transaction and one commit; a failure leaves nothing. A batch that can never succeed
// (an unknown kind, bad details, a message and an event together) fails with ErrConstraint before anything is written.
//
// Inside the transaction: the canonical rows of every JID the batch stores (schema 4.3 step 1), the chat and
// participant upserts, then the one raw row. A message is a duplicate when it exists under any alias of its chat; its
// media is then not queued again.
func (s *Store) Apply(ctx context.Context, b Batch) (Applied, error) {
	w, err := prepare(b)
	if err != nil {
		return Applied{}, err
	}
	if w.empty() {
		return Applied{}, nil
	}
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return Applied{}, fmt.Errorf("begin apply: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	res, err := w.run(ctx, tx)
	if err != nil {
		return Applied{}, err
	}
	if err = tx.Commit(); err != nil {
		return Applied{}, fmt.Errorf("commit apply: %w", classify(err))
	}
	return res, nil
}

// batch is a validated copy of a Batch with defaults applied and JSON encoded; the caller's values are untouched.
type batch struct {
	chat      *Chat
	sender    *Participant
	msg       *Message
	mentions  string
	details   string
	event     *MessageEvent
	chatEvent *ChatEvent
	media     *Media
}

func (w batch) empty() bool {
	return w.chat == nil && w.sender == nil && w.msg == nil && w.event == nil && w.chatEvent == nil && w.media == nil
}

func prepare(b Batch) (batch, error) {
	var w batch
	rows := 0
	for _, present := range []bool{b.Message != nil, b.Event != nil, b.ChatEvent != nil} {
		if present {
			rows++
		}
	}
	if rows > 1 {
		return w, invalid("a batch holds one of message, event and chat event, not several")
	}
	if b.Chat != nil {
		c := *b.Chat
		w.chat = &c
	}
	if b.Sender != nil {
		p := *b.Sender
		w.sender = &p
	}
	if b.Message != nil {
		m := *b.Message
		if m.Kind == "" {
			m.Kind = "text"
		}
		if !ValidMessageKind(m.Kind) {
			return w, invalid("unknown message kind %q", m.Kind)
		}
		var err error
		if w.details, err = ValidateDetails(m.Kind, m.Details); err != nil {
			return w, err
		}
		mentions := m.Mentions
		if mentions == nil {
			mentions = []string{}
		}
		enc, err := json.Marshal(mentions)
		if err != nil {
			return w, fmt.Errorf("encode mentions: %w", err)
		}
		w.mentions = string(enc)
		if m.RawProto == nil {
			m.RawProto = []byte{}
		}
		w.msg = &m
	}
	if b.Event != nil {
		e := *b.Event
		if !ValidEventKind(e.Kind) {
			return w, invalid("unknown event kind %q", e.Kind)
		}
		if !ValidEventCause(e.Cause) {
			return w, invalid("unknown event cause %q", e.Cause)
		}
		if e.Source == "" {
			e.Source = "live"
		}
		if e.RawProto == nil {
			e.RawProto = []byte{}
		}
		w.event = &e
	}
	if b.ChatEvent != nil {
		e := *b.ChatEvent
		if e.RawProto == nil {
			e.RawProto = []byte{}
		}
		w.chatEvent = &e
	}
	if b.Media != nil {
		m := *b.Media
		if m.Source == "" {
			m.Source = "live"
		}
		if m.SourceRank == 0 { // 0 is "by source"; rank 0 (asked for by the viewer) is set by the queue, never at insert
			m.SourceRank = 2
			if m.Source == "live" {
				m.SourceRank = 1
			}
		}
		if m.Thumb == nil {
			m.Thumb = []byte{}
		}
		w.media = &m
	}
	return w, nil
}

func (w batch) run(ctx context.Context, tx *sql.Tx) (Applied, error) {
	var res Applied
	if err := ensureCanonical(ctx, tx, w.jids()...); err != nil {
		return res, err
	}
	if w.chat != nil {
		changed, err := upsertChat(ctx, tx, *w.chat)
		if err != nil {
			return res, err
		}
		if changed { // the chat is seen (R20) and its name or type may have moved
			if err = chatChanged(ctx, tx, w.chat.JID); err != nil {
				return res, err
			}
		}
	}
	if w.sender != nil {
		changed, err := upsertParticipant(ctx, tx, *w.sender)
		if err != nil {
			return res, err
		}
		if changed { // a push name can be the display name of the sender's own chat
			if _, err = refreshIdentity(ctx, tx, w.sender.JID); err != nil {
				return res, err
			}
		}
	}
	insertMedia := w.media != nil
	switch {
	case w.msg != nil:
		var err error
		if res.New, res.Seq, err = w.insertMessage(ctx, tx); err != nil {
			return res, err
		}
		insertMedia = insertMedia && res.New
		if res.New { // derived tables, same transaction (schema 4.3 step 3)
			if err = w.messageAdded(ctx, tx); err != nil {
				return res, err
			}
			if err = w.indexMessage(ctx, tx, res.Seq); err != nil {
				return res, err
			}
		}
	case w.event != nil:
		var err error
		if res.New, res.Seq, err = w.insertEvent(ctx, tx); err != nil {
			return res, err
		}
		if res.New { // schema 4.3 step 4
			if err = w.eventAdded(ctx, tx); err != nil {
				return res, err
			}
			if err = w.indexEdit(ctx, tx, res.Seq); err != nil {
				return res, err
			}
		}
	case w.chatEvent != nil:
		var err error
		if res.New, err = w.insertChatEvent(ctx, tx); err != nil {
			return res, err
		}
	}
	if insertMedia {
		var err error
		if res.MediaNew, err = w.insertMedia(ctx, tx); err != nil {
			return res, err
		}
		if res.MediaNew {
			if err = mediaMoved(ctx, tx, w.media.ChatJID, "", w.media.State, w.media.DeclaredSize, 1); err != nil {
				return res, err
			}
			if w.msg == nil { // a row that arrives after its message: the file name joins the message's searchable text
				if err = w.reindexForMedia(ctx, tx); err != nil {
					return res, err
				}
			}
		}
	}
	return res, nil
}

// jids lists every non-empty JID the batch stores as a chat or sender.
func (w batch) jids() []string {
	var out []string
	add := func(jid string) {
		if jid != "" {
			out = append(out, jid)
		}
	}
	if w.chat != nil {
		add(w.chat.JID)
	}
	if w.sender != nil {
		add(w.sender.JID)
	}
	if m := w.msg; m != nil {
		add(m.ChatJID)
		add(m.SenderJID)
	}
	if e := w.event; e != nil {
		add(e.ChatJID)
		add(e.SenderJID)
	}
	if e := w.chatEvent; e != nil {
		add(e.ChatJID)
		add(e.SenderJID)
	}
	if m := w.media; m != nil {
		add(m.ChatJID)
	}
	return out
}

// upsertChat reports whether the row was inserted or changed.
func upsertChat(ctx context.Context, tx *sql.Tx, c Chat) (bool, error) {
	// A write that changes nothing is skipped: the row is touched only when the name (an empty one never erases a
	// known one), the type or the parent differs.
	res, err := tx.ExecContext(ctx, `INSERT INTO chats(jid,name,type,parent_jid) VALUES(?1,?2,?3,?4)
 ON CONFLICT(jid) DO UPDATE SET name=COALESCE(NULLIF(excluded.name,''),chats.name),type=excluded.type,parent_jid=excluded.parent_jid
 WHERE COALESCE(NULLIF(excluded.name,''),chats.name) IS NOT chats.name OR excluded.type IS NOT chats.type OR excluded.parent_jid IS NOT chats.parent_jid`,
		c.JID, c.Name, c.Type, c.ParentJID)
	if err != nil {
		return false, fmt.Errorf("upsert chat %s: %w", c.JID, classify(err))
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// upsertParticipant reports whether the row was inserted or changed.
func upsertParticipant(ctx context.Context, tx *sql.Tx, p Participant) (bool, error) {
	at := p.UpdatedAt
	if at == 0 {
		at = nowUnix()
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO participants(jid,push_name,updated_at) VALUES(?1,?2,?3)
 ON CONFLICT(jid) DO UPDATE SET push_name=excluded.push_name,updated_at=excluded.updated_at
 WHERE excluded.push_name<>'' AND excluded.push_name<>participants.push_name`, p.JID, p.PushName, at)
	if err != nil {
		return false, fmt.Errorf("upsert participant %s: %w", p.JID, classify(err))
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (w batch) insertMessage(ctx context.Context, tx *sql.Tx) (isNew bool, seq int64, err error) {
	m := w.msg
	var dup int
	if err = tx.QueryRowContext(ctx, dedupeQuery, m.ChatJID, m.ID).Scan(&dup); err != nil {
		return false, 0, fmt.Errorf("check message alias: %w", classify(err))
	}
	if dup != 0 {
		return false, 0, nil
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO messages(chat_jid,id,sender_jid,from_me,push_name,kind,text,caption,quoted_id,quoted_sender,mentions,forwarded,forward_score,view_once,ephemeral_secs,details,source,ts,received_at,raw_proto)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`,
		m.ChatJID, m.ID, m.SenderJID, m.FromMe, m.PushName, m.Kind, m.Text, m.Caption, m.QuotedID, m.QuotedSender, w.mentions, m.Forwarded, m.ForwardScore, m.ViewOnce, m.EphemeralSecs, w.details, m.Source, m.Timestamp, m.ReceivedAt, m.RawProto)
	if err != nil {
		return false, 0, fmt.Errorf("insert message %s: %w", m.ID, classify(err))
	}
	return inserted(result)
}

func (w batch) insertEvent(ctx context.Context, tx *sql.Tx) (isNew bool, seq int64, err error) {
	e := w.event
	result, err := tx.ExecContext(ctx, `INSERT INTO message_events(chat_jid,target_id,kind,event_id,source_id,cause,sender_jid,source,ts,received_at,new_text,value,raw_proto)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`,
		e.ChatJID, e.TargetID, e.Kind, e.EventID, e.SourceID, e.Cause, e.SenderJID, e.Source, e.Timestamp, e.ReceivedAt, e.NewText, e.Value, e.RawProto)
	if err != nil {
		return false, 0, fmt.Errorf("insert event %s: %w", e.EventID, classify(err))
	}
	return inserted(result)
}

func (w batch) insertChatEvent(ctx context.Context, tx *sql.Tx) (bool, error) {
	e := w.chatEvent
	result, err := tx.ExecContext(ctx, `INSERT INTO chat_events(chat_jid,kind,event_id,sender_jid,ts,received_at,value,raw_proto) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`,
		e.ChatJID, e.Kind, e.EventID, e.SenderJID, e.Timestamp, e.ReceivedAt, e.Value, e.RawProto)
	if err != nil {
		return false, fmt.Errorf("insert chat event %s: %w", e.EventID, classify(err))
	}
	isNew, _, err := inserted(result)
	return isNew, err
}

func (w batch) insertMedia(ctx context.Context, tx *sql.Tx) (bool, error) {
	m := w.media
	result, err := tx.ExecContext(ctx, `INSERT INTO media(chat_jid,message_id,sender_jid,from_me,source,source_rank,type,mime,declared_size,filename,duration_s,width,height,pages,message_ts,enqueued_at,state,reason,error_class,http_status,attempts,next_attempt_at,retry_direct_path,path,sha256,updated_at,done_at,thumb)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(chat_jid,message_id) DO NOTHING`,
		m.ChatJID, m.MessageID, m.SenderJID, m.FromMe, m.Source, m.SourceRank, m.Type, m.MIME, m.DeclaredSize, m.Filename, m.DurationS, m.Width, m.Height, m.Pages, m.MessageTimestamp, m.EnqueuedAt, m.State, m.Reason, m.ErrorClass, m.HTTPStatus, m.Attempts, m.NextAttemptAt, m.RetryDirectPath, m.Path, m.SHA256, m.UpdatedAt, m.DoneAt, m.Thumb)
	if err != nil {
		return false, fmt.Errorf("insert media %s: %w", m.MessageID, classify(err))
	}
	isNew, _, err := inserted(result)
	return isNew, err
}

// inserted reads RowsAffected of an INSERT ... ON CONFLICT DO NOTHING: 1 is a new row (and its rowid), 0 a duplicate.
func inserted(result sql.Result) (isNew bool, id int64, err error) {
	n, err := result.RowsAffected()
	if err != nil || n == 0 {
		return false, 0, err
	}
	id, err = result.LastInsertId()
	return true, id, err
}

// The single-row writers below are one Apply each. Ingest uses Apply directly; these are for callers that write one
// row (tests, tools).

func (s *Store) InsertMessage(ctx context.Context, m Message) (bool, error) {
	a, err := s.Apply(ctx, Batch{Message: &m})
	return a.New, err
}
func (s *Store) InsertEvent(ctx context.Context, e MessageEvent) (bool, error) {
	a, err := s.Apply(ctx, Batch{Event: &e})
	return a.New, err
}
func (s *Store) InsertChatEvent(ctx context.Context, e ChatEvent) (bool, error) {
	a, err := s.Apply(ctx, Batch{ChatEvent: &e})
	return a.New, err
}
func (s *Store) UpsertChat(ctx context.Context, c Chat) error {
	_, err := s.Apply(ctx, Batch{Chat: &c})
	return err
}
func (s *Store) UpsertParticipant(ctx context.Context, p Participant) error {
	_, err := s.Apply(ctx, Batch{Sender: &p})
	return err
}
