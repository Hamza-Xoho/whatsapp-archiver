package store

import (
	"context"
	"database/sql"
	"fmt"
)

// ExportTextRecord contains only the facts required by the WhatsApp-compatible text transcript.
type ExportTextRecord struct {
	Message Message
	Media   *Media
}

// ExportTextRows owns one cancellable, ordered transcript query. The open SQLite cursor keeps memory constant and
// avoids rerunning the canonical merge for every page; callers must close it.
type ExportTextRows struct{ rows *sql.Rows }

func (r *ExportTextRows) Next() bool   { return r.rows.Next() }
func (r *ExportTextRows) Err() error   { return r.rows.Err() }
func (r *ExportTextRows) Close() error { return r.rows.Close() }

// Scan reads the current row. Media is nil when the message has no archived attachment metadata.
func (r *ExportTextRows) Scan() (ExportTextRecord, error) {
	var out ExportTextRecord
	var filename sql.NullString
	var mediaTS sql.NullInt64
	m := &out.Message
	if err := r.rows.Scan(&m.ChatJID, &m.ID, &m.SenderJID, &m.Kind, &m.Text, &m.Caption, &m.Details,
		&m.Timestamp, &filename, &mediaTS); err != nil {
		return out, err
	}
	if filename.Valid {
		out.Media = &Media{ChatJID: m.ChatJID, MessageID: m.ID, Filename: filename.String, MessageTimestamp: mediaTS.Int64}
	}
	return out, nil
}

// OpenExportText reads the complete selected message stream without archive-sized materialisation. Canonical aliases,
// duplicate selection, date scope, and one deterministic media filename are all fixed by the same SQLite snapshot.
func (s *Store) OpenExportText(ctx context.Context, canonical string, snapshot *ExportSnapshot, from, to int64) (*ExportTextRows, error) {
	snapshot, err := s.exportSnapshot(ctx, canonical, snapshot)
	if err != nil {
		return nil, err
	}
	forms, args := exportFormMarks(snapshot.forms)
	args = append(args, snapshot.maxMessageSeq, from, to)
	maxParam, fromParam, toParam := len(snapshot.forms)+1, len(snapshot.forms)+2, len(snapshot.forms)+3
	const columns = `chat_jid,id,sender_jid,kind,text,caption,details,ts`
	messageSQL := `SELECT ` + columns + ` FROM messages WHERE chat_jid IN (` + forms + `) AND seq<=?` + fmt.Sprint(maxParam) + `
	 AND (?` + fmt.Sprint(fromParam) + `=0 OR ts>=?` + fmt.Sprint(fromParam) + `)
	 AND (?` + fmt.Sprint(toParam) + `=0 OR ts<=?` + fmt.Sprint(toParam) + `)`
	if snapshot.hasDuplicates {
		messageSQL = `SELECT ` + columns + ` FROM (
	 SELECT ` + columns + `,ROW_NUMBER() OVER (PARTITION BY id ORDER BY received_at,chat_jid) rk
	 FROM messages WHERE chat_jid IN (` + forms + `) AND seq<=?` + fmt.Sprint(maxParam) + `
	 AND (?` + fmt.Sprint(fromParam) + `=0 OR ts>=?` + fmt.Sprint(fromParam) + `)
	 AND (?` + fmt.Sprint(toParam) + `=0 OR ts<=?` + fmt.Sprint(toParam) + `)) WHERE rk=1`
	}
	query := `WITH selected AS (` + messageSQL + `), media_ranked AS (
	 SELECT message_id,filename,message_ts,ROW_NUMBER() OVER (PARTITION BY message_id ORDER BY chat_jid) rk
	 FROM media WHERE chat_jid IN (` + forms + `))
	 SELECT m.chat_jid,m.id,m.sender_jid,m.kind,m.text,m.caption,m.details,m.ts,x.filename,x.message_ts
	 FROM selected m LEFT JOIN media_ranked x ON x.message_id=m.id AND x.rk=1 ORDER BY m.ts,m.id`
	rows, err := s.read.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("open text export: %w", err)
	}
	return &ExportTextRows{rows: rows}, nil
}
