package ingest_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/testkit"
)

// I1: the WriteFailed alert carries no error text, so the pipeline logs the failing write's error when it reports it.
func TestReportedWriteFailureIsLoggedWithItsError(t *testing.T) {
	st := testkit.Store(t)
	var buf bytes.Buffer
	h := &healthFake{}
	ft := newFakeTime()
	p := ingest.NewPipeline(newFlakyWriter(st, "InsertMessage", 7), ingest.NewFilter(nil, nil), &queueFake{}, h, slog.New(slog.NewTextHandler(&buf, nil))).WithClock(ft.clock.Now, ft.sleep)
	p.Handle(messageEvent("g@g.us", "A", text("x")))
	out := buf.String()
	if h.failed != 1 || !strings.Contains(out, "level=ERROR") || !strings.Contains(out, "database is locked") {
		t.Fatalf("WriteFailed=%d log=%q, want one report and an ERROR line with the error", h.failed, out)
	}
}
