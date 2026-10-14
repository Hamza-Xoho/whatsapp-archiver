package ingest_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

type queueFake struct{ items []store.Media }

func (q *queueFake) Enqueue(_ context.Context, m store.Media) error {
	q.items = append(q.items, m)
	return nil
}
func (q *queueFake) OnRetry(context.Context, *events.MediaRetry) {}

type healthFake struct {
	seen, failed, panics int
	state, detail        string
}

func (h *healthFake) Seen(time.Time, bool) { h.seen++ }
func (h *healthFake) SetState(s, d string) { h.state, h.detail = s, d }
func (h *healthFake) WriteFailed(error)    { h.failed++ }
func (h *healthFake) Panic(string)         { h.panics++ }

func TestHandleStoresKeptMessage(t *testing.T) {
	st := testkit.Store(t)
	h := &healthFake{}
	p := ingest.NewPipeline(st, ingest.NewFilter(nil, nil), &queueFake{}, h, slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.Handle(messageEvent("g@g.us", "A", text("hello")))
	stats, _ := st.ChatStats(t.Context())
	if len(stats) != 1 || stats[0].MessageCount != 1 || h.seen != 1 {
		t.Fatalf("stats=%+v health=%+v", stats, h)
	}
}
func TestHandleDropsExcludedChat(t *testing.T) {
	st := testkit.Store(t)
	q := &queueFake{}
	p := ingest.NewPipeline(st, ingest.NewFilter([]string{"g@g.us"}, nil), q, &healthFake{}, slog.Default())
	p.Handle(messageEvent("g@g.us", "A", text("hello")))
	stats, _ := st.ChatStats(t.Context())
	if len(stats) != 0 || len(q.items) != 0 {
		t.Fatalf("stats=%+v media=%+v", stats, q.items)
	}
}
func TestUndecryptableThenResentClosesGap(t *testing.T) {
	st := testkit.Store(t)
	p := ingest.NewPipeline(st, ingest.NewFilter(nil, nil), &queueFake{}, &healthFake{}, slog.Default())
	u := &events.UndecryptableMessage{Info: messageEvent("g@g.us", "X", text("x")).Info}
	p.Handle(u)
	if n, _ := st.OpenGapCount(t.Context()); n != 1 {
		t.Fatalf("gaps=%d", n)
	}
	p.Handle(messageEvent("g@g.us", "X", text("x")))
	if n, _ := st.OpenGapCount(t.Context()); n != 0 {
		t.Fatalf("gaps=%d", n)
	}
}
func text(value string) *waE2E.Message { return &waE2E.Message{Conversation: proto.String(value)} }

func TestConnectFailureReportsDisconnected(t *testing.T) {
	h := &healthFake{}
	p := ingest.NewPipeline(testkit.Store(t), ingest.NewFilter(nil, nil), &queueFake{}, h, slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.Handle(&events.ConnectFailure{Reason: events.ConnectFailureReason(499), Message: "odd failure"})
	if h.state != "disconnected" || h.detail != "499: unknown error odd failure" {
		t.Fatalf("state=%q detail=%q", h.state, h.detail)
	}
}
