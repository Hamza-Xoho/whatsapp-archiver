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
	Snapshot     *ExportSnapshot
	// Legacy caller hints are intentionally ignored. Store owns canonical forms and duplicate state.
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
	// MediaNameCount lets renderers choose a collision-safe name without another query.
	MediaNameCount int `json:"-"`
}

// ExportTextRecord is one bounded-page record shaped for the WhatsApp-compatible text transcript.
type ExportTextRecord struct {
	Message        Message
	Media          *Media
	MediaNameCount int
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
	snapshot, err := s.exportSnapshot(ctx, p.CanonicalJID, p.Snapshot)
	if err != nil {
		return nil, err
	}
	limit := p.Limit
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	hasCursor := p.HasCursor || p.AfterTS != 0 || p.AfterID != ""
	const columns = `m.chat_jid,m.id,m.sender_jid,m.from_me,m.push_name,m.kind,m.text,m.caption,m.quoted_id,m.quoted_sender,m.mentions,m.forwarded,m.forward_score,m.view_once,m.ephemeral_secs,m.details,m.source,m.ts,m.received_at`
	forms, queryArgs := exportFormMarks(snapshot.forms)
	queryArgs = append(queryArgs, snapshot.maxMessageSeq, p.From, p.To, hasCursor, p.AfterTS, p.AfterID, limit)
	maxParam := len(snapshot.forms) + 1
	fromParam, toParam := len(snapshot.forms)+2, len(snapshot.forms)+3
	cursorParam, tsParam, idParam, limitParam := len(snapshot.forms)+4, len(snapshot.forms)+5, len(snapshot.forms)+6, len(snapshot.forms)+7
	parts := make([]string, 0, len(snapshot.forms))
	for formIndex := range snapshot.forms {
		where := []string{
			fmt.Sprintf("m.chat_jid=?%d", formIndex+1),
			fmt.Sprintf("m.seq<=?%d", maxParam),
			fmt.Sprintf("(?%d=0 OR m.ts>=?%d)", fromParam, fromParam),
			fmt.Sprintf("(?%d=0 OR m.ts<=?%d)", toParam, toParam),
			fmt.Sprintf("(?%d=0 OR m.ts>?%d OR (m.ts=?%d AND m.id>?%d))", cursorParam, tsParam, tsParam, idParam),
		}
		if snapshot.hasDuplicates {
			where = append(where, `NOT EXISTS (SELECT 1 FROM messages d WHERE d.id=m.id AND d.chat_jid IN (`+forms+`)
			 AND d.seq<=?`+fmt.Sprint(maxParam)+`
			 AND (?`+fmt.Sprint(fromParam)+`=0 OR d.ts>=?`+fmt.Sprint(fromParam)+`)
			 AND (?`+fmt.Sprint(toParam)+`=0 OR d.ts<=?`+fmt.Sprint(toParam)+`)
			 AND (d.received_at<m.received_at OR (d.received_at=m.received_at AND d.chat_jid<m.chat_jid)))`)
		}
		parts = append(parts, `SELECT * FROM (SELECT `+columns+` FROM messages m INDEXED BY messages_chat_ts WHERE `+
			strings.Join(where, " AND ")+` ORDER BY m.ts,m.id LIMIT ?`+fmt.Sprint(limitParam)+`)`)
	}
	q := `SELECT * FROM (` + strings.Join(parts, " UNION ALL ") + `) ORDER BY ts,id LIMIT ?` + fmt.Sprint(limitParam)
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
	forms, eventArgs := exportFormMarks(snapshot.forms)
	eventArgs = append(eventArgs, snapshot.maxEventSeq)
	eventMaxParam := len(eventArgs)
	for i, id := range ids {
		marks[i] = fmt.Sprintf("?%d", len(eventArgs)+1)
		eventArgs = append(eventArgs, id)
	}
	in := strings.Join(marks, ",")
	if !p.SkipEvents {
		err = s.scanRows(ctx, `SELECT chat_jid,target_id,kind,event_id,source_id,cause,sender_jid,source,new_text,value,ts,received_at FROM message_events WHERE chat_jid IN (`+forms+`) AND seq<=?`+fmt.Sprint(eventMaxParam)+` AND target_id IN (`+in+`) ORDER BY ts,received_at,event_id`, eventArgs, func(rows *sql.Rows) error {
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
	mediaMarks := make([]string, len(ids))
	mediaForms, mediaArgs := exportFormMarks(snapshot.forms)
	for i, id := range ids {
		mediaMarks[i] = fmt.Sprintf("?%d", len(mediaArgs)+1)
		mediaArgs = append(mediaArgs, id)
	}
	err = snapshot.scanRows(ctx, `SELECT chat_jid,message_id,sender_jid,source,type,mime,filename,path,sha256,state,reason,error_class,retry_direct_path,declared_size,updated_at,message_ts,enqueued_at,next_attempt_at,done_at,source_rank,duration_s,width,height,pages,http_status,attempts,from_me,thumb FROM media WHERE chat_jid IN (`+mediaForms+`) AND message_id IN (`+strings.Join(mediaMarks, ",")+`) ORDER BY chat_jid`, mediaArgs, func(rows *sql.Rows) error {
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
		if out[i].Media != nil {
			out[i].MediaNameCount = snapshot.mediaNameCount(out[i].Media.Filename)
		}
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

// ExportMediaPage lives in export_media.go; its bounded messages_chat_ts keyset and batched collision count avoid
// both archive-wide reranking and per-media naming queries.
