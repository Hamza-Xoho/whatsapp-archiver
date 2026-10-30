//go:build race

package store

import "time"

// Race instrumentation changes modernc SQLite execution itself. The release
// budget remains pinned in logging_w6f_review_bounded_norace_test.go; these
// ceilings keep the same 300k/2k dense functional gate under make check.
const (
	loggingMediaDenseLimit = 8 * time.Second
	loggingGapDenseLimit   = time.Second
)
