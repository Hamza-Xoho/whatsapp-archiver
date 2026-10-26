package store

import (
	"context"
	"fmt"
)

// Derived-table maintenance (R3, schema 4.3). chat_stats, chat_canonical and the two FTS tables are maintained in Go by
// the writer, in the transaction of the raw change, and RebuildDerived recreates them from the raw tables. Every
// incremental update below has a counterpart in the rebuild SQL; the rebuild-equals-incremental test pins that they
// agree.

const canonicalOfAlias = `(SELECT canonical FROM chat_canonical WHERE alias=?1)`

// nameExpr is the display name of canonical ref: the chat's name, else the contact name, else the push name of the
// chat's own JIDs, else ”. typeExpr is its type. Both look at every alias of the canonical.
func nameExpr(ref string) string {
	pick := func(table, column string) string {
		return `NULLIF((SELECT MAX(t.` + column + `) FROM chat_canonical a JOIN ` + table + ` t ON t.jid=a.alias WHERE a.canonical=` + ref + `),'')`
	}
	return `COALESCE(` + pick("chats", "name") + `,` + pick("participants", "contact_name") + `,` + pick("participants", "push_name") + `,'')`
}

func typeExpr(ref string) string {
	return `COALESCE(NULLIF((SELECT MAX(t.type) FROM chat_canonical a JOIN chats t ON t.jid=a.alias WHERE a.canonical=` + ref + `),''),'')`
}

// statsSelect builds the INSERT ... SELECT that computes chat_stats rows from the raw tables: for every canonical (all
// of them, or only ?1 when filtered) that has a message, a chats row, a rule or a sighting (R20). A media row alone
// makes no row; its counts are read once the chat is seen. It is the definition of chat_stats; the incremental updates
// are shortcuts that must give the same rows.
func statsSelect(filtered bool) string {
	byAlias, byGaps := "", ""
	if filtered {
		byAlias, byGaps = " WHERE a.canonical=?1", " WHERE canonical_jid=?1"
	}
	return `INSERT INTO chat_stats(canonical,name,type,messages,last_ts,oldest_ts,oldest_history_ts,media_done,media_failed,media_expired,media_pending,media_bytes,gaps_open)
WITH msg AS (
  SELECT a.canonical AS c, COUNT(DISTINCT m.id) AS n, MAX(m.ts) AS last, MIN(m.ts) AS oldest,
   MIN(CASE WHEN m.source='history' THEN m.ts END) AS oldest_history
  FROM chat_canonical a JOIN messages m ON m.chat_jid=a.alias` + byAlias + ` GROUP BY a.canonical),
med AS (
  SELECT a.canonical AS c, SUM(x.state='done') AS done, SUM(x.state='failed') AS failed,
   SUM(x.state IN ('expired','retry_requested')) AS expired, SUM(x.state IN ('pending','downloading')) AS pending,
   SUM(CASE WHEN x.state='done' THEN x.declared_size ELSE 0 END) AS bytes
  FROM chat_canonical a JOIN media x ON x.chat_jid=a.alias` + byAlias + ` GROUP BY a.canonical),
gap AS (SELECT canonical_jid AS c, COUNT(*) AS n FROM open_gaps` + byGaps + ` GROUP BY canonical_jid),
keys(c) AS (
  SELECT c FROM msg
  UNION SELECT a.canonical FROM chat_canonical a JOIN chats t ON t.jid=a.alias` + byAlias + `
  UNION SELECT a.canonical FROM chat_canonical a JOIN chat_rules t ON t.jid=a.alias` + byAlias + `
  UNION SELECT a.canonical FROM chat_canonical a JOIN chat_seen t ON t.jid=a.alias` + byAlias + `)
SELECT k.c, ` + nameExpr("k.c") + `, ` + typeExpr("k.c") + `,
 COALESCE(msg.n,0), COALESCE(msg.last,0), COALESCE(msg.oldest,0), msg.oldest_history,
 COALESCE(med.done,0), COALESCE(med.failed,0), COALESCE(med.expired,0), COALESCE(med.pending,0), COALESCE(med.bytes,0),
 COALESCE(gap.n,0)
FROM keys k LEFT JOIN msg ON msg.c=k.c LEFT JOIN med ON med.c=k.c LEFT JOIN gap ON gap.c=k.c`
}

