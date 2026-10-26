//go:build race

package store

// scaleMessages under -race (make check): seeding 300k messages takes about three minutes with the race detector (and
// the timings would measure the detector), so the gate runs the same assertions on 50k; the 300k run is `go test
// ./internal/store -run AtScale` without -race, whose numbers are logged in docs/v1/STATE.md.
const scaleMessages = 50_000
