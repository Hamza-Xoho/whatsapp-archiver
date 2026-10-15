package main

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/store"
)

// rejectingStore is the archive with every message write rejected by a constraint.
type rejectingStore struct{ *store.Store }

func (rejectingStore) InsertMessage(context.Context, store.Message) (bool, error) {
	return false, fmt.Errorf("insert message: %w", store.ErrConstraint)
}

// panickingStore is the archive with a message write that panics (recovered by the handler guard).
type panickingStore struct{ *store.Store }

func (panickingStore) InsertMessage(context.Context, store.Message) (bool, error) { panic("boom") }

// H14 M1: a replay during which writes failed or a panic was recovered exits non-zero and says so; a short result is
// never reported as a success.
func TestReplayFailsWhenWritesFailedOrPanicked(t *testing.T) {
	for name, wrap := range map[string]func(*store.Store) ingest.Writer{
		"write failed": func(st *store.Store) ingest.Writer { return rejectingStore{st} },
		"panic":        func(st *store.Store) ingest.Writer { return panickingStore{st} },
	} {
		t.Run(name, func(t *testing.T) {
			saved := replayWriter
			replayWriter = wrap
			t.Cleanup(func() { replayWriter = saved })
			var stdout, stderr bytes.Buffer
			code := run([]string{"replay", recordFixture(t), "--data", t.TempDir()}, &stdout, &stderr)
			reported := strings.Contains(stderr.String(), "replay incomplete")
			if code != 1 || !reported || strings.Contains(stdout.String(), "replayed") {
				t.Fatalf("code=%d stdout=%q incomplete reported=%v, want exit 1 and the incomplete replay reported", code, stdout.String(), reported)
			}
		})
	}
}
