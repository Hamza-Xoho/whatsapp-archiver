package media

import (
	"errors"
	"strings"
	"testing"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"log/slog"
	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
	"whatsapp-archiver/internal/wa"
)

const signedErr = "download https://mmg.whatsapp.net/v/t62/a.enc?ccb=11-4&oh=01_SECRET&oe=65F0: status 403"

type applyFails struct{ *fakeDown }

func (applyFails) ApplyMediaRetry(*events.MediaRetry, []byte) (string, error) {
	return "", errors.New(signedErr)
}

func checkRedacted(t *testing.T, out string) {
	t.Helper()
	if !strings.Contains(out, "mmg.whatsapp.net/v/t62/a.enc?REDACTED") {
		t.Fatalf("no redacted URL in the journal: %q", out)
	}
	if strings.Contains(out, "SECRET") || strings.Contains(out, "ccb=") {
		t.Fatalf("signed query reached the journal: %q", out)
	}
}

func TestMediaFailureLogIsRedacted(t *testing.T) {
	rules := config.MediaRules{Download: []string{"image"}, Workers: 1}
	t.Run("retry answer not applied", func(t *testing.T) {
		st := testkit.Store(t)
		q := newTestQueue(t, st, applyFails{&fakeDown{}}, rules, newClock(), nil)
		var buf lockedBuf
		q.log = slog.New(slog.NewTextHandler(&buf, nil))
		enqueue(t, q, store.Media{ChatJID: "123@g.us", MessageID: "A", SenderJID: "5@lid", Source: "history", Type: "image", RawProto: []byte("raw")})
		chat, _ := types.ParseJID("123@g.us")
		q.OnRetry(t.Context(), &events.MediaRetry{ChatID: chat, MessageID: "A"})
		checkRedacted(t, buf.String())
	})
	t.Run("re-upload request failed", func(t *testing.T) {
		st := testkit.Store(t)
		d := &fakeDown{
			download: func([]byte, string) ([]byte, error) { return nil, wa.ErrMediaExpired },
			request:  func(wa.MediaRef, []byte) error { return errors.New(signedErr) },
		}
		q := newTestQueue(t, st, d, rules, newClock(), nil)
		var buf lockedBuf
		q.log = slog.New(slog.NewTextHandler(&buf, nil))
		enqueue(t, q, store.Media{ChatJID: "123@g.us", MessageID: "B", SenderJID: "5@lid", Source: "history", Type: "image", RawProto: []byte("raw")})
		stop := start(t, q)
		eventually(t, "request failure logged", func() bool { return strings.Contains(buf.String(), "re-upload request failed") })
		stop()
		checkRedacted(t, buf.String())
	})
}