// recomputeStats rebuilds the chat_stats row of canonical from the raw tables, or removes it when nothing makes the
// chat seen (R20). It is the fallback of every incremental update that finds no row, and the merge of a mapping.
func recomputeStats(ctx context.Context, tx execer, canonical string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM chat_stats WHERE canonical=?1`, canonical); err != nil {
		return fmt.Errorf("clear stats of %s: %w", canonical, classify(err))
	}
	if _, err := tx.ExecContext(ctx, statsSelect(true), canonical); err != nil {
		return fmt.Errorf("compute stats of %s: %w", canonical, classify(err))
	}
	return nil
}

// canonicalIn is the canonical JID of jid inside a transaction.
func canonicalIn(ctx context.Context, tx execer, jid string) (string, error) {
	var c string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT canonical FROM chat_canonical WHERE alias=?1),?1)`, jid).Scan(&c); err != nil {
		return "", fmt.Errorf("canonical of %s: %w", jid, err)
	}
	return c, nil
}

// ensureStats gives the chat of jid a chat_stats row when it has none (R20); the row is computed from the raw tables.
// The rule, sighting or directory row that makes the chat seen must be written before the call.
func ensureStats(ctx context.Context, tx execer, jid string) error {
	var have int
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM chat_stats WHERE canonical=`+canonicalOfAlias+`)`, jid).Scan(&have)
	if err != nil {
		return fmt.Errorf("check stats of %s: %w", jid, err)
	}
	if have != 0 {
		return nil
	}
	c, err := canonicalIn(ctx, tx, jid)
	if err != nil {
		return err
	}
	return recomputeStats(ctx, tx, c)
}

// refreshIdentity recomputes the name and type of the chat of jid from its directory rows and reports whether the
// chat has a row. It never creates one.
func refreshIdentity(ctx context.Context, tx execer, jid string) (bool, error) {
	res, err := tx.ExecContext(ctx, `UPDATE chat_stats SET name=`+nameExpr("chat_stats.canonical")+`, type=`+typeExpr("chat_stats.canonical")+` WHERE canonical=`+canonicalOfAlias, jid)
	if err != nil {
		return false, fmt.Errorf("refresh name of %s: %w", jid, classify(err))
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// chatChanged follows a chats row that was inserted or changed: the chat is seen (R20) and its name or type moved.
func chatChanged(ctx context.Context, tx execer, jid string) error {
	ok, err := refreshIdentity(ctx, tx, jid)
	if err != nil || ok {
		return err
	}
	return ensureStats(ctx, tx, jid)
}

// openGapCount is the number of open gaps of the chat_stats row being updated.
const openGapCount = `(SELECT COUNT(*) FROM open_gaps WHERE canonical_jid=chat_stats.canonical)`

// gapWaitingFor is true when an undecryptable event of the chat_stats row's chat (under any alias) is waiting for the
// message id param: the partial index message_events_gaps and the UNIQUE index answer it with one probe per alias.
func gapWaitingFor(param string) string {
	return `EXISTS(SELECT 1 FROM chat_canonical a JOIN message_events e ON e.chat_jid=a.alias AND e.kind='undecryptable' AND e.target_id=` + param + `
 WHERE a.canonical=chat_stats.canonical)`
}

// gapsOf recomputes gaps_open of the chat of chat. With closing set it does so only when an undecryptable event
// of that chat (any alias) is waiting for the id ?2, i.e. when the new message or event may have closed a gap (R11).
func gapsOf(ctx context.Context, tx execer, chat, id string, closing bool) error {
	q := `UPDATE chat_stats SET gaps_open=` + openGapCount + ` WHERE canonical=` + canonicalOfAlias
	args := []any{chat}
	if closing {
		q += ` AND ` + gapWaitingFor("?2")
		args = append(args, id)
	}
	if _, err := tx.ExecContext(ctx, q, args...); err != nil {
		return fmt.Errorf("recompute gaps of %s: %w", chat, classify(err))
	}
	return nil
}

// messageAdded follows a new message (not a duplicate): count, times, and the gaps it closes. A chat without a row is
// computed from the raw tables, which already hold the message.
func (w batch) messageAdded(ctx context.Context, tx execer) error {
	m := w.msg
	res, err := tx.ExecContext(ctx, `UPDATE chat_stats SET messages=messages+1, last_ts=MAX(last_ts,?2),
 oldest_ts=CASE WHEN messages=0 THEN ?2 ELSE MIN(oldest_ts,?2) END,
 oldest_history_ts=CASE WHEN ?3 THEN CASE WHEN oldest_history_ts IS NULL THEN ?2 ELSE MIN(oldest_history_ts,?2) END ELSE oldest_history_ts END
 WHERE canonical=`+canonicalOfAlias, m.ChatJID, m.Timestamp, m.Source == "history")
	if err != nil {
		return fmt.Errorf("count message %s: %w", m.ID, classify(err))
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		c, err := canonicalIn(ctx, tx, m.ChatJID)
		if err != nil {
			return err
		}
		return recomputeStats(ctx, tx, c)
	}
	// The view behind gaps_open is only run when a gap is waiting for this id: the check is a cheap statement of its
	// own, because compiling the view costs more than the rest of the insert.
	var waiting int
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM chat_canonical a JOIN message_events e ON e.chat_jid=a.alias AND e.kind='undecryptable' AND e.target_id=?2
 WHERE a.canonical=`+canonicalOfAlias+`)`, m.ChatJID, m.ID).Scan(&waiting); err != nil {
		return fmt.Errorf("check gaps of %s: %w", m.ID, classify(err))
	}
	if waiting != 0 {
		return gapsOf(ctx, tx, m.ChatJID, "", false)
	}
	return nil
}

