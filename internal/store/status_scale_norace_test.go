//go:build !race

package store

// scaleMessages is the size of the archive the W2(f) scale tests read: the plan's 300k messages.
const scaleMessages = 300_000
