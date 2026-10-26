package store

import (
	"context"
	"database/sql"
)

// statusReportQuery is the one query behind the chat half of `archiver status`: a read of chat_stats, the table the
// writer maintains (schema 4.4), so its cost is O(chats) and does not grow with messages, media or events. A chat with
// no message is not listed (the chat list adds those). The name is the chat_stats name: the chat's name, else the
// contact name, else the push name (nameExpr). The oldest history ts is NULL when the chat has no history message.
// Columns: canonical_jid, name, messages, oldest, newest, media failed, expired, pending, open gaps, oldest history ts.
const statusReportQuery = `SELECT canonical,name,messages,oldest_ts,last_ts,media_failed,media_expired,media_pending,gaps_open,oldest_history_ts
 FROM chat_stats WHERE messages>0 ORDER BY canonical`

// StatusReport is everything `archiver status` reads from the archive.
type StatusReport struct {
	Chats    []ChatStat
	Coverage Coverage
}

// StatusReport reads the per-chat statistics from chat_stats and the history coverage from the ledger.
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
const chatStatsQuery = statusReportQuery

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
