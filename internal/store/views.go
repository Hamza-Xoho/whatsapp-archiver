package store

import (
	"context"
	"database/sql"
	"slices"
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
	coverage, err := s.historyCoverage(ctx)
	if err != nil {
		return StatusReport{}, err
	}
	r := StatusReport{Coverage: coverage}
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

// historyChunksQuery reads only the insert-only ledger. historyCoverage folds its two stages per sync type in Go: the
// ledger stays small, while received progress becomes visible before asynchronous import finishes.
const historyChunksQuery = `SELECT id,stage,sync_type,chunk_order,progress,oldest_ts,newest_ts,received_at
 FROM history_sync_chunks ORDER BY received_at,rowid`

type historyStatusRow struct {
	id, stage, syncType        string
	order, progress            int
	oldest, newest, receivedAt int64
}

func (s *Store) historyCoverage(ctx context.Context) (Coverage, error) {
	c := Coverage{OldestByChat: map[string]int64{}}
	rows, err := s.read.QueryContext(ctx, historyChunksQuery)
	if err != nil {
		return c, err
	}
	defer func() { _ = rows.Close() }()
	byType := map[string][]historyStatusRow{}
	for rows.Next() {
		var r historyStatusRow
		if err = rows.Scan(&r.id, &r.stage, &r.syncType, &r.order, &r.progress, &r.oldest, &r.newest, &r.receivedAt); err != nil {
			return c, err
		}
		if r.syncType == "" {
			r.syncType = "UNKNOWN"
		}
		byType[r.syncType] = append(byType[r.syncType], r)
		if r.stage == StageImported {
			c.Chunks++
		}
	}
	if err = rows.Err(); err != nil {
		return c, err
	}
	types := make([]string, 0, len(byType))
	for typ := range byType {
		types = append(types, typ)
	}
	slices.Sort(types)
	for _, typ := range types {
		c.Syncs = append(c.Syncs, summarizeSync(typ, byType[typ]))
	}
	chosen := chooseCoverage(c.Syncs)
	if chosen != nil {
		c.LastProgress, c.LastChunkTime, c.Complete = chosen.LastProgress, chosen.LastChunkTime, chosen.Complete
	}
	return c, nil
}

func summarizeSync(typ string, rows []historyStatusRow) SyncCoverage {
	out := SyncCoverage{SyncType: typ}
	minOrder := 0
	lastOrder := 0
	haveOrder := false
	for _, r := range rows {
		switch r.stage {
		case StageReceived:
			out.ReceivedChunks++
		case StageImported:
			out.ImportedChunks++
		}
		if r.receivedAt > out.LastChunkTime || r.receivedAt == out.LastChunkTime && r.order > lastOrder {
			out.LastChunkTime, out.LastProgress = r.receivedAt, r.progress
			lastOrder = r.order
		}
		if !haveOrder || r.order < minOrder {
			minOrder, haveOrder = r.order, true
		}
		if r.oldest > 0 && (out.Oldest == 0 || r.oldest < out.Oldest) {
			out.Oldest = r.oldest
		}
		if r.newest > out.Newest {
			out.Newest = r.newest
		}
	}
	var start int64
	for _, r := range rows {
		if r.order == minOrder && r.receivedAt > start {
			start = r.receivedAt
		}
	}
	for _, r := range rows {
		if r.stage == StageImported && r.receivedAt >= start && r.progress >= 100 {
			out.Complete = true
		}
	}
	return out
}

func chooseCoverage(syncs []SyncCoverage) *SyncCoverage {
	for _, want := range []string{"FULL", "INITIAL_BOOTSTRAP"} {
		for i := range syncs {
			if syncs[i].SyncType == want {
				return &syncs[i]
			}
		}
	}
	var newest *SyncCoverage
	for i := range syncs {
		if newest == nil || syncs[i].LastChunkTime > newest.LastChunkTime {
			newest = &syncs[i]
		}
	}
	return newest
}

// HistoryCoverage is the coverage half of StatusReport.
func (s *Store) HistoryCoverage(ctx context.Context) (Coverage, error) {
	r, err := s.StatusReport(ctx)
	return r.Coverage, err
}
