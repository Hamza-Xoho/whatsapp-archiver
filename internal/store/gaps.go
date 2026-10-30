package store

import "context"

// GapSummary is one operational group of open gaps. PostOutage is derived from the outage ledger at read time
// (schema R12); Cause remains the raw undecryptable cause recorded on each event.
type GapSummary struct {
	Cause            string
	PostOutage       bool
	Count            int
	OldestReceivedAt int64
}

// OpenGapSummary groups the indexed open-gaps view without adding another archive-wide read path. A reaction that
// merely targets a missing message remains in the view; an event whose SourceID is that message does not (R11).
func (s *Store) OpenGapSummary(ctx context.Context) ([]GapSummary, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT cause,post_outage,COUNT(*),MIN(received_at)
		FROM open_gaps GROUP BY cause,post_outage ORDER BY MIN(received_at),cause,post_outage`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []GapSummary
	for rows.Next() {
		var g GapSummary
		if err = rows.Scan(&g.Cause, &g.PostOutage, &g.Count, &g.OldestReceivedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
