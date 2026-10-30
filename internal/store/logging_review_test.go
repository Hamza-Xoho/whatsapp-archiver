package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

const reviewLoggingGapsQuery = `SELECT c.canonical,e.chat_jid,e.target_id,e.cause,e.received_at,
 EXISTS (SELECT 1 FROM connection_periods p WHERE p.ended_at IS NOT NULL AND e.received_at BETWEEN p.ended_at AND p.ended_at+120)
 FROM message_events e INDEXED BY message_events_gaps JOIN chat_canonical c ON c.alias=e.chat_jid
 WHERE e.kind='undecryptable'
 AND NOT EXISTS (SELECT 1 FROM chat_canonical ga JOIN messages gm ON gm.chat_jid=ga.alias WHERE ga.canonical=c.canonical AND gm.id=e.target_id)
 AND NOT EXISTS (SELECT 1 FROM chat_canonical ga JOIN message_events gs ON gs.chat_jid=ga.alias WHERE ga.canonical=c.canonical AND gs.source_id=e.target_id AND gs.source_id<>'' AND gs.kind<>'undecryptable')
 ORDER BY e.received_at DESC,e.seq DESC LIMIT ?`

func TestLoggingReadModelsAllHonorCancellation(t *testing.T) {
	s := openInternal(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reads := map[string]func() error{
		"gaps":    func() error { _, err := s.LoggingGaps(ctx, 10); return err },
		"media":   func() error { _, err := s.LoggingMedia(ctx, 10); return err },
		"history": func() error { _, err := s.LoggingHistory(ctx, 10); return err },
		"alerts":  func() error { _, err := s.LoggingAlerts(ctx, 10); return err },
	}
	for name, read := range reads {
		if err := read(); err == nil {
			t.Errorf("cancelled %s read succeeded", name)
		}
	}
}

func TestLoggingGapAndMediaPlansReuseExistingIndexesWithoutSchemaGrowth(t *testing.T) {
	s := openInternal(t)
	gapPlan := planOf(t, s, reviewLoggingGapsQuery, 200)
	if !strings.Contains(gapPlan, "message_events_gaps") || strings.Contains(gapPlan, "TEMP B-TREE") {
		t.Errorf("gap plan is unbounded or sorts:\n%s", gapPlan)
	}
	mediaPlan := planOf(t, s, `SELECT state,error_class,reason FROM media INDEXED BY media_pick ORDER BY state,source_rank,message_ts DESC`)
	if !strings.Contains(mediaPlan, "COVERING INDEX media_pick") || strings.Contains(mediaPlan, "TEMP B-TREE") {
		t.Errorf("media aggregation does not stay on the existing covering scheduler index:\n%s", mediaPlan)
	}
	for name, q := range map[string]struct {
		query string
		args  []any
	}{"claim": {claimMediaQuery, []any{int64(1000)}}, "expired": {nextExpiredQuery, nil}} {
		plan := planOf(t, s, q.query, q.args...)
		if !strings.Contains(plan, "media_pick") || strings.Contains(plan, "TEMP B-TREE") {
			t.Errorf("existing %s scheduler plan regressed:\n%s", name, plan)
		}
	}
	var added int
	if err := s.read.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND (name LIKE 'media_logging_%' OR name LIKE 'message_events_logging_%')`).Scan(&added); err != nil {
		t.Fatal(err)
	}
	if added != 0 {
		t.Errorf("logging added %d fresh b-trees instead of reusing V1 indexes", added)
	}
}

func TestLoggingDenseGapAndMediaResultsStayBounded(t *testing.T) {
	s := openInternal(t)
	for i := range 600 {
		chat, id := fmt.Sprintf("dense-%02d@g.us", i%20), fmt.Sprintf("missing-%04d", i)
		if _, err := s.InsertEvent(t.Context(), MessageEvent{ChatJID: chat, TargetID: id, Kind: "undecryptable", EventID: id, Cause: "no_sender_key", ReceivedAt: int64(i + 1)}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.write.ExecContext(t.Context(), `INSERT INTO media(chat_jid,message_id,source,source_rank,type,state,error_class,reason) VALUES(?,?,'live',1,'image','failed','network',?)`, chat, id, fmt.Sprintf("reason-%02d", i%80)); err != nil {
			t.Fatal(err)
		}
	}
	gaps, err := s.LoggingGaps(t.Context(), 200)
	if err != nil || len(gaps) != 200 {
		t.Fatalf("dense gaps = %d, %v", len(gaps), err)
	}
	media, err := s.LoggingMedia(t.Context(), 50)
	if err != nil || len(media.Reasons) != 50 || media.States[0].Count != 600 {
		t.Fatalf("dense media = %+v, %v", media, err)
	}
}
