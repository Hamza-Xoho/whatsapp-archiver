package store

import (
	"strings"
	"testing"
	"time"
)

func TestLoggingMediaReasonTaxonomyCountsEveryApplicableRowExactly(t *testing.T) {
	s := openInternal(t)
	_, err := s.write.ExecContext(t.Context(), `WITH RECURSIVE n(i) AS (
 VALUES(0) UNION ALL SELECT i+1 FROM n WHERE i<29999
) INSERT INTO media(chat_jid,message_id,source,source_rank,type,state,error_class,reason)
SELECT 'reason@g.us','reason-'||i,'live',1,'image','failed',
 CASE WHEN i<6000 THEN 'expired' WHEN i<10000 THEN 'network' WHEN i<14000 THEN 'network'
      WHEN i<18000 THEN 'gone' WHEN i<22000 THEN 'key' WHEN i<26000 THEN 'network' ELSE 'other' END,
 CASE WHEN i<6000 THEN '403 expired before the old sample limit'
      WHEN i<10000 THEN 'dns lookup failed https://user:pass@secret.invalid/a?token=DO_NOT_RENDER'
      WHEN i<14000 THEN 'request deadline timeout /home/private/media'
      WHEN i<18000 THEN 'HTTP 410 gone' WHEN i<22000 THEN 'missing media key'
      WHEN i<26000 THEN 'connection reset' ELSE 'unexpected private detail' END
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
		t.Fatalf("exact reason aggregation took %v, want <%v", took, loggingMediaDenseLimit)
	}
	want := map[string]int{
		"Media expired": 6000, "DNS lookup failed": 4000, "Request timed out": 4000,
		"Media unavailable": 4000, "Media key unavailable": 4000,
		"Network request failed": 4000, "Download failed": 4000,
	}
	total := 0
	for _, reason := range report.Reasons {
		if strings.Contains(reason.Reason, "secret") || strings.Contains(reason.Reason, "/home/") || strings.Contains(reason.Reason, "token") {
			t.Fatalf("unsafe reason crossed store boundary: %+v", reason)
		}
		if want[reason.Reason] != reason.Count {
			t.Errorf("reason %q = %d, want %d", reason.Reason, reason.Count, want[reason.Reason])
		}
		total += reason.Count
		delete(want, reason.Reason)
	}
	if len(want) != 0 || total != 30_000 {
		t.Fatalf("reason taxonomy omitted rows: remaining=%v total=%d report=%+v", want, total, report.Reasons)
	}
	t.Logf("30k exact safe media reasons aggregated in %v", took)
}
