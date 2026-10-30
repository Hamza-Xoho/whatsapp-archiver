package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ExportParams selects one ascending page of a canonical chat. From and To are inclusive unix seconds.
type ExportParams struct {
	CanonicalJID string
	From, To     int64
	AfterTS      int64
	AfterID      string
	HasCursor    bool
	// DuplicateStateKnown and HasDuplicates carry a Store-derived fact across
	// pages so a streaming export need not recount the full chat each time.
	DuplicateStateKnown, HasDuplicates bool
	Forms                              []string
	SkipEvents                         bool
	SkipMentions                       bool
	Limit                              int
}

// ExportRecord is one typed archived message with its events and optional media metadata. Raw protobufs are always
// cleared before the value leaves Store.
type ExportRecord struct {
	Message    Message        `json:"message"`
	Events     []MessageEvent `json:"events,omitempty"`
	Media      *Media         `json:"media,omitempty"`
	SenderName string         `json:"sender_name,omitempty"`
}

// ExportAliases returns every stored form of canonicalJID in stable order.
func (s *Store) ExportAliases(ctx context.Context, canonicalJID string) ([]string, error) {
	known, err := s.ChatKnown(ctx, canonicalJID)
	if err != nil {
		return nil, err
	}
	if !known {
		return nil, sql.ErrNoRows
	}
	var out []string
	err = s.scanRows(ctx, `SELECT alias FROM chat_canonical WHERE canonical=? ORDER BY alias=? DESC,alias`, []any{canonicalJID, canonicalJID}, func(rows *sql.Rows) error {
		var alias string
		if err := rows.Scan(&alias); err != nil {
			return err
		}
		out = append(out, alias)
		return nil
	})
	return out, err
}

