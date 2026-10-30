package store

import (
	"context"
	"database/sql"
	"fmt"
)

// ExportEventParams selects a chronological page of message and chat events for one canonical chat.
type ExportEventParams struct {
	CanonicalJID string
	From, To     int64
	AfterTS      int64
	AfterKey     string
	HasCursor    bool
	Limit        int
}

// ExportEvent is exactly one event union arm. Raw protobuf bytes are never populated.
type ExportEvent struct {
	Message *MessageEvent `json:"message_event,omitempty"`
	Chat    *ChatEvent    `json:"chat_event,omitempty"`
	TS      int64         `json:"timestamp"`
	Key     string        `json:"-"`
}

func (e ExportEvent) ID() string {
	if e.Message != nil {
		return e.Message.EventID
	}
	if e.Chat != nil {
		return e.Chat.EventID
	}
	return ""
}
func (e ExportEvent) RawProto() []byte {
	if e.Message != nil {
		return e.Message.RawProto
	}
	if e.Chat != nil {
		return e.Chat.RawProto
	}
	return nil
}

// ExportEventPage includes events whose targets are absent or empty and chat-level events; canonical aliases and the
// date range are applied before the stable (timestamp,key) cursor.
func (s *Store) ExportEventPage(ctx context.Context, p ExportEventParams) ([]ExportEvent, error) {
	known, err := s.ChatKnown(ctx, p.CanonicalJID)
	if err != nil {
		return nil, err
	}
	if !known {
		return nil, sql.ErrNoRows
	}
	limit := p.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	const q = `WITH events AS (
 SELECT 'm:'||hex(chat_jid)||':'||hex(target_id)||':'||hex(kind)||':'||hex(event_id) AS k,chat_jid,target_id,kind,event_id,source_id,cause,sender_jid,source,new_text,value,ts,received_at,0 AS chat
 FROM message_events WHERE chat_jid IN (` + chatForms + `) AND (?2=0 OR ts>=?2) AND (?3=0 OR ts<=?3)
 UNION ALL
 SELECT 'c:'||hex(chat_jid)||':'||hex(kind)||':'||hex(event_id),chat_jid,'',kind,event_id,'','',sender_jid,'','',value,ts,received_at,1
 FROM chat_events WHERE chat_jid IN (` + chatForms + `) AND (?2=0 OR ts>=?2) AND (?3=0 OR ts<=?3)
)
SELECT k,chat_jid,target_id,kind,event_id,source_id,cause,sender_jid,source,new_text,value,ts,received_at,chat
FROM events WHERE (?4=0 OR ts>?5 OR (ts=?5 AND k>?6)) ORDER BY ts,k LIMIT ?7`
	var out []ExportEvent
	hasCursor := p.HasCursor || p.AfterTS != 0 || p.AfterKey != ""
	err = s.scanRows(ctx, q, []any{p.CanonicalJID, p.From, p.To, hasCursor, p.AfterTS, p.AfterKey, limit}, func(rows *sql.Rows) error {
		var x ExportEvent
		var chat bool
		var chatJID, target, kind, id, sourceID, cause, sender, source, newText, value string
		var received int64
		if err := rows.Scan(&x.Key, &chatJID, &target, &kind, &id, &sourceID, &cause, &sender, &source, &newText, &value, &x.TS, &received, &chat); err != nil {
			return err
		}
		if chat {
			x.Chat = &ChatEvent{ChatJID: chatJID, Kind: kind, EventID: id, SenderJID: sender, Value: value, Timestamp: x.TS, ReceivedAt: received}
		} else {
			x.Message = &MessageEvent{ChatJID: chatJID, TargetID: target, Kind: kind, EventID: id, SourceID: sourceID, Cause: cause, SenderJID: sender, Source: source, NewText: newText, Value: value, Timestamp: x.TS, ReceivedAt: received}
		}
		out = append(out, x)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("export events: %w", err)
	}
	return out, nil
}
