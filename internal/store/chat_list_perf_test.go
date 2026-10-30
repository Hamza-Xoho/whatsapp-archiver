package store

import (
	"cmp"
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// TestChatsSQLAtScale reports individual SQL timings on the prescribed W6(h) archive. It is opt-in because the release
// harness owns that 300k/250 seed; normal test runs have no external data dependency.
func TestChatsSQLAtScale(t *testing.T) {
	data := os.Getenv("W6B_DEVSEED_DATA")
	if data == "" {
		t.Skip("set W6B_DEVSEED_DATA to the 300k/250 devseed data directory")
	}
	s, err := Open(filepath.Join(data, "archive.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	f := ChatListFilter{Query: "Synthetic", Type: "direct", Gaps: true}
	where, args := chatListWhere(f)
	type measurement struct {
		name    string
		samples []time.Duration
		run     func() error
	}
	measurements := []*measurement{
		{name: "filtered count", run: func() error {
			var n int
			return s.read.QueryRowContext(ctx, `SELECT COUNT(*) FROM (`+chatListPageBase+where+`)`, args...).Scan(&n)
		}},
		{name: "filtered LIMIT 51 page", run: func() error {
			rows, queryErr := s.read.QueryContext(ctx, chatListPageBase+where+` ORDER BY s.last_ts DESC,s.messages=0,s.canonical LIMIT 51`, args...)
			if queryErr != nil {
				return queryErr
			}
			defer func() { _ = rows.Close() }()
			for rows.Next() {
				var jid, name string
				var values [13]any
				values[0], values[1] = &jid, &name
				for i := 2; i < len(values); i++ {
					values[i] = new(int64)
				}
				if scanErr := rows.Scan(values[:]...); scanErr != nil {
					return scanErr
				}
			}
			return rows.Err()
		}},
		{name: "filtered ordered membership", run: func() error {
			rows, queryErr := s.read.QueryContext(ctx, `SELECT canonical FROM (`+chatListPageBase+where+`) ORDER BY last_ts DESC,messages=0,canonical`, args...)
			if queryErr != nil {
				return queryErr
			}
			defer func() { _ = rows.Close() }()
			for rows.Next() {
				var jid string
				if scanErr := rows.Scan(&jid); scanErr != nil {
					return scanErr
				}
			}
			return rows.Err()
		}},
	}
	for _, m := range measurements {
		for i := range 35 {
			start := time.Now()
			if err = m.run(); err != nil {
				t.Fatalf("%s: %v", m.name, err)
			}
			if i >= 5 {
				m.samples = append(m.samples, time.Since(start))
			}
		}
		slices.Sort(m.samples)
		t.Logf("SQL %s: p50=%v p95=%v max=%v", m.name, m.samples[14], m.samples[28], m.samples[29])
	}
	slices.SortFunc(measurements, func(a, b *measurement) int { return cmp.Compare(b.samples[28], a.samples[28]) })
	t.Logf("slowest SQL: %s (p95=%v)", measurements[0].name, measurements[0].samples[28])
}
