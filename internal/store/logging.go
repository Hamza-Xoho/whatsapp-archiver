package store

import (
	"context"
	"database/sql"
	"fmt"
)

// LoggingCount is one grouped operational count. Name is a closed state/class
// value from the archive, never user content.
type LoggingCount struct {
	Name  string
	Count int
}

// LoggingOutages returns a bounded newest-first outage timeline. The existing
// ConnectionPeriods method remains chronological for ledger consumers.
func (s *Store) LoggingOutages(ctx context.Context, limit int) ([]ConnectionPeriod, error) {
	limit = boundedLoggingLimit(limit, 200)
	rows, err := s.read.QueryContext(ctx, `SELECT id,state,started_at,COALESCE(ended_at,0),cause_class,error_count,last_error,offline_preview_msgs,offline_completed_count
 FROM connection_periods ORDER BY started_at DESC,id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("read logging outages: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []ConnectionPeriod
	for rows.Next() {
		var p ConnectionPeriod
		var preview, completed sql.NullInt64
		if err = rows.Scan(&p.ID, &p.State, &p.StartedAt, &p.EndedAt, &p.CauseClass, &p.ErrorCount, &p.LastError, &preview, &completed); err != nil {
			return nil, fmt.Errorf("scan logging outage: %w", err)
		}
		if preview.Valid {
			p.OfflinePreviewMsgs = &preview.Int64
		}
		if completed.Valid {
			p.OfflineCompletedCount = &completed.Int64
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// LoggingGap is a bounded trace link for one open gap. It contains identifiers
// only; message text and raw protobufs are never selected.
type LoggingGap struct {
	CanonicalJID, ChatJID, TargetID, Cause string
	ReceivedAt                             int64
	PostOutage                             bool
}

func (s *Store) LoggingGaps(ctx context.Context, limit int) ([]LoggingGap, error) {
	limit = boundedLoggingLimit(limit, 200)
	rows, err := s.read.QueryContext(ctx, `SELECT canonical_jid,chat_jid,target_id,cause,received_at,post_outage
 FROM open_gaps ORDER BY received_at DESC,seq DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("read logging gaps: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []LoggingGap
	for rows.Next() {
		var g LoggingGap
		if err = rows.Scan(&g.CanonicalJID, &g.ChatJID, &g.TargetID, &g.Cause, &g.ReceivedAt, &g.PostOutage); err != nil {
			return nil, fmt.Errorf("scan logging gap: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

type LoggingMediaReason struct {
	Reason string
	Count  int
}

type LoggingMediaReport struct {
	States, ErrorClasses []LoggingCount
	Reasons              []LoggingMediaReason
}

// LoggingMedia groups narrow indexed/scalar columns and never reads paths,
// media URLs, thumbnails, hashes, retry paths or protobufs.
func (s *Store) LoggingMedia(ctx context.Context, reasonLimit int) (LoggingMediaReport, error) {
	var out LoggingMediaReport
	var err error
	out.States, err = s.loggingCounts(ctx, `SELECT state,COUNT(*) FROM media GROUP BY state ORDER BY state`)
	if err != nil {
		return out, err
	}
	out.ErrorClasses, err = s.loggingCounts(ctx, `SELECT error_class,COUNT(*) FROM media WHERE error_class<>'' GROUP BY error_class ORDER BY error_class`)
	if err != nil {
		return out, err
	}
	reasonLimit = boundedLoggingLimit(reasonLimit, 50)
	rows, err := s.read.QueryContext(ctx, `SELECT reason,COUNT(*) FROM media WHERE reason<>'' GROUP BY reason ORDER BY COUNT(*) DESC,reason LIMIT ?`, reasonLimit)
	if err != nil {
		return out, fmt.Errorf("read logging media reasons: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var r LoggingMediaReason
		if err = rows.Scan(&r.Reason, &r.Count); err != nil {
			return out, err
		}
		out.Reasons = append(out.Reasons, r)
	}
	return out, rows.Err()
}

func (s *Store) loggingCounts(ctx context.Context, query string) ([]LoggingCount, error) {
	rows, err := s.read.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []LoggingCount
	for rows.Next() {
		var c LoggingCount
		if err = rows.Scan(&c.Name, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

type LoggingHistoryChat struct {
	CanonicalJID, Name string
	OldestHistory      int64
}

type LoggingHistoryReport struct {
	Syncs                     []SyncCoverage
	SpoolBacklog, SpoolErrors int
	Chats                     []LoggingHistoryChat
}

func (s *Store) LoggingHistory(ctx context.Context, chatLimit int) (LoggingHistoryReport, error) {
	coverage, err := s.historyCoverage(ctx)
	if err != nil {
		return LoggingHistoryReport{}, err
	}
	out := LoggingHistoryReport{Syncs: coverage.Syncs}
	if err = s.read.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(error<>''),0) FROM history_spool WHERE imported_at IS NULL`).Scan(&out.SpoolBacklog, &out.SpoolErrors); err != nil {
		return out, fmt.Errorf("read logging spool counts: %w", err)
	}
	chatLimit = boundedLoggingLimit(chatLimit, 250)
	rows, err := s.read.QueryContext(ctx, `SELECT canonical,name,oldest_history_ts FROM chat_stats WHERE oldest_history_ts IS NOT NULL ORDER BY oldest_history_ts,canonical LIMIT ?`, chatLimit)
	if err != nil {
		return out, fmt.Errorf("read logging history chats: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var row LoggingHistoryChat
		if err = rows.Scan(&row.CanonicalJID, &row.Name, &row.OldestHistory); err != nil {
			return out, err
		}
		out.Chats = append(out.Chats, row)
	}
	return out, rows.Err()
}

// LoggingAlert omits Body and LastError deliberately: both can contain sink
// details while the operational page needs only delivery facts.
type LoggingAlert struct {
	ID, RaisedAt, SentAt int64
	Kind, Title, State   string
	Attempts             int
}

func (s *Store) LoggingAlerts(ctx context.Context, limit int) ([]LoggingAlert, error) {
	limit = boundedLoggingLimit(limit, 200)
	rows, err := s.read.QueryContext(ctx, `SELECT id,kind,title,raised_at,state,attempts,COALESCE(sent_at,0)
 FROM alerts ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("read logging alerts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []LoggingAlert
	for rows.Next() {
		var a LoggingAlert
		if err = rows.Scan(&a.ID, &a.Kind, &a.Title, &a.RaisedAt, &a.State, &a.Attempts, &a.SentAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func boundedLoggingLimit(limit, fallback int) int {
	if limit < 1 {
		return fallback
	}
	if limit > 1000 {
		return 1000
	}
	return limit
}
