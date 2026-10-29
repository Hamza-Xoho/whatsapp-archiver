package store

import (
	"context"
	"time"
)

// OutageDay is the outage time attributed to one local calendar day.
type OutageDay struct {
	Date    string
	Minutes int
}

// OutageSummary is the recent daily outage total plus the longest period in the ledger.
type OutageSummary struct {
	Days       []OutageDay
	Longest    time.Duration
	HasOutages bool
}

// OutageSummary splits outage periods across local calendar days. Open periods end at now.
func (s *Store) OutageSummary(ctx context.Context, now time.Time, loc *time.Location, days int) (OutageSummary, error) {
	if loc == nil {
		loc = time.UTC
	}
	if days < 1 {
		days = 1
	}
	periods, err := s.ConnectionPeriods(ctx, 0)
	if err != nil {
		return OutageSummary{}, err
	}
	now = now.In(loc)
	startToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	firstDay := startToday.AddDate(0, 0, 1-days)
	durations := make([]time.Duration, days)
	out := OutageSummary{Days: make([]OutageDay, days)}
	for i := range days {
		out.Days[i].Date = firstDay.AddDate(0, 0, i).Format("2006-01-02")
	}
	for _, p := range periods {
		start := time.Unix(p.StartedAt, 0).In(loc)
		end := now
		if p.EndedAt != 0 {
			end = time.Unix(p.EndedAt, 0).In(loc)
		}
		if end.After(now) {
			end = now
		}
		if !end.After(start) {
			continue
		}
		out.HasOutages = true
		if d := end.Sub(start); d > out.Longest {
			out.Longest = d
		}
		for i := range days {
			dayStart := firstDay.AddDate(0, 0, i)
			dayEnd := dayStart.AddDate(0, 0, 1)
			from, to := maxTime(start, dayStart), minTime(end, dayEnd)
			if to.After(from) {
				durations[i] += to.Sub(from)
			}
		}
	}
	for i, d := range durations {
		out.Days[i].Minutes = int(d / time.Minute)
	}
	return out, nil
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
