package store

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
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

// LoggingGapGroup is one complete-ledger aggregate plus a bounded trace
// representative. Counts and oldest times are never derived from the detail
// window.
type LoggingGapGroup struct {
	Cause            string
	PostOutage       bool
	Count            int
	OldestReceivedAt int64
	Representative   LoggingGap
}

// LoggingGapGroups aggregates every open gap, then resolves at most one safe
// representative per closed cause/post-outage bucket. Event causes are a
// closed enum, so the representative reads are bounded independently of the
// archive size.
func (s *Store) LoggingGapGroups(ctx context.Context) ([]LoggingGapGroup, error) {
	summary, err := s.OpenGapSummary(ctx)
	if err != nil {
		return nil, fmt.Errorf("read logging gap summary: %w", err)
	}
	out := make([]LoggingGapGroup, 0, len(summary))
	for _, item := range summary {
		representative, readErr := s.loggingGapRepresentative(ctx, item.Cause, item.PostOutage)
		if readErr != nil {
			return nil, readErr
		}
		out = append(out, LoggingGapGroup{Cause: item.Cause, PostOutage: item.PostOutage, Count: item.Count,
			OldestReceivedAt: item.OldestReceivedAt, Representative: representative})
	}
	return out, nil
}

func (s *Store) loggingGapRepresentative(ctx context.Context, cause string, postOutage bool) (LoggingGap, error) {
	var gap LoggingGap
	err := s.read.QueryRowContext(ctx, `SELECT canonical_jid,chat_jid,target_id,cause,received_at,post_outage
 FROM open_gaps WHERE cause=? AND post_outage=? ORDER BY received_at DESC,seq DESC LIMIT 1`, cause, postOutage).Scan(
		&gap.CanonicalJID, &gap.ChatJID, &gap.TargetID, &gap.Cause, &gap.ReceivedAt, &gap.PostOutage)
	if err != nil {
		return LoggingGap{}, fmt.Errorf("read logging gap representative: %w", err)
	}
	return gap, nil
}

func (s *Store) LoggingGaps(ctx context.Context, limit int) ([]LoggingGap, error) {
	limit = boundedLoggingLimit(limit, 200)
	rows, err := s.read.QueryContext(ctx, `SELECT c.canonical,e.chat_jid,e.target_id,e.cause,e.received_at,
 EXISTS (SELECT 1 FROM connection_periods p WHERE p.ended_at IS NOT NULL AND e.received_at BETWEEN p.ended_at AND p.ended_at+120)
 FROM message_events e INDEXED BY message_events_gaps JOIN chat_canonical c ON c.alias=e.chat_jid
 WHERE e.kind='undecryptable'
 AND NOT EXISTS (SELECT 1 FROM chat_canonical ga JOIN messages gm ON gm.chat_jid=ga.alias WHERE ga.canonical=c.canonical AND gm.id=e.target_id)
 AND NOT EXISTS (SELECT 1 FROM chat_canonical ga JOIN message_events gs ON gs.chat_jid=ga.alias WHERE ga.canonical=c.canonical AND gs.source_id=e.target_id AND gs.source_id<>'' AND gs.kind<>'undecryptable')
 ORDER BY e.received_at DESC,e.seq DESC LIMIT ?`, limit)
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
	states, classes, reasons := map[string]int{}, map[string]int{}, map[string]int{}
	rows, err := s.read.QueryContext(ctx, `SELECT state,error_class,reason FROM media INDEXED BY media_pick ORDER BY state,source_rank,message_ts DESC`)
	if err != nil {
		return LoggingMediaReport{}, fmt.Errorf("read logging media: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var state, class, reason string
		if err = rows.Scan(&state, &class, &reason); err != nil {
			return LoggingMediaReport{}, fmt.Errorf("scan logging media: %w", err)
		}
		states[state]++
		if class != "" {
			classes[class]++
		}
		if reason != "" {
			reasons[reason]++
		}
	}
	if err = rows.Err(); err != nil {
		return LoggingMediaReport{}, err
	}
	out := LoggingMediaReport{States: sortedLoggingCounts(states), ErrorClasses: sortedLoggingCounts(classes)}
	reasonLimit = boundedLoggingLimit(reasonLimit, 50)
	for reason, count := range reasons {
		out.Reasons = append(out.Reasons, LoggingMediaReason{Reason: reason, Count: count})
	}
	slices.SortFunc(out.Reasons, func(a, b LoggingMediaReason) int {
		if a.Count != b.Count {
			return b.Count - a.Count
		}
		return compareStrings(a.Reason, b.Reason)
	})
	if len(out.Reasons) > reasonLimit {
		out.Reasons = out.Reasons[:reasonLimit]
	}
	return out, nil
}

func sortedLoggingCounts(counts map[string]int) []LoggingCount {
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	slices.Sort(names)
	out := make([]LoggingCount, 0, len(names))
	for _, name := range names {
		out = append(out, LoggingCount{Name: name, Count: counts[name]})
	}
	return out
}

func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

type LoggingHistoryChat struct {
	CanonicalJID, Name string
	OldestHistory      int64
	HasHistory         bool
}

type LoggingHistoryReport struct {
	Syncs                     []SyncCoverage
	SpoolBacklog, SpoolErrors int
	Chats                     []LoggingHistoryChat
	AuthoritativeComplete     bool
	TotalChats                int
	HasMore                   bool
	NextAfter                 string
}

func (s *Store) LoggingHistory(ctx context.Context, chatLimit int) (LoggingHistoryReport, error) {
	return s.LoggingHistoryPage(ctx, chatLimit, "")
}

// LoggingHistoryPage reads one canonical-JID keyset page. TotalChats makes a
// bounded page explicit rather than silently dropping chats beyond the limit.
func (s *Store) LoggingHistoryPage(ctx context.Context, chatLimit int, after string) (LoggingHistoryReport, error) {
	coverage, err := s.historyCoverage(ctx)
	if err != nil {
		return LoggingHistoryReport{}, err
	}
	out := LoggingHistoryReport{Syncs: coverage.Syncs, AuthoritativeComplete: coverage.Complete}
	if err = s.read.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(error<>''),0) FROM history_spool WHERE imported_at IS NULL`).Scan(&out.SpoolBacklog, &out.SpoolErrors); err != nil {
		return out, fmt.Errorf("read logging spool counts: %w", err)
	}
	if err = s.read.QueryRowContext(ctx, `SELECT COUNT(*) FROM chat_stats`).Scan(&out.TotalChats); err != nil {
		return out, fmt.Errorf("count logging history chats: %w", err)
	}
	chatLimit = boundedLoggingLimit(chatLimit, 250)
	rows, err := s.read.QueryContext(ctx, `SELECT canonical,name,COALESCE(oldest_history_ts,0),oldest_history_ts IS NOT NULL
	 FROM chat_stats WHERE canonical>? ORDER BY canonical LIMIT ?`, after, chatLimit+1)
	if err != nil {
		return out, fmt.Errorf("read logging history chats: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var row LoggingHistoryChat
		if err = rows.Scan(&row.CanonicalJID, &row.Name, &row.OldestHistory, &row.HasHistory); err != nil {
			return out, err
		}
		if len(out.Chats) == chatLimit {
			out.HasMore = true
			break
		}
		out.Chats = append(out.Chats, row)
	}
	if len(out.Chats) > 0 {
		out.NextAfter = out.Chats[len(out.Chats)-1].CanonicalJID
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
