package ingest_test

import (
	"strings"
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
	"whatsapp-archiver/internal/activity"
	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/testkit"
)

func activityPipeline(t *testing.T, f *ingest.Filter) (*ingest.Pipeline, *activity.Bus) {
	bus := activity.NewBus(500)
	return ingest.NewPipeline(testkit.Store(t), f, &queueFake{}, &healthFake{}, quiet()).WithHistoryParser(historyParser()).WithActivity(bus), bus
}

func TestPipelinePublishesNoText(t *testing.T) {
	p, bus := activityPipeline(t, ingest.NewFilter(nil, nil))
	p.Handle(messageEvent("g@g.us", "A", text("secret words")))
	got := bus.Recent()
	if len(got) != 1 || got[0].Kind != "message" || got[0].Chat != "g@g.us" {
		t.Fatalf("events=%+v", got)
	}
	for _, e := range got {
		if all := e.Kind + e.Chat + e.Detail; strings.Contains(all, "secret") || strings.Contains(all, "words") {
			t.Fatalf("event carries message text: %+v", e)
		}
	}
}

func TestPipelinePublishesEditsAndRevokesAsEvents(t *testing.T) {
	p, bus := activityPipeline(t, ingest.NewFilter(nil, nil))
	edit := protocolMsg(waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(), "NEVER")
	edit.EditedMessage = text("new secret words")
	p.Handle(messageEvent("g@g.us", "E1", &waE2E.Message{ProtocolMessage: edit}))
	got := bus.Recent()
	if len(got) != 1 || got[0].Kind != "event" || got[0].Detail != "edit" || strings.Contains(got[0].Detail+got[0].Chat, "secret") {
		t.Fatalf("events=%+v", got)
	}
}

func TestPipelinePublishesGapForUndecryptable(t *testing.T) {
	p, bus := activityPipeline(t, ingest.NewFilter(nil, nil))
	p.Handle(&events.UndecryptableMessage{Info: messageEvent("g@g.us", "X", text("x")).Info})
	got := bus.Recent()
	if len(got) != 1 || got[0].Kind != "gap" || got[0].Chat != "g@g.us" {
		t.Fatalf("events=%+v", got)
	}
}

func TestPipelinePublishesChatEvent(t *testing.T) {
	p, bus := activityPipeline(t, ingest.NewFilter(nil, nil))
	p.Handle(&events.Pin{JID: jid("g@g.us"), Timestamp: messageEvent("g@g.us", "A", nil).Info.Timestamp})
	got := bus.Recent()
	if len(got) != 1 || got[0].Kind != "chat_event" || got[0].Chat != "g@g.us" {
		t.Fatalf("events=%+v", got)
	}
}

func TestPipelinePublishesExcludedSkipWithoutText(t *testing.T) {
	f := ingest.NewFilter(ingest.NewRuleSet(ingest.ExclusionRules([]string{"g@g.us"})), nil)
	p, bus := activityPipeline(t, f)
	p.Handle(messageEvent("g@g.us", "A", text("secret words")))
	got := bus.Recent()
	if len(got) != 1 || got[0].Kind != "excluded_skip" || got[0].Chat != "g@g.us" || strings.Contains(got[0].Detail, "secret") {
		t.Fatalf("events=%+v", got)
	}
}

func TestHistoryChunkPublishesOneEventNotOnePerMessage(t *testing.T) {
	p, bus := activityPipeline(t, ingest.NewFilter(nil, nil))
	hs := chunk("g@g.us", webMsg("H1", "3@lid", false, text("secret one")), webMsg("H2", "3@lid", false, text("secret two")))
	if _, _, err := p.ImportHistory(t.Context(), hs); err != nil {
		t.Fatal(err)
	}
	got := bus.Recent()
	if len(got) != 1 || got[0].Kind != "history_chunk" || strings.Contains(got[0].Detail, "secret") {
		t.Fatalf("events=%+v", got)
	}
	if !strings.Contains(got[0].Detail, "2 messages") || !strings.Contains(got[0].Detail, "#") {
		t.Fatalf("detail=%q", got[0].Detail)
	}
}

func TestPipelinePublishesNothingForAConnectionEvent(t *testing.T) {
	st := testkit.Store(t)
	bus := activity.NewBus(500)
	p := ingest.NewPipeline(st, ingest.NewFilter(nil, nil), &queueFake{}, &healthFake{}, quiet()).WithActivity(bus)
	p.Handle(&events.Connected{}) // a state change, not a stored event
	if got := bus.Recent(); len(got) != 0 {
		t.Fatalf("events=%+v", got)
	}
}
