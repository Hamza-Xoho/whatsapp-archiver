package ingest_test

import (
	"io"
	"log/slog"
	"testing"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/testkit"
)

func TestHistoryThenLiveNoDuplicate(t *testing.T) {
	st := testkit.Store(t)
	p := ingest.NewPipeline(st, ingest.NewFilter(nil, nil), &queueFake{}, &healthFake{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	hs := history("g@g.us", "ABC", 1)
	stored, skipped, err := p.ImportHistory(t.Context(), hs)
	if err != nil || stored != 1 || skipped != 0 {
		t.Fatalf("stored=%d skipped=%d err=%v", stored, skipped, err)
	}
	p.Handle(messageEvent("g@g.us", "ABC", text("live")))
	stats, _ := st.ChatStats(t.Context())
	if stats[0].MessageCount != 1 {
		t.Fatalf("stats=%+v", stats)
	}
}
func TestLateChunkAfterCompleteIsStored(t *testing.T) {
	st := testkit.Store(t)
	p := ingest.NewPipeline(st, ingest.NewFilter(nil, nil), &queueFake{}, &healthFake{}, slog.Default())
	a := history("g@g.us", "A", 100)
	b := history("g@g.us", "B", 50)
	_, _, _ = p.ImportHistory(t.Context(), a)
	_, _, _ = p.ImportHistory(t.Context(), b)
	coverage, err := st.HistoryCoverage(t.Context())
	if err != nil || coverage.Chunks != 2 {
		t.Fatalf("coverage=%+v err=%v", coverage, err)
	}
}
func TestEmptyOrPartialChunkDoesNotPanic(t *testing.T) {
	st := testkit.Store(t)
	p := ingest.NewPipeline(st, ingest.NewFilter(nil, nil), &queueFake{}, &healthFake{}, slog.Default())
	stored, skipped, err := p.ImportHistory(t.Context(), &events.HistorySync{Data: &waHistorySync.HistorySync{Conversations: []*waHistorySync.Conversation{nil, {Messages: []*waHistorySync.HistorySyncMsg{{}}}}}})
	if err != nil || stored != 0 || skipped != 1 {
		t.Fatalf("stored=%d skipped=%d err=%v", stored, skipped, err)
	}
}
func history(chat, id string, progress uint32) *events.HistorySync {
	return &events.HistorySync{Data: &waHistorySync.HistorySync{
		Progress: proto.Uint32(progress),
		Conversations: []*waHistorySync.Conversation{{
			ID: proto.String(chat),
			Messages: []*waHistorySync.HistorySyncMsg{{Message: &waWeb.WebMessageInfo{
				Key:              &waCommon.MessageKey{RemoteJID: proto.String(chat), ID: proto.String(id)},
				MessageTimestamp: proto.Uint64(10), Message: text(id),
			}}},
		}},
	}}
}
