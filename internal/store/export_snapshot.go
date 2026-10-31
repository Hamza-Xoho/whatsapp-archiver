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
	canonical      string
	forms          []string
	maxMessageSeq  int64
	maxEventSeq    int64
	maxChatEvent   int64
	maxMediaRowID  int64
	mediaCount     int64
	mediaMinTS     int64
	mediaMaxTS     int64
	duplicateNames filenameBloom
	hasDuplicates  bool
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
	 COALESCE((SELECT MAX(rowid) FROM media WHERE chat_jid IN (` + marks + `)),0),
	 (SELECT COUNT(*) FROM media WHERE chat_jid IN (` + marks + `))`
	if err = tx.QueryRowContext(ctx, query, args...).Scan(&snapshot.maxMessageSeq, &snapshot.hasDuplicates,
		&snapshot.maxEventSeq, &snapshot.maxChatEvent, &snapshot.maxMediaRowID, &snapshot.mediaCount); err != nil {
		return nil, fmt.Errorf("snapshot export %s: %w", canonical, err)
	}
	if snapshot.mediaCount > 0 {
		mediaArgs := append(append([]any(nil), args...), snapshot.maxMediaRowID, snapshot.maxMessageSeq)
		mediaMax, messageMax := len(args)+1, len(args)+2
		mediaRange := `SELECT COALESCE(MIN((SELECT MIN(m.ts) FROM messages m WHERE m.id=x.message_id AND m.chat_jid IN (` + marks + `) AND m.seq<=?` + fmt.Sprint(messageMax) + `)),0),
		 COALESCE(MAX((SELECT MAX(m.ts) FROM messages m WHERE m.id=x.message_id AND m.chat_jid IN (` + marks + `) AND m.seq<=?` + fmt.Sprint(messageMax) + `)),0)
		 FROM media x WHERE x.chat_jid IN (` + marks + `) AND x.rowid<=?` + fmt.Sprint(mediaMax)
		if err = tx.QueryRowContext(ctx, mediaRange, mediaArgs...).Scan(&snapshot.mediaMinTS, &snapshot.mediaMaxTS); err != nil {
			return nil, fmt.Errorf("snapshot export media range %s: %w", canonical, err)
		}
	}
	if snapshot.mediaCount > 1 {
		duplicateArgs := append(append([]any(nil), args...), snapshot.maxMediaRowID)
		duplicateRows, queryErr := tx.QueryContext(ctx, `SELECT filename FROM media WHERE chat_jid IN (`+marks+
			`) AND rowid<=?`+fmt.Sprint(len(args)+1)+` GROUP BY filename HAVING COUNT(*)>1`, duplicateArgs...)
		if queryErr != nil {
			return nil, queryErr
		}
		for duplicateRows.Next() {
			var filename string
			if err = duplicateRows.Scan(&filename); err != nil {
				_ = duplicateRows.Close()
				return nil, err
			}
			snapshot.duplicateNames.add(filename)
		}
		if err = duplicateRows.Close(); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &snapshot, nil
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
	return 1
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