// ExportHasMessages checks a date range without materializing or enriching an
// export page. It lets HTTP downloads commit their headers before page work.
func (s *Store) ExportHasMessages(ctx context.Context, canonicalJID string, from, to int64) (bool, error) {
	var found bool
	err := s.read.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM messages WHERE chat_jid IN (`+chatForms+`)
	 AND (?2=0 OR ts>=?2) AND (?3=0 OR ts<=?3) LIMIT 1)`, canonicalJID, from, to).Scan(&found)
	if err != nil {
		return false, fmt.Errorf("check export range: %w", err)
	}
	return found, nil
}

// ExportHasDuplicates reports whether alias rows need canonical ID ranking.
func (s *Store) ExportHasDuplicates(ctx context.Context, canonicalJID string) (bool, error) {
	return s.viewerHasDuplicates(ctx, canonicalJID)
}

// ExportPage reads a bounded, chronological page and gathers names, events and media with three bounded queries.
func (s *Store) ExportPage(ctx context.Context, p ExportParams) ([]ExportRecord, error) {
	known, err := s.ChatKnown(ctx, p.CanonicalJID)
	if err != nil {
		return nil, err
	}
	if !known {
		return nil, sql.ErrNoRows
	}
	limit := p.Limit
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	hasDuplicates := p.HasDuplicates
	if !p.DuplicateStateKnown {
		hasDuplicates, err = s.viewerHasDuplicates(ctx, p.CanonicalJID)
		if err != nil {
			return nil, err
		}
	}
	hasCursor := p.HasCursor || p.AfterTS != 0 || p.AfterID != ""
	const columns = `chat_jid,id,sender_jid,from_me,push_name,kind,text,caption,quoted_id,quoted_sender,mentions,forwarded,forward_score,view_once,ephemeral_secs,details,source,ts,received_at`
	var q string
	var queryArgs []any
	if hasDuplicates {
		q = `WITH ranked AS (
 SELECT chat_jid,id,sender_jid,from_me,push_name,kind,text,caption,quoted_id,quoted_sender,mentions,forwarded,forward_score,view_once,ephemeral_secs,details,source,ts,received_at,
 ROW_NUMBER() OVER (PARTITION BY id ORDER BY received_at,chat_jid) rk
 FROM messages WHERE chat_jid IN (` + chatForms + `)
 AND (?2=0 OR ts>=?2) AND (?3=0 OR ts<=?3)
)
SELECT chat_jid,id,sender_jid,from_me,push_name,kind,text,caption,quoted_id,quoted_sender,mentions,forwarded,forward_score,view_once,ephemeral_secs,details,source,ts,received_at
FROM ranked WHERE rk=1 AND (?4=0 OR ts>?5 OR (ts=?5 AND id>?6)) ORDER BY ts,id LIMIT ?7`
		queryArgs = []any{p.CanonicalJID, p.From, p.To, hasCursor, p.AfterTS, p.AfterID, limit}
	} else {
		forms := p.Forms
		if len(forms) == 0 {
			forms, err = s.ExportAliases(ctx, p.CanonicalJID)
			if err != nil {
				return nil, err
			}
		}
		parts := make([]string, 0, len(forms))
		for _, form := range forms {
			where := []string{"chat_jid=?"}
			queryArgs = append(queryArgs, form)
			if p.From > 0 {
				where = append(where, "ts>=?")
				queryArgs = append(queryArgs, p.From)
			}
			if p.To > 0 {
				where = append(where, "ts<=?")
				queryArgs = append(queryArgs, p.To)
			}
			if hasCursor {
				where = append(where, "(ts,id)>(?,?)")
				queryArgs = append(queryArgs, p.AfterTS, p.AfterID)
			}
			queryArgs = append(queryArgs, limit)
			parts = append(parts, `SELECT * FROM (SELECT `+columns+` FROM messages INDEXED BY messages_chat_ts WHERE `+strings.Join(where, " AND ")+` ORDER BY ts,id LIMIT ?)`)
		}
		queryArgs = append(queryArgs, limit)
		q = `SELECT * FROM (` + strings.Join(parts, " UNION ALL ") + `) ORDER BY ts,id LIMIT ?`
	}
	var out []ExportRecord
	err = s.scanRows(ctx, q, queryArgs, func(rows *sql.Rows) error {
		var r ExportRecord
		var mentions string
		m := &r.Message
		if err := rows.Scan(&m.ChatJID, &m.ID, &m.SenderJID, &m.FromMe, &m.PushName, &m.Kind, &m.Text, &m.Caption, &m.QuotedID, &m.QuotedSender, &mentions, &m.Forwarded, &m.ForwardScore, &m.ViewOnce, &m.EphemeralSecs, &m.Details, &m.Source, &m.Timestamp, &m.ReceivedAt); err != nil {
			return err
		}
		if !p.SkipMentions {
			if err := json.Unmarshal([]byte(mentions), &m.Mentions); err != nil {
				return err
			}
		}
		out = append(out, r)
		return nil
	})
	if err != nil || len(out) == 0 {
		return out, err
	}
	ids, senders := make([]string, len(out)), make([]string, 0, len(out))
	at := make(map[string]int, len(out))
	senderSeen := make(map[string]bool)
	for i := range out {
		ids[i], at[out[i].Message.ID] = out[i].Message.ID, i
		if j := out[i].Message.SenderJID; j != "" && !senderSeen[j] {
			senders = append(senders, j)
			senderSeen[j] = true
		}
	}
	names, err := s.senderNames(ctx, senders)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].SenderName = names[out[i].Message.SenderJID]
	}
	marks := make([]string, len(ids))
	args := []any{p.CanonicalJID}
	for i, id := range ids {
		marks[i] = fmt.Sprintf("?%d", i+2)
		args = append(args, id)
	}
	in := strings.Join(marks, ",")
	if !p.SkipEvents {
		err = s.scanRows(ctx, `SELECT chat_jid,target_id,kind,event_id,source_id,cause,sender_jid,source,new_text,value,ts,received_at FROM message_events WHERE chat_jid IN (`+chatForms+`) AND target_id IN (`+in+`) ORDER BY ts,received_at,event_id`, args, func(rows *sql.Rows) error {
			var e MessageEvent
			if err := rows.Scan(&e.ChatJID, &e.TargetID, &e.Kind, &e.EventID, &e.SourceID, &e.Cause, &e.SenderJID, &e.Source, &e.NewText, &e.Value, &e.Timestamp, &e.ReceivedAt); err != nil {
				return err
			}
			out[at[e.TargetID]].Events = append(out[at[e.TargetID]].Events, e)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	err = s.scanRows(ctx, `SELECT chat_jid,message_id,sender_jid,source,type,mime,filename,path,sha256,state,reason,error_class,retry_direct_path,declared_size,updated_at,message_ts,enqueued_at,next_attempt_at,done_at,source_rank,duration_s,width,height,pages,http_status,attempts,from_me,thumb FROM media WHERE chat_jid IN (`+chatForms+`) AND message_id IN (`+in+`) ORDER BY chat_jid`, args, func(rows *sql.Rows) error {
		var m Media
		if err := rows.Scan(&m.ChatJID, &m.MessageID, &m.SenderJID, &m.Source, &m.Type, &m.MIME, &m.Filename, &m.Path, &m.SHA256, &m.State, &m.Reason, &m.ErrorClass, &m.RetryDirectPath, &m.DeclaredSize, &m.UpdatedAt, &m.MessageTimestamp, &m.EnqueuedAt, &m.NextAttemptAt, &m.DoneAt, &m.SourceRank, &m.DurationS, &m.Width, &m.Height, &m.Pages, &m.HTTPStatus, &m.Attempts, &m.FromMe, &m.Thumb); err != nil {
			return err
		}
		i := at[m.MessageID]
		if out[i].Media == nil {
			m.RawProto = nil
			out[i].Media = &m
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Message.RawProto = nil
		for j := range out[i].Events {
			out[i].Events[j].RawProto = nil
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Message.Timestamp != out[j].Message.Timestamp {
			return out[i].Message.Timestamp < out[j].Message.Timestamp
		}
		return out[i].Message.ID < out[j].Message.ID
	})
	return out, nil
}

// ExportMediaPage reads a bounded media-only page for ZIP passes without
// rescanning and enriching every message in the chat.
func (s *Store) ExportMediaPage(ctx context.Context, p ExportParams) ([]ExportRecord, error) {
	limit := p.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	hasCursor := p.HasCursor || p.AfterTS != 0 || p.AfterID != ""
	query := `WITH ranked AS (
	 SELECT ` + mediaColumns + `,ROW_NUMBER() OVER (PARTITION BY message_id ORDER BY chat_jid) rk
	 FROM media WHERE chat_jid IN (` + chatForms + `)
	 AND (?2=0 OR message_ts>=?2) AND (?3=0 OR message_ts<=?3)
	)
	SELECT ` + mediaColumns + ` FROM ranked WHERE rk=1
	 AND (?4=0 OR message_ts>?5 OR (message_ts=?5 AND message_id>?6))
	 ORDER BY message_ts,message_id LIMIT ?7`
	var out []ExportRecord
	err := s.scanRows(ctx, query, []any{p.CanonicalJID, p.From, p.To, hasCursor, p.AfterTS, p.AfterID, limit}, func(rows *sql.Rows) error {
		var media Media
		if err := rows.Scan(media.fields()...); err != nil {
			return err
		}
		out = append(out, ExportRecord{Message: Message{ChatJID: p.CanonicalJID, ID: media.MessageID, Timestamp: media.MessageTimestamp}, Media: &media})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("export media page: %w", err)
	}
	return out, nil
}

// ExportMediaNameFacts supplies deterministic naming facts without retaining archive-sized state in the exporter.
func (s *Store) ExportMediaNameFacts(ctx context.Context, canonical, filename, candidate, messageID string, ts int64) (int, bool, error) {
	var ordinal int
	err := s.read.QueryRowContext(ctx, `SELECT COUNT(*) FROM media x JOIN chat_canonical c ON c.alias=x.chat_jid WHERE c.canonical=?1 AND x.filename=?2 AND (x.message_ts<?3 OR (x.message_ts=?3 AND x.message_id<=?4))`, canonical, filename, ts, messageID).Scan(&ordinal)
	if err != nil {
		return 0, false, err
	}
	var collision bool
	err = s.read.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM media x JOIN chat_canonical c ON c.alias=x.chat_jid WHERE c.canonical=?1 AND x.filename=?2 AND x.message_id<>?3)`, canonical, candidate, messageID).Scan(&collision)
	return ordinal, collision, err
}
