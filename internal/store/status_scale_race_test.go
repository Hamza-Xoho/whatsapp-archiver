//go:build race

package store

import "time"

// scaleMessages under -race (make check): seeding 300k messages takes about three minutes with the race detector (and
// the timings would measure the detector), so the gate runs the same assertions on 50k; the 300k run is `go test
// ./internal/store -run AtScale` without -race, whose numbers are logged in docs/v1/STATE.md.
const scaleMessages = 50_000

// statusMedianLimit under -race: the detector slows every query several times (22 ms measured at 50k), so the bound
// stays loose; the plan's 100 ms is asserted in the build without -race.
const statusMedianLimit = 250 * time.Millisecond
