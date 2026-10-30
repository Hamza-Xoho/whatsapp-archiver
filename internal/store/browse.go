package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Reads behind the chats screen, the chat viewer and the message trace. All of them use the read pool.

// ChatRow is one line of the chats screen. Logging, HasMediaRule and New take every JID form of the chat into account.
type ChatRow struct {
	ChatStat
	DisplayName   string
	OldestHistory int64
	Logging       bool
	HasMediaRule  bool
	New           bool
}

// ViewMessage is one message as the chat viewer shows it. Text is the text as first received; EditedText is the
// latest edit, if any. Reactions are each reactor's current reaction (a removed reaction is not listed).
type ViewMessage struct {
	ID, Sender, SenderName, Text, EditedText, MediaType, MediaState string
	TS                                                              int64
	Deleted                                                         bool
	Reactions                                                       []string
	QuotedID                                                        string
}

// Trace is everything the archive holds about one message. Message is zero when only events arrived; Gap is true
// when one of them says the message could not be decrypted.
type Trace struct {
	Message Message
	Events  []MessageEvent
	Media   *Media
	Gap     bool
}

// chatListQuery is the chats screen: one row of chat_stats per chat (every chat with a message, a rule, a sighting or a
// directory row, R20), newest activity first, the quiet ones (no message) after the chats that have messages at the same
// time. Rules and sightings are aggregated per canonical through chat_canonical: a chat logs unless some form of it has
// logging off, has a media rule if some form has one, and is new when it was seen and no form was acknowledged.
const chatListQuery = `SELECT s.canonical,s.name,s.messages,s.oldest_ts,s.last_ts,COALESCE(s.oldest_history_ts,0),s.media_failed,s.media_expired,s.media_pending,s.gaps_open,
 COALESCE(r.logging,1),COALESCE(r.media,0),COALESCE(NOT f.acked,0)
FROM chat_stats s
LEFT JOIN (SELECT a.canonical AS c,MIN(x.logging) AS logging,MAX(x.media<>'') AS media FROM chat_rules x JOIN chat_canonical a ON a.alias=x.jid GROUP BY a.canonical) r ON r.c=s.canonical
LEFT JOIN (SELECT a.canonical AS c,MAX(x.acknowledged) AS acked FROM chat_seen x JOIN chat_canonical a ON a.alias=x.jid GROUP BY a.canonical) f ON f.c=s.canonical
ORDER BY s.last_ts DESC,s.messages=0,s.canonical`

