package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestLoggingReadModelsAllHonorCancellation(t *testing.T) {
	s := openInternal(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reads := map[string]func() error{
		"gaps":           func() error { _, err := s.LoggingGaps(ctx, 10); return err },
		"gap groups":     func() error { _, err := s.LoggingGapGroups(ctx); return err },
		"media":          func() error { _, err := s.LoggingMedia(ctx, 10); return err },
		"history":        func() error { _, err := s.LoggingHistory(ctx, 10); return err },
		"alerts":         func() error { _, err := s.LoggingAlerts(ctx, 10); return err },
		"outage summary": func() error { _, err := s.OutageSummary(ctx, time.Now(), time.UTC, 7); return err },
	}
	for name, read := range reads {
		if err := read(); err == nil {
			t.Errorf("cancelled %s read succeeded", name)
		}
	}
}

func TestLoggingGapAndMediaPlansReuseExistingIndexesWithoutSchemaGrowth(t *testing.T) {
	s := openInternal(t)
	gapPlan := planOf(t, s, loggingGapGroupsQuery)
	if !strings.Contains(gapPlan, "message_events_gaps") || strings.Contains(gapPlan, "TEMP B-TREE") {
		t.Errorf("gap plan is unbounded or sorts:\n%s", gapPlan)
	}
	if strings.Contains(gapPlan, "SCAN p") || strings.Contains(gapPlan, "connection_periods") {
		t.Errorf("gap aggregation has a correlated outage scan:\n%s", gapPlan)
	}
	outagePlan := planOf(t, s, loggingOutageRangeQuery, int64(0))
	if strings.Contains(outagePlan, "TEMP B-TREE") {
		t.Errorf("outage range read sorts:\n%s", outagePlan)
	}
	for name, check := range map[string]struct{ query, access string }{
		"counts":  {loggingMediaCountsQuery, "SCAN media USING COVERING INDEX media_pick"},
		"reasons": {loggingMediaReasonsQuery, "SEARCH media USING COVERING INDEX media_pick"},
	} {
		mediaPlan := planOf(t, s, check.query)
		if !strings.Contains(mediaPlan, check.access) || strings.Contains(mediaPlan, "TEMP B-TREE") {
			t.Errorf("media %s aggregation streams the base table instead of the covering index:\n%s", name, mediaPlan)
		}
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
	if err != nil || len(media.Reasons) != 1 || media.Reasons[0].Reason != "Download failed" || media.Reasons[0].Count != 600 || media.States[0].Count != 600 {
		t.Fatalf("dense media = %+v, %v", media, err)
	}
}
