package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// ExportSnapshot is Store-owned canonicalization state for one export. Its fields are deliberately private so callers
// cannot inject aliases, suppress duplicate handling, or move the upper bound while a stream is in progress.
type ExportSnapshot struct {
	canonical     string
	forms         []string
	maxMessageSeq int64
	maxEventSeq   int64
	maxChatEvent  int64
	maxMediaRowID int64
	hasDuplicates bool
}

// NewExportSnapshot captures forms and every insert-only/artifact upper bound in one SQLite read snapshot.
func (s *Store) NewExportSnapshot(ctx context.Context, canonical string) (*ExportSnapshot, error) {
	tx, err := s.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var known bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM chat_stats WHERE canonical=?1)`, canonical).Scan(&known); err != nil {
		return nil, err
	}
	if !known {
		return nil, sql.ErrNoRows
	}
	rows, err := tx.QueryContext(ctx, `SELECT alias FROM chat_canonical WHERE canonical=? ORDER BY alias=? DESC,alias`, canonical, canonical)
	if err != nil {
		return nil, err
	}
	var forms []string
	for rows.Next() {
		var form string
		if err = rows.Scan(&form); err != nil {
			_ = rows.Close()
			return nil, err
		}
		forms = append(forms, form)
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	marks, args := exportFormMarks(forms)
	snapshot := ExportSnapshot{canonical: canonical, forms: forms}
	query := `SELECT
	 COALESCE((SELECT MAX(seq) FROM messages WHERE chat_jid IN (` + marks + `)),0),
	 (SELECT COUNT(*)<>COUNT(DISTINCT id) FROM messages WHERE chat_jid IN (` + marks + `)),
	 COALESCE((SELECT MAX(seq) FROM message_events WHERE chat_jid IN (` + marks + `)),0),
	 COALESCE((SELECT MAX(rowid) FROM chat_events WHERE chat_jid IN (` + marks + `)),0),
	 COALESCE((SELECT MAX(rowid) FROM media WHERE chat_jid IN (` + marks + `)),0)`
	if err = tx.QueryRowContext(ctx, query, args...).Scan(&snapshot.maxMessageSeq, &snapshot.hasDuplicates,
		&snapshot.maxEventSeq, &snapshot.maxChatEvent, &snapshot.maxMediaRowID); err != nil {
		return nil, fmt.Errorf("snapshot export %s: %w", canonical, err)
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &snapshot, nil
}

// Aliases returns a copy suitable for export metadata.
func (s *ExportSnapshot) Aliases() []string { return append([]string(nil), s.forms...) }

func (s *Store) exportSnapshot(ctx context.Context, canonical string, supplied *ExportSnapshot) (*ExportSnapshot, error) {
	if supplied == nil {
		return s.NewExportSnapshot(ctx, canonical)
	}
	if supplied.canonical != canonical {
		return nil, invalid("export snapshot is for %s, not %s", supplied.canonical, canonical)
	}
	return supplied, nil
}

// ExportSnapshotHasMessages applies the captured forms and message upper bound to the initial non-empty decision.
func (s *Store) ExportSnapshotHasMessages(ctx context.Context, snapshot *ExportSnapshot, from, to int64) (bool, error) {
	if snapshot == nil {
		return false, invalid("export snapshot is required")
	}
	forms, args := exportFormMarks(snapshot.forms)
	args = append(args, snapshot.maxMessageSeq, from, to)
	maxParam, fromParam, toParam := len(snapshot.forms)+1, len(snapshot.forms)+2, len(snapshot.forms)+3
	var found bool
	query := `SELECT EXISTS(SELECT 1 FROM messages WHERE chat_jid IN (` + forms + `) AND seq<=?` + fmt.Sprint(maxParam) +
		` AND (?` + fmt.Sprint(fromParam) + `=0 OR ts>=?` + fmt.Sprint(fromParam) + `)` +
		` AND (?` + fmt.Sprint(toParam) + `=0 OR ts<=?` + fmt.Sprint(toParam) + `) LIMIT 1)`
	if err := s.read.QueryRowContext(ctx, query, args...).Scan(&found); err != nil {
		return false, fmt.Errorf("check export snapshot range: %w", err)
	}
	return found, nil
}

func exportFormMarks(forms []string) (string, []any) {
	marks, args := make([]string, len(forms)), make([]any, len(forms))
	for i, form := range forms {
		marks[i], args[i] = fmt.Sprintf("?%d", i+1), form
	}
	return strings.Join(marks, ","), args
}
