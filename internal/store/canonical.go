package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"
)

func nowUnix() int64 { return time.Now().Unix() }

// execer is what the in-transaction helpers need; *sql.Tx satisfies it.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// ensureCanonical gives every JID a chat_canonical row if it has none: itself, until a mapping says otherwise. A JID
// with a row (it was mapped) keeps it. Every JID the archive stores as a chat or sender must have a row, so reads can
// use inner joins (R8).
func ensureCanonical(ctx context.Context, tx execer, jids ...string) error {
	for _, jid := range jids {
		if jid == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO chat_canonical(alias,canonical) VALUES(?1,?1)`, jid); err != nil {
			return fmt.Errorf("canonical row of %s: %w", jid, classify(err))
		}
	}
	return nil
}

// InsertIDMapping stores a LID-phone mapping and reports whether it was new. A new mapping rewrites the
// chat_canonical rows of its component in the same transaction (schema 4.3 step 5); a known one changes nothing.
func (s *Store) InsertIDMapping(ctx context.Context, m IDMapping) (bool, error) {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin id mapping: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `INSERT INTO id_mappings(lid,pn,source,first_seen) VALUES(?,?,?,?) ON CONFLICT DO NOTHING`, m.LID, m.PN, m.Source, m.FirstSeen.Unix())
	if err != nil {
		return false, fmt.Errorf("insert id mapping: %w", classify(err))
	}
	isNew, _, err := inserted(result)
	if err != nil || !isNew {
		return false, err
	}
	moved, err := rewriteComponent(ctx, tx, m.LID, m.PN)
	if err != nil {
		return false, err
	}
	if err = mergeStats(ctx, tx, moved); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, fmt.Errorf("commit id mapping: %w", classify(err))
	}
	return true, nil
}

// merge is what a new mapping did to chat_canonical: the component's canonical JID and the canonical JIDs its members
// had before (a member without a row was its own canonical).
type merge struct {
	canonical string
	before    []string
}

// mergeStats brings chat_stats in line with a merge (schema 4.3 step 5). The stale rows of the canonicals that were
// absorbed are deleted and the merged chat is recomputed from the raw tables (O(rows of the chat), once per new
// mapping). A cheaper refresh of the name and the gaps alone would miss rows that the re-pointed aliases bring in
// without a stats row of their own (a stand-alone media row), so rebuild would differ from incremental.
func mergeStats(ctx context.Context, tx execer, mg merge) error {
	for _, old := range mg.before {
		if old == mg.canonical {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM chat_stats WHERE canonical=?`, old); err != nil {
			return fmt.Errorf("delete stale stats of %s: %w", old, classify(err))
		}
	}
	return recomputeStats(ctx, tx, mg.canonical)
}

// rewriteComponent walks the id_mappings component that contains lid and pn and points every member's chat_canonical
// row at the component's canonical JID: its smallest LID (R8, LID preferred). Members without a row get one.
func rewriteComponent(ctx context.Context, tx execer, lid, pn string) (merge, error) {
	members := []string{lid, pn}
	lids := map[string]bool{lid: true}
	for i := 0; i < len(members); i++ {
		found, err := neighbours(ctx, tx, members[i])
		if err != nil {
			return merge{}, err
		}
		for _, n := range found.lids {
			lids[n] = true
			if !slices.Contains(members, n) {
				members = append(members, n)
			}
		}
		for _, n := range found.pns {
			if !slices.Contains(members, n) {
				members = append(members, n)
			}
		}
		if len(found.pns) > 0 {
			lids[members[i]] = true // a JID with phone numbers is a LID
		}
	}
	canonical := ""
	for l := range lids {
		if canonical == "" || l < canonical {
			canonical = l
		}
	}
	mg := merge{canonical: canonical}
	for _, member := range members {
		var before string
		switch err := tx.QueryRowContext(ctx, `SELECT canonical FROM chat_canonical WHERE alias=?`, member).Scan(&before); {
		case errors.Is(err, sql.ErrNoRows):
			before = member
		case err != nil:
			return merge{}, fmt.Errorf("canonical row of %s: %w", member, err)
		}
		if !slices.Contains(mg.before, before) {
			mg.before = append(mg.before, before)
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO chat_canonical(alias,canonical) VALUES(?1,?2)
 ON CONFLICT(alias) DO UPDATE SET canonical=excluded.canonical WHERE chat_canonical.canonical<>excluded.canonical`, member, canonical)
		if err != nil {
			return merge{}, fmt.Errorf("canonical row of %s: %w", member, classify(err))
		}
	}
	return mg, nil
}

type neighbourSet struct{ lids, pns []string }

// neighbours are the LIDs mapped to jid (when it is a phone number) and the phone numbers mapped to it (when it is a
// LID): two primary-key and (pn, lid) index lookups.
func neighbours(ctx context.Context, tx execer, jid string) (neighbourSet, error) {
	var n neighbourSet
	for _, q := range []struct {
		query string
		into  *[]string
	}{{`SELECT lid FROM id_mappings WHERE pn=?`, &n.lids}, {`SELECT pn FROM id_mappings WHERE lid=?`, &n.pns}} {
		rows, err := tx.QueryContext(ctx, q.query, jid)
		if err != nil {
			return n, fmt.Errorf("mappings of %s: %w", jid, err)
		}
		for rows.Next() {
			var v string
			if err = rows.Scan(&v); err != nil {
				_ = rows.Close()
				return n, err
			}
			*q.into = append(*q.into, v)
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return n, err
		}
	}
	return n, nil
}
