package store

import (
	"fmt"
	"testing"
	"time"
)

func TestLoggingMediaAggregatesThreeHundredThousandRowsWithinBudget(t *testing.T) {
	s := openInternal(t)
	_, err := s.write.ExecContext(t.Context(), `WITH RECURSIVE n(i) AS (
 VALUES(0) UNION ALL SELECT i+1 FROM n WHERE i<299999
) INSERT INTO media(chat_jid,message_id,source,source_rank,type,state,error_class,reason)
SELECT 'chat-'||(i%250)||'@g.us','media-'||i,'live',1,'image',
 CASE i%5 WHEN 0 THEN 'done' WHEN 1 THEN 'pending' WHEN 2 THEN 'downloading' WHEN 3 THEN 'expired' ELSE 'failed' END,
 CASE WHEN i%5=3 THEN 'expired' WHEN i%5=4 THEN 'network' ELSE '' END,
 CASE WHEN i%5=3 THEN '403 expired' WHEN i%10=4 THEN 'DNS lookup failed' WHEN i%10=9 THEN 'request timeout' ELSE '' END
FROM n`)
	if err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	report, err := s.LoggingMedia(t.Context(), 50)
	took := time.Since(started)
	if err != nil {
		t.Fatal(err)
	}
	if took >= loggingMediaDenseLimit {
		t.Fatalf("300k-media aggregation took %v, want <%v", took, loggingMediaDenseLimit)
	}
	for _, state := range []string{"done", "pending", "downloading", "expired", "failed"} {
		if countLogging(report.States, state) != 60_000 {
			t.Errorf("state %s = %d, want 60000", state, countLogging(report.States, state))
		}
	}
	if countLogging(report.ErrorClasses, "expired") != 60_000 || countLogging(report.ErrorClasses, "network") != 60_000 {
		t.Errorf("error classes = %+v", report.ErrorClasses)
	}
	if len(report.States) > 7 || len(report.ErrorClasses) > 6 || len(report.Reasons) > 8 {
		t.Fatalf("unbounded media groups: states=%d classes=%d reasons=%d", len(report.States), len(report.ErrorClasses), len(report.Reasons))
	}
	t.Logf("300k media rows aggregated in %v", took)
}

func TestLoggingGapGroupingDoesNotScanPeriodsPerGap(t *testing.T) {
	s := openInternal(t)
	if _, err := s.write.ExecContext(t.Context(), `INSERT INTO chats(jid,type) VALUES('dense@g.us','group');
INSERT INTO chat_canonical(alias,canonical) VALUES('dense@g.us','dense@g.us')`); err != nil {
		t.Fatal(err)
	}
	const rows = 2000
	if _, err := s.write.ExecContext(t.Context(), fmt.Sprintf(`WITH RECURSIVE n(i) AS (
 VALUES(0) UNION ALL SELECT i+1 FROM n WHERE i<%d
) INSERT INTO connection_periods(state,started_at,ended_at,cause_class)
SELECT 'disconnected',i*100,i*100+10,'tcp' FROM n`, rows-1)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.write.ExecContext(t.Context(), fmt.Sprintf(`WITH RECURSIVE n(i) AS (
 VALUES(0) UNION ALL SELECT i+1 FROM n WHERE i<%d
) INSERT INTO message_events(chat_jid,target_id,kind,event_id,cause,received_at)
SELECT 'dense@g.us','missing-'||i,'undecryptable','gap-'||i,'no_sender_key',i*100+11 FROM n`, rows-1)); err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	groups, err := s.LoggingGapGroups(t.Context())
	took := time.Since(started)
	if err != nil {
		t.Fatal(err)
	}
	if took >= loggingGapDenseLimit {
		t.Fatalf("%d gaps x %d periods took %v, want <%v without a correlated period scan", rows, rows, took, loggingGapDenseLimit)
	}
	if len(groups) != 1 || groups[0].Count != rows || !groups[0].PostOutage {
		t.Fatalf("gap groups = %+v", groups)
	}
	t.Logf("%d dense gaps and periods grouped in %v", rows, took)
}

func countLogging(rows []LoggingCount, name string) int {
	for _, row := range rows {
		if row.Name == name {
			return row.Count
		}
	}
	return 0
}