// eventAdded follows a new event: an undecryptable one opens a gap, any other event that is a message's own id may
// close one.
func (w batch) eventAdded(ctx context.Context, tx execer) error {
	e := w.event
	switch {
	case e.Kind == "undecryptable":
		return gapsOf(ctx, tx, e.ChatJID, "", false)
	case e.SourceID != "":
		return gapsOf(ctx, tx, e.ChatJID, e.SourceID, true)
	}
	return nil
}

// Media buckets of chat_stats: the state of a media row decides which count it is in.
const (
	bucketNone = iota
	bucketDone
	bucketFailed
	bucketExpired
	bucketPending
)

func mediaBucket(state string) int {
	switch state {
	case "done":
		return bucketDone
	case "failed":
		return bucketFailed
	case "expired", "retry_requested":
		return bucketExpired
	case "pending", "downloading":
		return bucketPending
	}
	return bucketNone // skipped, or no row
}

// mediaMoved follows n media rows of chat that moved from state from to state to ("" for no row), each with the
// declared size size: one count leaves a bucket and another grows, and the bytes follow the done rows (FR-25). It runs
// after the state is written; a chat without a row is computed from the raw tables.
func mediaMoved(ctx context.Context, tx execer, chat, from, to string, size int64, n int) error {
	var delta [5]int64
	delta[mediaBucket(from)] -= int64(n)
	delta[mediaBucket(to)] += int64(n)
	bytes := int64(0)
	if mediaBucket(to) == bucketDone {
		bytes += size * int64(n)
	}
	if mediaBucket(from) == bucketDone {
		bytes -= size * int64(n)
	}
	if delta[bucketDone] == 0 && delta[bucketFailed] == 0 && delta[bucketExpired] == 0 && delta[bucketPending] == 0 && bytes == 0 {
		return nil
	}
	res, err := tx.ExecContext(ctx, `UPDATE chat_stats SET media_done=media_done+?2, media_failed=media_failed+?3, media_expired=media_expired+?4,
 media_pending=media_pending+?5, media_bytes=media_bytes+?6 WHERE canonical=`+canonicalOfAlias,
		chat, delta[bucketDone], delta[bucketFailed], delta[bucketExpired], delta[bucketPending], bytes)
	if err != nil {
		return fmt.Errorf("move media count of %s: %w", chat, classify(err))
	}
	if rows, err := res.RowsAffected(); err != nil {
		return err
	} else if rows > 0 {
		return nil
	}
	c, err := canonicalIn(ctx, tx, chat)
	if err != nil {
		return err
	}
	return recomputeStats(ctx, tx, c)
}
