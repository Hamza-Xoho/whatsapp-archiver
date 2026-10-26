package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

const rebuildBatch = 2000

// RebuildDerived recreates chat_canonical, chat_stats, search_fts and search_tri from the raw tables in one
// transaction (schema 4.3): a failure leaves the previous derived state. Raw tables are not touched. The run is
// O(archive): it streams messages by seq in batches. Run it with the service stopped (OpenExclusive).
func (s *Store) RebuildDerived(ctx context.Context) error {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin rebuild: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, q := range []string{`DELETE FROM chat_canonical`, `DELETE FROM chat_stats`, `DROP TABLE IF EXISTS search_fts`, `DROP TABLE IF EXISTS search_tri`, createSearchFTS, createSearchTri} {
		if _, err = tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("reset derived tables: %w", classify(err))
		}
	}
	if err = rebuildCanonical(ctx, tx); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, statsSelect(false)); err != nil {
		return fmt.Errorf("rebuild chat_stats: %w", classify(err))
	}
	if err = rebuildIndex(ctx, tx); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit rebuild: %w", classify(err))
	}
	return nil
}

// jidSources are the columns that hold a JID the archive stores as a chat or a sender: every one has a chat_canonical
// row (R8), made at write time by ensureCanonical.
var jidSources = []string{
	"chats.jid", "participants.jid", "messages.chat_jid", "messages.sender_jid", "message_events.chat_jid", "message_events.sender_jid",
	"chat_events.chat_jid", "chat_events.sender_jid", "media.chat_jid", "chat_rules.jid", "chat_seen.jid", "id_mappings.lid", "id_mappings.pn",
}

// rebuildCanonical gives every stored JID its row (itself), then points the members of each id_mappings component at
// the component's smallest LID, compared as strings like rewriteComponent does (R8).
func rebuildCanonical(ctx context.Context, tx *sql.Tx) error {
	selects := make([]string, len(jidSources))
	for i, src := range jidSources {
		table, column, _ := strings.Cut(src, ".")
		selects[i] = `SELECT ` + column + ` AS j FROM ` + table
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO chat_canonical(alias,canonical) SELECT j,j FROM (`+strings.Join(selects, " UNION ")+`) WHERE j<>''`); err != nil {
		return fmt.Errorf("rebuild chat_canonical: %w", classify(err))
	}
	rows, err := tx.QueryContext(ctx, `SELECT lid,pn FROM id_mappings`)
	if err != nil {
		return fmt.Errorf("read id mappings: %w", err)
	}
	parent := map[string]string{}
	isLID := map[string]bool{}
	var find func(string) string
	find = func(x string) string {
		p, ok := parent[x]
		if !ok || p == x {
			parent[x] = x
			return x
		}
		root := find(p)
		parent[x] = root
		return root
	}
	for rows.Next() {
		var lid, pn string
		if err = rows.Scan(&lid, &pn); err != nil {
			_ = rows.Close()
			return err
		}
		isLID[lid] = true
		parent[find(lid)] = find(pn)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	smallest := map[string]string{} // component root -> smallest LID
	for jid := range parent {
		if r := find(jid); isLID[jid] && (smallest[r] == "" || jid < smallest[r]) {
			smallest[r] = jid
		}
	}
	for jid := range parent {
		if _, err = tx.ExecContext(ctx, `UPDATE chat_canonical SET canonical=? WHERE alias=?`, smallest[find(jid)], jid); err != nil {
			return fmt.Errorf("rebuild canonical of %s: %w", jid, classify(err))
		}
	}
	return nil
}

// rebuildIndex fills both FTS tables: messages by seq (under their seq) and edit events by seq (under -seq, R13), in
// batches so the raw protos and the whole archive are never in memory.
func rebuildIndex(ctx context.Context, tx *sql.Tx) error {
	after := int64(0)
	for {
		rows, err := tx.QueryContext(ctx, `SELECT m.seq,m.kind,m.text,m.caption,m.details,COALESCE(x.filename,'')
 FROM messages m LEFT JOIN media x ON x.chat_jid=m.chat_jid AND x.message_id=m.id WHERE m.seq>? ORDER BY m.seq LIMIT ?`, after, rebuildBatch)
		if err != nil {
			return fmt.Errorf("read messages to index: %w", err)
		}
		type entry struct {
			seq  int64
			body string
		}
		var batch []entry
		for rows.Next() {
			var seq int64
			var kind, text, caption, details, filename string
			if err = rows.Scan(&seq, &kind, &text, &caption, &details, &filename); err != nil {
				_ = rows.Close()
				return err
			}
			batch = append(batch, entry{seq, searchBody(kind, text, caption, details, filename)})
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return err
		}
		for _, e := range batch {
			if err = indexBody(ctx, tx, e.seq, e.body); err != nil {
				return err
			}
			after = e.seq
		}
		if len(batch) < rebuildBatch {
			break
		}
	}
	after = 0
	for {
		rows, err := tx.QueryContext(ctx, `SELECT seq,new_text FROM message_events WHERE kind='edit' AND new_text<>'' AND seq>? ORDER BY seq LIMIT ?`, after, rebuildBatch)
		if err != nil {
			return fmt.Errorf("read edits to index: %w", err)
		}
		n := 0
		var seqs []int64
		var texts []string
		for rows.Next() {
			var seq int64
			var text string
			if err = rows.Scan(&seq, &text); err != nil {
				_ = rows.Close()
				return err
			}
			seqs, texts = append(seqs, seq), append(texts, text)
			n++
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return err
		}
		for i, seq := range seqs {
			if err = indexBody(ctx, tx, -seq, strings.TrimSpace(Fold(texts[i]))); err != nil {
				return err
			}
			after = seq
		}
		if n < rebuildBatch {
			break
		}
	}
	return nil
}
