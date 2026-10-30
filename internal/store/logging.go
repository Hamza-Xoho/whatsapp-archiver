package store

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"sort"
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

const loggingOpenGapsFrom = `FROM message_events e INDEXED BY message_events_gaps JOIN chat_canonical c ON c.alias=e.chat_jid
 WHERE e.kind='undecryptable'
 AND NOT EXISTS (SELECT 1 FROM chat_canonical ga JOIN messages gm ON gm.chat_jid=ga.alias WHERE ga.canonical=c.canonical AND gm.id=e.target_id)
 AND NOT EXISTS (SELECT 1 FROM chat_canonical ga JOIN message_events gs ON gs.chat_jid=ga.alias WHERE ga.canonical=c.canonical AND gs.source_id=e.target_id AND gs.source_id<>'' AND gs.kind<>'undecryptable')`

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
	var oldest int64
	if err := s.read.QueryRowContext(ctx, `SELECT COALESCE(MIN(e.received_at),0) `+loggingOpenGapsFrom).Scan(&oldest); err != nil {
		return nil, fmt.Errorf("read logging gap range: %w", err)
	}
	ends, err := s.loggingOutageEnds(ctx, oldest)
	if err != nil {
		return nil, err
	}
	rows, err := s.read.QueryContext(ctx, `SELECT c.canonical,e.chat_jid,e.target_id,e.cause,e.received_at `+loggingOpenGapsFrom)
	if err != nil {
		return nil, fmt.Errorf("read logging gap groups: %w", err)
	}
	defer func() { _ = rows.Close() }()
	type key struct {
		cause string
		post  bool
	}
	groups := make(map[key]*LoggingGapGroup, len(EventCauses)*2)
	for rows.Next() {
		var gap LoggingGap
		if err = rows.Scan(&gap.CanonicalJID, &gap.ChatJID, &gap.TargetID, &gap.Cause, &gap.ReceivedAt); err != nil {
			return nil, fmt.Errorf("scan logging gap group: %w", err)
		}
		gap.PostOutage = loggingPostOutage(ends, gap.ReceivedAt)
		groupKey := key{cause: gap.Cause, post: gap.PostOutage}
		group := groups[groupKey]
		if group == nil {
			group = &LoggingGapGroup{Cause: gap.Cause, PostOutage: gap.PostOutage, OldestReceivedAt: gap.ReceivedAt}
			groups[groupKey] = group
		}
		group.Count++
		group.OldestReceivedAt = min(group.OldestReceivedAt, gap.ReceivedAt)
		if group.Representative.TargetID == "" || gap.ReceivedAt > group.Representative.ReceivedAt {
			group.Representative = gap
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	out := make([]LoggingGapGroup, 0, len(groups))
	for _, group := range groups {
		out = append(out, *group)
	}
	slices.SortFunc(out, func(a, b LoggingGapGroup) int {
		if a.OldestReceivedAt != b.OldestReceivedAt {
			return int(a.OldestReceivedAt - b.OldestReceivedAt)
		}
		if order := compareStrings(a.Cause, b.Cause); order != 0 {
			return order
		}
		return compareStrings(fmt.Sprint(a.PostOutage), fmt.Sprint(b.PostOutage))
	})
	return out, nil
}

func (s *Store) LoggingGaps(ctx context.Context, limit int) ([]LoggingGap, error) {
	limit = boundedLoggingLimit(limit, 200)
	rows, err := s.read.QueryContext(ctx, `SELECT c.canonical,e.chat_jid,e.target_id,e.cause,e.received_at `+loggingOpenGapsFrom+
		` ORDER BY e.received_at DESC,e.seq DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("read logging gaps: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []LoggingGap
	for rows.Next() {
		var g LoggingGap
		if err = rows.Scan(&g.CanonicalJID, &g.ChatJID, &g.TargetID, &g.Cause, &g.ReceivedAt); err != nil {
			return nil, fmt.Errorf("scan logging gap: %w", err)
		}
		out = append(out, g)
	}
	if err = rows.Err(); err != nil || len(out) == 0 {
		return out, err
	}
	ends, err := s.loggingOutageEnds(ctx, out[len(out)-1].ReceivedAt)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].PostOutage = loggingPostOutage(ends, out[i].ReceivedAt)
	}
	return out, nil
}

func (s *Store) loggingOutageEnds(ctx context.Context, oldest int64) ([]int64, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT ended_at FROM connection_periods
 WHERE ended_at IS NOT NULL AND ended_at>=? ORDER BY ended_at`, oldest-120)
	if err != nil {
		return nil, fmt.Errorf("read logging outage range: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var ends []int64
	for rows.Next() {
		var end int64
		if err = rows.Scan(&end); err != nil {
			return nil, err
		}
		ends = append(ends, end)
	}
	return ends, rows.Err()
}

func loggingPostOutage(ends []int64, at int64) bool {
	i := sort.Search(len(ends), func(i int) bool { return ends[i]+120 >= at })
	return i < len(ends) && at >= ends[i]
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
	states, classes := map[string]int{}, map[string]int{}
	rows, err := s.read.QueryContext(ctx, `SELECT state,COUNT(*),COUNT(*) FILTER (WHERE error_class='network'),
 COUNT(*) FILTER (WHERE error_class='expired'),COUNT(*) FILTER (WHERE error_class='gone'),
 COUNT(*) FILTER (WHERE error_class='key'),COUNT(*) FILTER (WHERE error_class='other')
	 FROM media INDEXED BY media_pick GROUP BY state`)
	if err != nil {
		return LoggingMediaReport{}, fmt.Errorf("read logging media counts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var state string
		var count, network, expired, gone, key, other int
		if err = rows.Scan(&state, &count, &network, &expired, &gone, &key, &other); err != nil {
			return LoggingMediaReport{}, fmt.Errorf("scan logging media counts: %w", err)
		}
		states[state] += count
		for name, n := range map[string]int{"network": network, "expired": expired, "gone": gone, "key": key, "other": other} {
			if n > 0 {
				classes[name] += n
			}
		}
	}
	if err = rows.Err(); err != nil {
		return LoggingMediaReport{}, err
	}
	reasonLimit = boundedLoggingLimit(reasonLimit, 50)
	// Reasons are diagnostic examples, not ledger totals. Sample at most 5,000
	// recent scheduler-index entries; the exact state/class totals above remain
	// complete. The web layer maps every sample to controlled safe copy.
	sampleLimit := min(max(reasonLimit*100, 1000), 5000)
	reasonRows, err := s.read.QueryContext(ctx, `SELECT reason FROM media INDEXED BY media_pick
	 WHERE state IN ('failed','expired','retry_requested') AND reason<>'' LIMIT ?`, sampleLimit)
	if err != nil {
		return LoggingMediaReport{}, fmt.Errorf("read logging media reasons: %w", err)
	}
	defer func() { _ = reasonRows.Close() }()
	reasons := make(map[string]int)
	for reasonRows.Next() {
		var reason string
		if err = reasonRows.Scan(&reason); err != nil {
			return LoggingMediaReport{}, fmt.Errorf("scan logging media reason: %w", err)
		}
		reasons[reason]++
	}
	if err = reasonRows.Err(); err != nil {
		return LoggingMediaReport{}, err
	}
	out := LoggingMediaReport{States: sortedLoggingCounts(states), ErrorClasses: sortedLoggingCounts(classes)}
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
