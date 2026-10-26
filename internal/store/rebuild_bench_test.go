package store

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// BenchmarkRebuildDerived times RebuildDerived on a generated archive. It is skipped unless ARCHIVER_BENCH_MESSAGES is
// set to the number of messages (300 chats), e.g.
//
//	ARCHIVER_BENCH_MESSAGES=300000 go test ./internal/store -run ^$ -bench RebuildDerived
//
// Reference, 300000 messages without -race: about 8.7 s per rebuild.
func BenchmarkRebuildDerived(b *testing.B) {
	n, _ := strconv.Atoi(os.Getenv("ARCHIVER_BENCH_MESSAGES"))
	if n <= 0 {
		b.Skip("set ARCHIVER_BENCH_MESSAGES to run")
	}
	s, err := Open(filepath.Join(b.TempDir(), "archive.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	if _, err = s.write.ExecContext(ctx, `WITH RECURSIVE c(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM c WHERE i<?)
INSERT INTO messages(chat_jid,id,kind,text,ts,received_at,source,raw_proto)
SELECT 'c' || (i%300) || '@g.us','M' || i,'text','message number ' || i || ' كِتَاب hello world',i,1,'live',x'78' FROM c`, n); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err = s.RebuildDerived(ctx); err != nil {
			b.Fatal(err)
		}
	}
}
