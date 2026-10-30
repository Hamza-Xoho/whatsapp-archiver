package web

import (
	"fmt"
	"time"

	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/view"
)

func outageSummary(rows []store.ConnectionPeriod, now time.Time, loc *time.Location) (string, string) {
	y, m, d := now.In(loc).Date()
	startToday := time.Date(y, m, d, 0, 0, 0, 0, loc)
	var today, longest time.Duration
	for _, row := range rows {
		start := time.Unix(row.StartedAt, 0)
		end := now
		if row.EndedAt > 0 {
			end = time.Unix(row.EndedAt, 0)
		}
		if duration := end.Sub(start); duration > longest {
			longest = duration
		}
		if start.Before(startToday) {
			start = startToday
		}
		if end.After(now) {
			end = now
		}
		if end.After(start) {
			today += end.Sub(start)
		}
	}
	minutes := int(today / time.Minute)
	return fmt.Sprintf("%d %s", minutes, plural(minutes, "minute", "minutes")), durationWords(longest)
}

func optionalCountWords(value *int64) string {
	if value == nil {
		return "Unknown"
	}
	return fmt.Sprint(*value)
}

func groupLoggingGaps(rows []store.LoggingGap, now time.Time) []loggingGapView {
	positions := make(map[string]int)
	var out []loggingGapView
	for _, gap := range rows {
		key := fmt.Sprintf("%s|%t", gap.Cause, gap.PostOutage)
		age := agoWords(now.Sub(time.Unix(gap.ReceivedAt, 0)))
		if at, ok := positions[key]; ok {
			out[at].Count++
			out[at].Oldest = age
			continue
		}
		positions[key] = len(out)
		out = append(out, loggingGapView{CanonicalJID: gap.CanonicalJID, TargetID: gap.TargetID,
			Cause: gapCauseWords(gap.Cause), Age: age, Oldest: age, PostOutage: gap.PostOutage, Count: 1})
	}
	return out
}

func hasMediaFailures(report store.LoggingMediaReport) bool {
	for _, row := range report.States {
		switch row.Name {
		case "failed", "expired", "retry_requested":
			if row.Count > 0 {
				return true
			}
		}
	}
	return false
}

func optionalUnix(loc *time.Location, value int64) string {
	if value == 0 {
		return "Unknown"
	}
	return view.FormatUnix(loc, value)
}

func anyCompleteSync(rows []store.SyncCoverage) bool {
	for _, row := range rows {
		if row.Complete {
			return true
		}
	}
	return false
}
