package store

import (
	"context"
	"database/sql"
)

// statusReportQuery is the one query behind `archiver status`. It evaluates the window function that dedupes messages
// by (canonical chat, id) once, into the resolved CTE, and every message-derived column comes from that one result:
// the per-chat counts and range, and the oldest history message per chat. Media and gap aggregates are one pass each
// over their own table and joined per chat; a correlated subquery per chat would rescan them for every chat, so the
// cost would grow with chats times archive size. The canonical JID of a chat is its chat_canonical row; a chat
// without one is its own. Task (f) replaces this with a read of chat_stats.
// Columns: canonical_jid, name, messages, oldest, newest, media failed, expired, pending, open gaps, and the oldest
// history ts (NULL when the chat has no history message).
var statusReportQuery = `WITH resolved AS MATERIALIZED (
 SELECT canonical_jid,chat_jid,id,ts,source FROM (
  SELECT COALESCE(a.canonical,m.chat_jid) AS canonical_jid,m.chat_jid,m.id,m.ts,m.source,
   ROW_NUMBER() OVER (PARTITION BY COALESCE(a.canonical,m.chat_jid),m.id ORDER BY m.received_at,m.chat_jid) AS duplicate_rank
  FROM messages m LEFT JOIN chat_canonical a ON a.alias=m.chat_jid
 ) WHERE duplicate_rank=1
),
messages_by_chat AS (
 SELECT rs.canonical_jid,COALESCE(MAX(c.name),'') AS name,COUNT(*) AS messages,MIN(rs.ts) AS oldest,MAX(rs.ts) AS newest,
  MIN(CASE WHEN rs.source='history' THEN rs.ts END) AS oldest_history
 FROM resolved rs LEFT JOIN chats c ON c.jid=rs.chat_jid GROUP BY rs.canonical_jid
),
media_by_chat AS (
 SELECT COALESCE(a.canonical,x.chat_jid) AS canonical_jid,
  SUM(x.state='failed') AS failed,
  SUM(x.state IN ('expired','retry_requested')) AS expired,
  SUM(x.state IN ('pending','downloading')) AS pending
 FROM media x LEFT JOIN chat_canonical a ON a.alias=x.chat_jid
 WHERE x.state IN ('failed','expired','retry_requested','pending','downloading')
 GROUP BY 1
),
gaps_by_chat AS (
 SELECT canonical_jid,COUNT(*) AS gaps FROM open_gaps GROUP BY 1
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

// historyChunksQuery summarizes the history ledger (the imported rows only, R10: a chunk that is only spooled has not
// made the archive more complete): chunk count, progress of the newest T chunk (by received_at, then
// chunk_order; any type when there is none), time of the last chunk, and whether history is complete. Let T be FULL if any FULL chunk exists, else INITIAL_BOOTSTRAP; the
// newest sync of T starts at start, the latest received_at among the T chunks with the lowest chunk_order (a restarted
// sync sends its first chunk again). History is complete when some T chunk received at or after start has progress
// 100. The ledger has no sync id, so a sync is found this way; with no T chunk history is not complete. A sync whose
// first chunk arrives after a later chunk reads "in progress" until a newer chunk arrives. Limitation: if the newest
// sync's lowest-order chunk never reaches the ledger (acked as gone, or never importable), start stays at the old sync
// and an old 100 can complete the newer one.
const historyChunksQuery = `WITH chunks AS (SELECT *,rowid AS rid FROM history_sync_chunks WHERE stage='imported') SELECT COUNT(*),COALESCE((SELECT progress FROM chunks WHERE sync_type=COALESCE((SELECT sync_type FROM chunks WHERE sync_type='FULL' LIMIT 1),'INITIAL_BOOTSTRAP') ORDER BY received_at DESC,chunk_order DESC LIMIT 1),(SELECT progress FROM chunks ORDER BY received_at DESC,rid DESC LIMIT 1),0),COALESCE(MAX(received_at),0),COALESCE((SELECT MAX(h.progress)>=100 FROM chunks h,(SELECT t.sync_type,MAX(t.received_at) AS start FROM chunks t WHERE t.sync_type=COALESCE((SELECT sync_type FROM chunks WHERE sync_type='FULL' LIMIT 1),'INITIAL_BOOTSTRAP') AND t.chunk_order=(SELECT MIN(chunk_order) FROM chunks WHERE sync_type=t.sync_type)) s WHERE h.sync_type=s.sync_type AND h.received_at>=s.start),0) FROM chunks`

// HistoryCoverage is the coverage half of StatusReport.
func (s *Store) HistoryCoverage(ctx context.Context) (Coverage, error) {
	r, err := s.StatusReport(ctx)
	return r.Coverage, err
}