// ChatList is the chats screen: every chat with messages, newest activity first, then every chat known only from
// chat_rules, chat_seen or chats (no stored message: zero counts), each once under its canonical JID. DisplayName is the
// chat's name (a group's subject), else the contact name, else the push name, else the JID (the order of nameExpr).
// filter keeps the chats whose name or JID contains it, ignoring case. It reads chat_stats and the rule and sighting
// tables only: O(chats).
func (s *Store) ChatList(ctx context.Context, filter string) ([]ChatRow, error) {
	needle := strings.ToLower(filter)
	var out []ChatRow
	err := s.scanRows(ctx, chatListQuery, nil, func(rows *sql.Rows) error {
		var row ChatRow
		c := &row.ChatStat
		if err := rows.Scan(&c.CanonicalJID, &c.Name, &c.MessageCount, &c.Oldest, &c.Newest, &row.OldestHistory, &c.MediaFailed, &c.MediaExpired, &c.MediaPending, &c.OpenGaps, &row.Logging, &row.HasMediaRule, &row.New); err != nil {
			return err
		}
		row.DisplayName = c.Name
		if row.DisplayName == "" {
			row.DisplayName = c.CanonicalJID
		}
		if needle != "" && !strings.Contains(strings.ToLower(row.DisplayName), needle) && !strings.Contains(strings.ToLower(c.CanonicalJID), needle) {
			return nil
		}
		out = append(out, row)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("chat list: %w", err)
	}
	return out, nil
}

// nameOfJID is the display name of the chat of the JID in column col (nameExpr over its canonical JID).
func nameOfJID(col string) string {
	return nameExpr(`COALESCE((SELECT canonical FROM chat_canonical WHERE alias=` + col + `),` + col + `)`)
}

// ChatName is the chat's name, else the contact name, else the push name, over all its JID forms; "" when the archive
// knows none of them.
func (s *Store) ChatName(ctx context.Context, jid string) (string, error) {
	var name string
	if err := s.read.QueryRowContext(ctx, `SELECT `+nameOfJID("?1"), jid).Scan(&name); err != nil {
		return "", fmt.Errorf("chat name of %s: %w", jid, err)
	}
	return name, nil
}

// senderNames maps each JID to its display name (nameOfJID) in one query; a JID without a name is absent.
func (s *Store) senderNames(ctx context.Context, jids []string) (map[string]string, error) {
	out := make(map[string]string, len(jids))
	if len(jids) == 0 {
		return out, nil
	}
	args := make([]any, len(jids))
	marks := make([]string, len(jids))
	for i, j := range jids {
		args[i] = j
		marks[i] = fmt.Sprintf("(?%d)", i+1)
	}
	err := s.scanRows(ctx, `WITH s(j) AS (VALUES `+strings.Join(marks, ",")+`) SELECT j,`+nameOfJID("s.j")+` FROM s`, args, func(rows *sql.Rows) error {
		var j, name string
		if err := rows.Scan(&j, &name); err != nil {
			return err
		}
		if name != "" {
			out[j] = name
		}
		return nil
	})
	return out, err
}

// chatPageQuery is messages_resolved for one chat: its messages under every JID form (chatForms of ?1), each id once
// (the first received copy), newest first, at most ?3, and, when ?2 > 0, after the cursor (?2, ?4) in that order:
// older than ?2, or at ?2 with an id below ?4. The dedupe rank is computed over all the chat's rows before the cursor
// filter. Restricting to the chat's forms before the window function keeps the cost to the chat's own rows instead of
// the whole archive.
const chatPageQuery = `WITH m AS (
 SELECT chat_jid,id,sender_jid,ts,CASE WHEN text<>'' THEN text ELSE caption END AS body,quoted_id,CASE WHEN kind IN ('image','video','gif','audio','voice','document','sticker') THEN kind ELSE '' END AS media_type,
  ROW_NUMBER() OVER (PARTITION BY id ORDER BY received_at,chat_jid) AS rk
 FROM messages WHERE chat_jid IN (` + chatForms + `)
)
SELECT m.id,m.sender_jid,m.body,m.media_type,m.ts,m.quoted_id
FROM m
WHERE m.rk=1 AND (?2<=0 OR m.ts<?2 OR (m.ts=?2 AND m.id<?4))
ORDER BY m.ts DESC,m.id DESC LIMIT ?3`

// ChatPage is one page of the viewer: at most limit messages of the chat after the cursor (beforeTS, beforeID) in
// newest-first order (all when beforeTS is 0), newest first, with their edits, deletion, reactions and media state.
func (s *Store) ChatPage(ctx context.Context, canonicalJID string, beforeTS int64, beforeID string, limit int) ([]ViewMessage, error) {
	var page []ViewMessage
	err := s.scanRows(ctx, chatPageQuery, []any{canonicalJID, beforeTS, limit, beforeID}, func(rows *sql.Rows) error {
		var m ViewMessage
		if err := rows.Scan(&m.ID, &m.Sender, &m.Text, &m.MediaType, &m.TS, &m.QuotedID); err != nil {
			return err
		}
		page = append(page, m)
		return nil
	})
	if err != nil || len(page) == 0 {
		return page, err
	}
	var senders []string
	for _, m := range page {
		if m.Sender != "" && !slices.Contains(senders, m.Sender) {
			senders = append(senders, m.Sender)
		}
	}
	names, err := s.senderNames(ctx, senders)
	if err != nil {
		return nil, err
	}
	for i := range page {
		page[i].SenderName = names[page[i].Sender]
	}
	at := make(map[string]int, len(page))
	args := []any{canonicalJID}
	marks := make([]string, 0, len(page))
	for i, m := range page {
		at[m.ID] = i
		args = append(args, m.ID)
		marks = append(marks, fmt.Sprintf("?%d", i+2))
	}
	in := strings.Join(marks, ",")
	reactions := map[string]map[string]string{} // message id -> reactor -> current reaction
	err = s.scanRows(ctx, `SELECT target_id,kind,sender_jid,new_text,value FROM message_events WHERE chat_jid IN (`+chatForms+`) AND kind IN ('edit','revoke','reaction') AND target_id IN (`+in+`) ORDER BY ts,received_at,event_id`, args, func(rows *sql.Rows) error {
		var id, kind, sender, newText, value string
		if err := rows.Scan(&id, &kind, &sender, &newText, &value); err != nil {
			return err
		}
		m := &page[at[id]]
		switch kind {
		case "edit":
			m.EditedText = newText
		case "revoke":
			m.Deleted = true
		case "reaction":
			if reactions[id] == nil {
				reactions[id] = map[string]string{}
			}
			reactions[id][sender] = value
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for id, by := range reactions {
		reactors := make([]string, 0, len(by))
		for r := range by {
			reactors = append(reactors, r)
		}
		sort.Strings(reactors)
		for _, r := range reactors {
			if by[r] != "" {
				page[at[id]].Reactions = append(page[at[id]].Reactions, by[r])
			}
		}
	}
	err = s.scanRows(ctx, `SELECT message_id,state FROM media WHERE chat_jid IN (`+chatForms+`) AND message_id IN (`+in+`) ORDER BY chat_jid`, args, func(rows *sql.Rows) error {
		var id, state string
		if err := rows.Scan(&id, &state); err != nil {
			return err
		}
		if m := &page[at[id]]; m.MediaState == "" {
			m.MediaState = state
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return page, nil
}

// Trace reads message id of the chat (under any of its JID forms), every event about it, and its media row. It
// returns sql.ErrNoRows when the archive holds none of them.
func (s *Store) Trace(ctx context.Context, chatJID, id string) (Trace, error) {
	var t Trace
	m := &t.Message
	var mentions string
	err := s.read.QueryRowContext(ctx, `SELECT chat_jid,id,sender_jid,from_me,push_name,kind,text,caption,quoted_id,quoted_sender,mentions,forwarded,forward_score,view_once,ephemeral_secs,details,source,ts,received_at,raw_proto FROM messages WHERE id=?2 AND chat_jid IN (`+chatForms+`) ORDER BY received_at,chat_jid LIMIT 1`, chatJID, id).
		Scan(&m.ChatJID, &m.ID, &m.SenderJID, &m.FromMe, &m.PushName, &m.Kind, &m.Text, &m.Caption, &m.QuotedID, &m.QuotedSender, &mentions, &m.Forwarded, &m.ForwardScore, &m.ViewOnce, &m.EphemeralSecs, &m.Details, &m.Source, &m.Timestamp, &m.ReceivedAt, &m.RawProto)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return t, fmt.Errorf("trace message %s: %w", id, err)
	}
	if err == nil {
		if err = json.Unmarshal([]byte(mentions), &m.Mentions); err != nil {
			return t, fmt.Errorf("trace message %s: mentions: %w", id, err)
		}
	}
	err = s.scanRows(ctx, `SELECT chat_jid,target_id,kind,event_id,source_id,cause,sender_jid,source,new_text,value,ts,received_at FROM message_events WHERE target_id=?2 AND chat_jid IN (`+chatForms+`) ORDER BY ts,received_at,event_id`, []any{chatJID, id}, func(rows *sql.Rows) error {
		var e MessageEvent
		if err := rows.Scan(&e.ChatJID, &e.TargetID, &e.Kind, &e.EventID, &e.SourceID, &e.Cause, &e.SenderJID, &e.Source, &e.NewText, &e.Value, &e.Timestamp, &e.ReceivedAt); err != nil {
			return err
		}
		t.Events = append(t.Events, e)
		if e.Kind == "undecryptable" && t.Message.ID == "" {
			t.Gap = true
		}
		return nil
	})
	if err != nil {
		return t, err
	}
	if t.Media, err = s.ChatMedia(ctx, chatJID, id); err != nil {
		return t, err
	}
	if t.Message.ID == "" && len(t.Events) == 0 && t.Media == nil {
		return t, fmt.Errorf("trace %s in %s: %w", id, chatJID, sql.ErrNoRows)
	}
	return t, nil
}

// ChatMedia is the media row of message id in the chat under any of its JID forms, or nil.
func (s *Store) ChatMedia(ctx context.Context, chatJID, id string) (*Media, error) {
	var m Media
	err := s.read.QueryRowContext(ctx, `SELECT `+mediaColumns+` FROM media WHERE message_id=?2 AND chat_jid IN (`+chatForms+`) ORDER BY chat_jid LIMIT 1`, chatJID, id).Scan(m.fields()...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("media of %s: %w", id, err)
	}
	return &m, nil
}

// stringsByCanonical runs a two-column (canonical JID, value) query into a map.
func (s *Store) stringsByCanonical(ctx context.Context, query string) (map[string]string, error) {
	out := map[string]string{}
	err := s.scanRows(ctx, query, nil, func(rows *sql.Rows) error {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return err
		}
		out[k] = v
		return nil
	})
	return out, err
}

// scanRows runs query on the read pool and calls each for every row.
func (s *Store) scanRows(ctx context.Context, query string, args []any, each func(*sql.Rows) error) error {
	rows, err := s.read.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("browse query: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		if err = each(rows); err != nil {
			return fmt.Errorf("browse scan: %w", err)
		}
	}
	if err = rows.Err(); err != nil {
		return fmt.Errorf("browse rows: %w", err)
	}
	return nil
}
