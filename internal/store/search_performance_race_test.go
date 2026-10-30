//go:build race

package store_test

// The 300 ms budget is release-server time. Race instrumentation changes the measured operation and is covered by
// the functional Search tests; the exact budget stays enabled in search_performance_norace_test.go.
const searchPerformanceEnabled = false
