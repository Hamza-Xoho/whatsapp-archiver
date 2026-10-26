package store

import (
	"context"
	"fmt"
)

// ContactName is the address-book name of a JID, as whatsmeow's contact store reports it. The JID is stored as
// received (CON-7).
type ContactName struct{ JID, Name string }

// SetContactNames writes participants.contact_name for each JID in one transaction and reports how many names
// changed. A name that equals the stored one writes nothing, and an empty name is skipped (it never erases a stored
// name, like push_name), so a repeated call is free. A JID that is not a participant yet gets a row, so the name is
// there when the chat appears. Every changed name refreshes chat_stats.name of the chat whose canonical includes the
// JID (the name expression reads contact_name, schema 4.3 step 2); no raw table is touched. A name without a JID
// refuses the whole call.
func (s *Store) SetContactNames(ctx context.Context, names []ContactName) (int, error) {
	for _, n := range names {
		if n.JID == "" {
			return 0, invalid("contact name %q without a JID", n.Name)
		}
	}
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin contact names: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	at := nowUnix()
	changed := 0
	for _, n := range names {
		if n.Name == "" {
			continue
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO participants(jid,contact_name,updated_at) VALUES(?1,?2,?3)
 ON CONFLICT(jid) DO UPDATE SET contact_name=excluded.contact_name,updated_at=excluded.updated_at
 WHERE excluded.contact_name<>participants.contact_name`, n.JID, n.Name, at)
		if err != nil {
			return 0, fmt.Errorf("write contact name of %s: %w", n.JID, classify(err))
		}
		rows, err := res.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("write contact name of %s: %w", n.JID, err)
		}
		if rows == 0 {
			continue
		}
		changed++
		if _, err = refreshIdentity(ctx, tx, n.JID); err != nil {
			return 0, err
		}
	}
	if err = tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit contact names: %w", classify(err))
	}
	return changed, nil
}
