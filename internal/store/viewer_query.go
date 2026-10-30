package store

import (
	"context"
	"fmt"
)

// viewerHasDuplicates compares the writer-maintained distinct count with the
// raw rows under every alias. Chats with no cross-form duplicate can skip the
// whole-history ranking step without changing their canonical result.
func (s *Store) viewerHasDuplicates(ctx context.Context, chat string) (bool, error) {
	var duplicates bool
	err := s.read.QueryRowContext(ctx, `SELECT COALESCE((SELECT messages FROM chat_stats WHERE canonical=COALESCE(
	 (SELECT canonical FROM chat_canonical WHERE alias=?1),?1)),0)<(SELECT COUNT(*) FROM messages WHERE chat_jid IN (`+chatForms+`))`, chat).Scan(&duplicates)
	if err != nil {
		return false, fmt.Errorf("viewer duplicate check: %w", err)
	}
	return duplicates, nil
}
