//go:build !race

package store

import "time"

// scaleMessages is the size of the archive the W2(f) scale tests read: the plan's 300k messages.
const scaleMessages = 300_000

// statusMedianLimit is the plan's StatusReport target (100 ms at 300k messages), asserted as it is.
const statusMedianLimit = 100 * time.Millisecond
