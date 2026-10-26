package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// PollOptions returns the option names of the poll message id in the chat (under any of its aliases, CON-7). found is
// false when the archive has no such poll.
func (s *Store) PollOptions(ctx context.Context, chat, id string) (options []string, found bool, err error) {
	var details string
	err = s.read.QueryRowContext(ctx, `SELECT m.details FROM messages m WHERE m.chat_jid=?1 AND m.id=?2 AND m.kind='poll'
 UNION ALL
 SELECT m.details FROM chat_canonical a JOIN chat_canonical b ON b.canonical=a.canonical JOIN messages m ON m.chat_jid=b.alias
 WHERE a.alias=?1 AND m.id=?2 AND m.kind='poll' LIMIT 1`, chat, id).Scan(&details)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("poll %s in %s: %w", id, chat, err)
	}
	var d PollDetails
	if err = json.Unmarshal([]byte(details), &d); err != nil {
		return nil, false, fmt.Errorf("details of poll %s: %w", id, err)
	}
	return d.Options, true, nil
}
