package ingest_test

import (
	"io"
	"log/slog"
	"testing"

	"go.mau.fi/whatsmeow/types/events"
	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/testkit"
)

type wakingQueue struct {
	queueFake
	wakes int
}

func (q *wakingQueue) Wake() { q.wakes++ }

func TestConnectedWakesPausedMediaQueue(t *testing.T) {
	q := &wakingQueue{}
	p := ingest.NewPipeline(testkit.Store(t), ingest.NewFilter(nil, nil), q, &healthFake{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.Handle(&events.Connected{})
	if q.wakes != 1 {
		t.Fatalf("media wakes after Connected = %d, want 1", q.wakes)
	}
}
