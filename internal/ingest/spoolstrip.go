package ingest

import "go.mau.fi/whatsmeow/proto/waHistorySync"

// RED STUBS (I3): the real implementation lands in the green commit.

func StripHistorySecrets(blob []byte, format string) ([]byte, error) { return blob, nil }

func StripHistorySync(hs *waHistorySync.HistorySync) *waHistorySync.HistorySync { return hs }
