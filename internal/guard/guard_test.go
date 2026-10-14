package guard_test

import (
	"io"
	"log/slog"
	"testing"

	"whatsapp-archiver/internal/guard"
)

func TestGuardRecoversPanic(t *testing.T) {
	var calls int
	guard.Run(slog.New(slog.NewTextHandler(io.Discard, nil)), "handler", func(where string, value any) {
		calls++
		if where != "handler" || value != "boom" {
			t.Fatalf("where=%s value=%v", where, value)
		}
	}, func() { panic("boom") })
	if calls != 1 {
		t.Fatalf("calls=%d", calls)
	}
}
