package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"fmt"
	"strings"
)

// ExportSnapshot is Store-owned canonicalization state for one export. Its fields are deliberately private so callers
// cannot inject aliases, suppress duplicate handling, or move the upper bound while a stream is in progress.
type ExportSnapshot struct {
	tx             *sql.Tx
	canonical      string
	forms          []string
	maxMessageSeq  int64
	maxEventSeq    int64
	maxChatEvent   int64
	duplicateNames filenameBloom
	hasDuplicates  bool
}

// NewExportSnapshot captures forms and every insert-only/artifact upper bound in one SQLite read snapshot.
func (s *Store) NewExportSnapshot(ctx context.Context, canonical string) (*ExportSnapshot, error) {
	snapshot, _, err := s.newExportSnapshot(ctx, canonical)
	return snapshot, err
}

// newExportSnapshot returns its exact startup-query count for the executable
// release-plan regression. The three queries are known-chat, aliases and indexed
// insert-only bounds; media is read lazily in bounded export pages.
func (s *Store) newExportSnapshot(ctx context.Context, canonical string) (*ExportSnapshot, int, error) {
	tx, err := s.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, 0, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = tx.Rollback()
		}
	}()
	var known bool
	queries := 1
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM chat_stats WHERE canonical=?1)`, canonical).Scan(&known); err != nil {
		return nil, queries, err
	}
	if !known {
		return nil, queries, sql.ErrNoRows
	}
	queries++
	rows, err := tx.QueryContext(ctx, `SELECT alias FROM chat_canonical WHERE canonical=? ORDER BY alias=? DESC,alias`, canonical, canonical)
	if err != nil {
		return nil, queries, err
	}
	var forms []string
	for rows.Next() {
		var form string
		if err = rows.Scan(&form); err != nil {
			_ = rows.Close()
			return nil, queries, err
		}
		forms = append(forms, form)
	}
	if err = rows.Close(); err != nil {
		return nil, queries, err
	}
	marks, args := exportFormMarks(forms)
	snapshot := ExportSnapshot{tx: tx, canonical: canonical, forms: forms}
	queries++
	if err = tx.QueryRowContext(ctx, exportSnapshotBoundsQuery(marks), args...).Scan(&snapshot.maxMessageSeq, &snapshot.hasDuplicates,
		&snapshot.maxEventSeq, &snapshot.maxChatEvent); err != nil {
		return nil, queries, fmt.Errorf("snapshot export %s: %w", canonical, err)
	}
	keep = true
	return &snapshot, queries, nil
}

func exportSnapshotBoundsQuery(marks string) string {
	return `SELECT
	 COALESCE((SELECT MAX(seq) FROM messages WHERE chat_jid IN (` + marks + `)),0),
	 (SELECT COUNT(*)<>COUNT(DISTINCT id) FROM messages WHERE chat_jid IN (` + marks + `)),
	 COALESCE((SELECT MAX(seq) FROM message_events WHERE chat_jid IN (` + marks + `)),0),
	 COALESCE((SELECT MAX(rowid) FROM chat_events WHERE chat_jid IN (` + marks + `)),0)`
}

// Close releases the SQLite read snapshot held for the whole artifact.
func (s *ExportSnapshot) Close() error {
	if s == nil || s.tx == nil {
		return nil
	}
	err := s.tx.Rollback()
	s.tx = nil
	if err == sql.ErrTxDone {
		return nil
	}
	return err
}

func (s *ExportSnapshot) scanRows(ctx context.Context, query string, args []any, each func(*sql.Rows) error) error {
	rows, err := s.tx.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("export snapshot query: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		if err = each(rows); err != nil {
			return fmt.Errorf("export snapshot scan: %w", err)
		}
	}
	return rows.Err()
}

// Aliases returns a copy suitable for export metadata.
func (s *ExportSnapshot) Aliases() []string { return append([]string(nil), s.forms...) }

const filenameBloomBytes = 8 << 10

type filenameBloom [filenameBloomBytes]byte

func (b *filenameBloom) add(name string) {
	for _, bit := range filenameBloomBits(name) {
		b[bit/8] |= 1 << (bit % 8)
	}
}

func (b *filenameBloom) contains(name string) bool {
	for _, bit := range filenameBloomBits(name) {
		if b[bit/8]&(1<<(bit%8)) == 0 {
			return false
		}
	}
	return true
}

func filenameBloomBits(name string) [3]uint64 {
	sum := sha256.Sum256([]byte(name))
	const bits = filenameBloomBytes * 8
	return [3]uint64{binary.LittleEndian.Uint64(sum[0:8]) % bits, binary.LittleEndian.Uint64(sum[8:16]) % bits,
		binary.LittleEndian.Uint64(sum[16:24]) % bits}
}

func (s *ExportSnapshot) mediaNameCount(filename string) int {
	if s.duplicateNames.contains(filename) {
		return 2
	}
	s.duplicateNames.add(filename)
	return 1
}

// ResetMediaNames starts a deterministic bounded naming traversal. Transcript
// and ZIP media traversals use the same chronological order, so their familiar
// first name and hashed later duplicates agree without an archive-wide scan.
func (s *ExportSnapshot) ResetMediaNames() {
	if s != nil {
		s.duplicateNames = filenameBloom{}
	}
}

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
