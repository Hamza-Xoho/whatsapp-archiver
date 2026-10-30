//go:build !race

package store

import "time"

const (
	loggingMediaDenseLimit = 150 * time.Millisecond
	loggingGapDenseLimit   = 150 * time.Millisecond
)
