package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// ExportTextStream owns one message-only SQLite cursor. The query merges indexed alias streams and uses an indexed
// anti-join for duplicate winners; it never globally ranks messages or joins archive-wide media/name facts.
type ExportTextStream struct {
	store    *Store
	snapshot *ExportSnapshot
	rows     *sql.Rows
	limit    int
}

func (s *Store) OpenExportPageStream(ctx context.Context, p ExportParams) (*ExportTextStream, error) {
	snapshot, err := s.exportSnapshot(ctx, p.CanonicalJID, p.Snapshot)
	if err != nil {
		return nil, err
	}
	limit := p.Limit
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	forms, args := exportFormMarks(snapshot.forms)
	args = append(args, snapshot.maxMessageSeq, p.From, p.To)
	maxParam, fromParam, toParam := len(snapshot.forms)+1, len(snapshot.forms)+2, len(snapshot.forms)+3
	parts := make([]string, 0, len(snapshot.forms))
	for formIndex := range snapshot.forms {
		where := []string{fmt.Sprintf("m.chat_jid=?%d", formIndex+1), fmt.Sprintf("m.seq<=?%d", maxParam),
			fmt.Sprintf("(?%d=0 OR m.ts>=?%d)", fromParam, fromParam), fmt.Sprintf("(?%d=0 OR m.ts<=?%d)", toParam, toParam)}
		if snapshot.hasDuplicates {
			where = append(where, `NOT EXISTS (SELECT 1 FROM messages d WHERE d.id=m.id AND d.chat_jid IN (`+forms+`)
			 AND d.seq<=?`+fmt.Sprint(maxParam)+`
			 AND (?`+fmt.Sprint(fromParam)+`=0 OR d.ts>=?`+fmt.Sprint(fromParam)+`)
			 AND (?`+fmt.Sprint(toParam)+`=0 OR d.ts<=?`+fmt.Sprint(toParam)+`)
			 AND (d.received_at<m.received_at OR (d.received_at=m.received_at AND d.chat_jid<m.chat_jid)))`)
		}
		parts = append(parts, `SELECT m.chat_jid,m.id,m.sender_jid,m.push_name,m.kind,m.text,m.caption,m.details,m.ts
		 FROM messages m INDEXED BY messages_chat_ts WHERE `+strings.Join(where, " AND "))
	}
	rows, err := s.read.QueryContext(ctx, `SELECT * FROM (`+strings.Join(parts, " UNION ALL ")+`) ORDER BY ts,id`, args...)
	if err != nil {
		return nil, fmt.Errorf("open text export stream: %w", err)
	}
	return &ExportTextStream{store: s, snapshot: snapshot, rows: rows, limit: limit}, nil
}

func (s *ExportTextStream) Close() error { return s.rows.Close() }

// NextPage retains at most limit messages and batches sender/media enrichment for just those messages.
func (s *ExportTextStream) NextPage(ctx context.Context) ([]ExportTextRecord, error) {
	out := make([]ExportTextRecord, 0, s.limit)
	for len(out) < s.limit && s.rows.Next() {
		var r ExportTextRecord
		m := &r.Message
		if err := s.rows.Scan(&m.ChatJID, &m.ID, &m.SenderJID, &m.PushName, &m.Kind, &m.Text, &m.Caption, &m.Details, &m.Timestamp); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := s.rows.Err(); err != nil || len(out) == 0 {
		return out, err
	}
	ids, senders := make([]string, len(out)), make([]string, 0, len(out))
	at, senderSeen := make(map[string]int, len(out)), make(map[string]bool)
	for i := range out {
		ids[i], at[out[i].Message.ID] = out[i].Message.ID, i
		if jid := out[i].Message.SenderJID; out[i].Message.PushName == "" && jid != "" && !senderSeen[jid] {
			senders, senderSeen[jid] = append(senders, jid), true
		}
	}
	if len(senders) > 0 {
		names, err := s.store.senderNames(ctx, senders)
		if err != nil {
			return nil, err
		}
		for i := range out {
			if out[i].Message.PushName == "" {
				out[i].Message.PushName = names[out[i].Message.SenderJID]
			}
		}
	}
	forms, args := exportFormMarks(s.snapshot.forms)
	marks := make([]string, len(ids))
	for i, id := range ids {
		marks[i] = fmt.Sprintf("?%d", len(args)+1)
		args = append(args, id)
	}
	err := s.snapshot.scanRows(ctx, `SELECT chat_jid,message_id,filename,message_ts FROM media WHERE chat_jid IN (`+forms+
		`) AND message_id IN (`+strings.Join(marks, ",")+`) ORDER BY chat_jid`, args, func(rows *sql.Rows) error {
		var media Media
		if err := rows.Scan(&media.ChatJID, &media.MessageID, &media.Filename, &media.MessageTimestamp); err != nil {
			return err
		}
		i := at[media.MessageID]
		if out[i].Media == nil {
			out[i].Media = &media
		}
		return nil
	})
	if err != nil {
		return out, err
	}
	// The media lookup is keyed for bounded enrichment rather than presentation
	// order. Apply collision state in the chronological message-page order so
	// transcript and ZIP traversals choose the same familiar/hash names.
	for i := range out {
		if out[i].Media != nil {
			out[i].MediaNameCount = s.snapshot.mediaNameCount(out[i].Media.Filename)
		}
	}
	return out, nil
}
