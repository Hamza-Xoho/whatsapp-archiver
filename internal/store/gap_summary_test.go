package store_test

import (
	"context"
	"testing"

	"whatsapp-archiver/internal/store"
)

// W5.3 uses the two Task 6 shapes through the operational summary too: the protocol event is the …6F66
// message itself, while a reaction only targets …FCCF and must leave that gap open.
func TestOpenGapSummaryGroupsCauseAndPostOutageAfterApplyingR11(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if _, err := s.InsertIDMapping(ctx, store.IDMapping{LID: "77@lid", PN: "9665@s.whatsapp.net", Source: "test"}); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertClosedConnectionPeriod(ctx, store.ConnectionPeriod{State: "reconnecting", StartedAt: 10, EndedAt: 100}); err != nil {
		t.Fatal(err)
	}

	closed := gap("9665@s.whatsapp.net", "3A1B6F66")
	closed.ReceivedAt = 50
	applyEvent(t, s, closed)
	applyEvent(t, s, store.MessageEvent{ChatJID: "77@lid", Kind: "protocol", EventID: "protocol-6F66", SourceID: "3A1B6F66", ReceivedAt: 60})

	stillOpen := gap("9665@s.whatsapp.net", "3A1BFCCF")
	stillOpen.ReceivedAt = 220
	applyEvent(t, s, stillOpen)
	applyEvent(t, s, store.MessageEvent{ChatJID: "77@lid", TargetID: "3A1BFCCF", Kind: "reaction", EventID: "reaction-FCCF", SourceID: "REACTION-ID", ReceivedAt: 221})

	other := gap("77@lid", "OTHER")
	other.Cause = "view_once_unavailable"
	other.ReceivedAt = 300
	applyEvent(t, s, other)

	got, err := s.OpenGapSummary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("summary = %+v, want two cause/post-outage groups", got)
	}
	if got[0].Cause != "no_sender_key" || !got[0].PostOutage || got[0].Count != 1 || got[0].OldestReceivedAt != 220 {
		t.Errorf("first summary = %+v", got[0])
	}
	if got[1].Cause != "view_once_unavailable" || got[1].PostOutage || got[1].Count != 1 || got[1].OldestReceivedAt != 300 {
		t.Errorf("second summary = %+v", got[1])
	}
}

func TestOpenGapSummaryKeepsPostOutageGroupsSeparateForTheSameCause(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if err := s.InsertClosedConnectionPeriod(ctx, store.ConnectionPeriod{State: "reconnecting", StartedAt: 10, EndedAt: 100}); err != nil {
		t.Fatal(err)
	}
	postOutage := gap("g@g.us", "POST")
	postOutage.ReceivedAt = 220
	applyEvent(t, s, postOutage)
	ordinary := gap("g@g.us", "ORDINARY")
	ordinary.ReceivedAt = 300
	applyEvent(t, s, ordinary)

	got, err := s.OpenGapSummary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("summary = %+v, want separate post-outage and ordinary groups", got)
	}
	if got[0].Cause != "no_sender_key" || !got[0].PostOutage || got[0].Count != 1 || got[0].OldestReceivedAt != 220 {
		t.Errorf("post-outage summary = %+v", got[0])
	}
	if got[1].Cause != "no_sender_key" || got[1].PostOutage || got[1].Count != 1 || got[1].OldestReceivedAt != 300 {
		t.Errorf("ordinary summary = %+v", got[1])
	}
}
