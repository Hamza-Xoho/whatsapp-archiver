package ingest

// ConnectionStateSink is the narrow health boundary used by connection
// observers. Keeping the SetState call in ingest preserves the receive-path
// package boundary: internal/wa reports facts, and ingest maps them to health.
type ConnectionStateSink interface {
	SetState(string, string)
}

// ReportConnectionState applies a state observation to health.
func ReportConnectionState(sink ConnectionStateSink, state, detail string) {
	sink.SetState(state, detail)
}
