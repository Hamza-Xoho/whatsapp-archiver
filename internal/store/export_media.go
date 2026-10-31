package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// ExportMediaPage reads one bounded page of canonical messages that own media. Each alias contributes through its
// messages_chat_ts keyset and duplicate winners use bounded indexed anti-joins. Naming is deterministic without a
// filename-count scan: a multi-media snapshot hashes every safe basename, while a one-media snapshot keeps its name.
func (s *Store) ExportMediaPage(ctx context.Context, p ExportParams) ([]ExportRecord, error) {
	snapshot, err := s.exportSnapshot(ctx, p.CanonicalJID, p.Snapshot)
	if err != nil {
		return nil, err
	}
	limit := p.Limit
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	forms, args := exportFormMarks(snapshot.forms)
	args = append(args, snapshot.maxMessageSeq, snapshot.maxMediaRowID, p.From, p.To, p.HasCursor || p.AfterTS != 0 || p.AfterID != "", p.AfterTS, p.AfterID, limit)
	maxParam, mediaMaxParam := len(snapshot.forms)+1, len(snapshot.forms)+2
	fromParam, toParam := len(snapshot.forms)+3, len(snapshot.forms)+4
	cursorParam, tsParam, idParam, limitParam := len(snapshot.forms)+5, len(snapshot.forms)+6, len(snapshot.forms)+7, len(snapshot.forms)+8

	parts := make([]string, 0, len(snapshot.forms))
	for formIndex := range snapshot.forms {
		where := []string{
			fmt.Sprintf("m.chat_jid=?%d", formIndex+1),
			fmt.Sprintf("m.seq<=?%d", maxParam),
			fmt.Sprintf("(?%d=0 OR m.ts>=?%d)", fromParam, fromParam),
			fmt.Sprintf("(?%d=0 OR m.ts<=?%d)", toParam, toParam),
			fmt.Sprintf("(?%d=0 OR m.ts>?%d OR (m.ts=?%d AND m.id>?%d))", cursorParam, tsParam, tsParam, idParam),
			"EXISTS (SELECT 1 FROM media mx WHERE mx.message_id=m.id AND mx.chat_jid IN (" + forms + ") AND mx.rowid<=?" + fmt.Sprint(mediaMaxParam) + ")",
		}
		if snapshot.hasDuplicates {
			where = append(where, `NOT EXISTS (SELECT 1 FROM messages d WHERE d.id=m.id AND d.chat_jid IN (`+forms+`)
			 AND d.seq<=?`+fmt.Sprint(maxParam)+`
			 AND (?`+fmt.Sprint(fromParam)+`=0 OR d.ts>=?`+fmt.Sprint(fromParam)+`)
			 AND (?`+fmt.Sprint(toParam)+`=0 OR d.ts<=?`+fmt.Sprint(toParam)+`)
			 AND (d.received_at<m.received_at OR (d.received_at=m.received_at AND d.chat_jid<m.chat_jid)))`)
		}
		parts = append(parts, `SELECT * FROM (SELECT m.id,m.ts FROM messages m INDEXED BY messages_chat_ts
		 WHERE `+strings.Join(where, " AND ")+` ORDER BY m.ts,m.id LIMIT ?`+fmt.Sprint(limitParam)+`)`)
	}
	selected := `SELECT * FROM (` + strings.Join(parts, " UNION ALL ") + `) ORDER BY ts,id LIMIT ?` + fmt.Sprint(limitParam)
	mediaSelect := "x." + strings.ReplaceAll(mediaColumns, ",", ",x.")
	query := `WITH selected AS (` + selected + `), selected_media AS MATERIALIZED (
	 SELECT x.*,s.ts AS selected_ts FROM selected s JOIN media x ON x.message_id=s.id AND x.chat_jid IN (` + forms + `) AND x.rowid<=?` + fmt.Sprint(mediaMaxParam) + `
	 WHERE NOT EXISTS (SELECT 1 FROM media y WHERE y.message_id=x.message_id
	  AND y.chat_jid IN (` + forms + `) AND y.rowid<=?` + fmt.Sprint(mediaMaxParam) + ` AND y.chat_jid<x.chat_jid))
	 SELECT ` + mediaSelect + `,x.selected_ts FROM selected_media x
	 ORDER BY x.selected_ts,x.message_id`
	var out []ExportRecord
	err = s.scanRows(ctx, query, args, func(rows *sql.Rows) error {
		var media Media
		var messageTS int64
		if err := rows.Scan(append(media.fields(), &messageTS)...); err != nil {
			return err
		}
		nameCount := snapshot.mediaNameCount(media.Filename)
		out = append(out, ExportRecord{Message: Message{ChatJID: p.CanonicalJID, ID: media.MessageID, Timestamp: messageTS}, Media: &media, MediaNameCount: nameCount})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("export media page: %w", err)
	}
	return out, nil
}
