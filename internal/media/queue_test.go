package media

import (
	"context"
	"go.mau.fi/whatsmeow/types/events"
	"io"
	"log/slog"
	"testing"
	"time"
	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
	"whatsapp-archiver/internal/wa"
)

type downFake struct {
	calls int
	data  []byte
	err   error
}

func (d *downFake) Download(context.Context, []byte, string) ([]byte, error) {
	d.calls++
	return d.data, d.err
}
func (d *downFake) RequestMediaRetry(context.Context, wa.MediaRef, []byte) error { return nil }
func (d *downFake) ApplyMediaRetry(*events.MediaRetry, []byte) (string, error)   { return "new", nil }

type fakeClock struct{ now time.Time }

func (c fakeClock) Now() time.Time { return c.now }
func TestDocumentDownloads(t *testing.T) {
	st := testkit.Store(t)
	d := &downFake{data: []byte("file")}
	q := NewQueue(t.TempDir(), config.MediaRules{Download: []string{"document"}, Workers: 1}, st, d, fakeClock{time.Unix(1, 0)}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err := q.Enqueue(t.Context(), store.Media{ChatJID: "g@g.us", MessageID: "A", Type: "document", MIME: "application/pdf", State: "pending"}); err != nil {
		t.Fatal(err)
	}
	stop := start(t, q)
	eventually(t, "document done", func() bool { return row(t, st, "g@g.us", "A").State == "done" })
	stop() // waits for Run to return, so reading the fake afterwards is race-free
	job, _ := st.MediaByID(t.Context(), "g@g.us", "A")
	if job.State != "done" || d.calls != 1 {
		t.Fatalf("job=%+v calls=%d", job, d.calls)
	}
}
