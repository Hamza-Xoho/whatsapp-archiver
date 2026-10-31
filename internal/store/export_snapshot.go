package store

import (
	"context"
	"fmt"
	"strings"
)

// ExportSnapshot is Store-owned canonicalization state for one export. Its fields are deliberately private so callers
// cannot inject aliases, suppress duplicate handling, or move the upper bound while a stream is in progress.
type ExportSnapshot struct {
	canonical     string
	forms         []string
	maxMessageSeq int64
	hasDuplicates bool
}

// NewExportSnapshot captures fixed canonical forms, a message upper bound, and duplicate handling for one stream.
func (s *Store) NewExportSnapshot(ctx context.Context, canonical string) (*ExportSnapshot, error) {
	forms, err := s.ExportAliases(ctx, canonical)
	if err != nil {
		return nil, err
	}
	marks, args := exportFormMarks(forms)
	var snapshot ExportSnapshot
	snapshot.canonical, snapshot.forms = canonical, forms
	query := `SELECT COALESCE(MAX(seq),0),COUNT(*)<>COUNT(DISTINCT id) FROM messages WHERE chat_jid IN (` + marks + `)`
	if err = s.read.QueryRowContext(ctx, query, args...).Scan(&snapshot.maxMessageSeq, &snapshot.hasDuplicates); err != nil {
		return nil, fmt.Errorf("snapshot export %s: %w", canonical, err)
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

func exportFormMarks(forms []string) (string, []any) {
	marks, args := make([]string, len(forms)), make([]any, len(forms))
	for i, form := range forms {
		marks[i], args[i] = fmt.Sprintf("?%d", i+1), form
	}
	return strings.Join(marks, ","), args
}
