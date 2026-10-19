package store

import (
	"context"
	"database/sql"
)

// statusReportQuery is the one query behind `archiver status`. It evaluates the window function of messages_resolved
// once, into the resolved CTE, and every message-derived column comes from that one result: the per-chat counts and
// range, the oldest history message per chat, and the (canonical chat, id) pairs the open-gap check looks up. Media
// and gap aggregates are one pass each over their own table and joined per chat; a correlated subquery per chat would
// rescan them for every chat, so the cost would grow with chats times archive size.
// Columns: canonical_jid, name, messages, oldest, newest, media failed, expired, pending, open gaps, and the oldest
// history ts (NULL when the chat has no history message).
var statusReportQuery = `WITH resolved AS MATERIALIZED (
 SELECT canonical_jid,chat_jid,id,ts,source FROM messages_resolved
),
messages_by_chat AS (
 SELECT rs.canonical_jid,COALESCE(MAX(c.name),'') AS name,COUNT(*) AS messages,MIN(rs.ts) AS oldest,MAX(rs.ts) AS newest,
  MIN(CASE WHEN rs.source='history' THEN rs.ts END) AS oldest_history
 FROM resolved rs LEFT JOIN chats c ON c.jid=rs.chat_jid GROUP BY rs.canonical_jid
),
media_by_chat AS (
 SELECT COALESCE(a.canonical_jid,x.chat_jid) AS canonical_jid,
  SUM(x.state='failed') AS failed,
  SUM(x.state IN ('expired','retry_requested')) AS expired,
  SUM(x.state IN ('pending','downloading')) AS pending
 FROM media x LEFT JOIN chat_aliases a ON a.alias_jid=x.chat_jid
 WHERE x.state IN ('failed','expired','retry_requested','pending','downloading')
 GROUP BY 1
),
gaps_by_chat AS (
 SELECT COALESCE(a.canonical_jid,g.chat_jid) AS canonical_jid,COUNT(*) AS gaps
 FROM (SELECT e.* FROM message_events e LEFT JOIN chat_aliases ea ON ea.alias_jid=e.chat_jid
  WHERE e.kind='undecryptable' AND NOT EXISTS (SELECT 1 FROM resolved k WHERE k.canonical_jid=COALESCE(ea.canonical_jid,e.chat_jid) AND k.id=e.target_id)) g
 LEFT JOIN chat_aliases a ON a.alias_jid=g.chat_jid
 GROUP BY 1
)
SELECT b.canonical_jid,b.name,b.messages,b.oldest,b.newest,
 COALESCE(md.failed,0),COALESCE(md.expired,0),COALESCE(md.pending,0),COALESCE(g.gaps,0),b.oldest_history
 FROM messages_by_chat b
 LEFT JOIN media_by_chat md ON md.canonical_jid=b.canonical_jid
 LEFT JOIN gaps_by_chat g ON g.canonical_jid=b.canonical_jid
 ORDER BY b.canonical_jid`

// StatusReport is everything `archiver status` reads from the archive.
type StatusReport struct {
	Chats    []ChatStat
	Coverage Coverage
}

// StatusReport reads the per-chat statistics and the history coverage with one pass over the messages.
func (s *Store) StatusReport(ctx context.Context) (StatusReport, error) {
	r := StatusReport{Coverage: Coverage{OldestByChat: map[string]int64{}}}
	err := s.read.QueryRowContext(ctx, historyChunksQuery).Scan(&r.Coverage.Chunks, &r.Coverage.LastProgress, &r.Coverage.LastChunkTime, &r.Coverage.Complete)
	if err != nil {
		return r, err
	}
	rows, err := s.read.QueryContext(ctx, statusReportQuery)
	if err != nil {
		return r, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var v ChatStat
		var oldestHistory sql.NullInt64
		if err = rows.Scan(&v.CanonicalJID, &v.Name, &v.MessageCount, &v.Oldest, &v.Newest, &v.MediaFailed, &v.MediaExpired, &v.MediaPending, &v.OpenGaps, &oldestHistory); err != nil {
			return r, err
		}
		r.Chats = append(r.Chats, v)
		if oldestHistory.Valid {
			r.Coverage.OldestByChat[v.CanonicalJID] = oldestHistory.Int64
		}
	}
	return r, rows.Err()
}

// chatStatsQuery is the same query under its old name, kept for the plan test of ChatStats.
var chatStatsQuery = statusReportQuery

// ChatStats counts media as failed, expired (including a re-upload request waiting for the phone) and pending
// (including a download in progress); done and skipped rows are not counted.
func (s *Store) ChatStats(ctx context.Context) ([]ChatStat, error) {
	r, err := s.StatusReport(ctx)
	return r.Chats, err
}
