package ingest_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"google.golang.org/protobuf/proto"
	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

// fakeTime is the injected clock: sleep records the requested duration and advances the clock.
type fakeTime struct {
	clock  testkit.Clock
	sleeps []time.Duration
	onTick func()
}

func newFakeTime() *fakeTime {
	return &fakeTime{clock: testkit.Clock{Current: time.Unix(1_700_000_000, 0)}}
}
func (f *fakeTime) sleep(d time.Duration) {
	f.sleeps = append(f.sleeps, d)
	f.clock.Advance(d)
	if f.onTick != nil {
		f.onTick()
	}
}

// flakyWriter wraps the real store and fails the first `failures` calls of one method.
type flakyWriter struct {
	*store.Store
	method   string
	failures int
	calls    map[string]int
}

func newFlakyWriter(st *store.Store, method string, failures int) *flakyWriter {
	return &flakyWriter{Store: st, method: method, failures: failures, calls: map[string]int{}}
}
func (w *flakyWriter) fail(method string) error {
	w.calls[method]++
	if method == w.method && w.failures > 0 {
		w.failures--
		return errors.New("database is locked")
	}
	return nil
}
func (w *flakyWriter) InsertIDMapping(ctx context.Context, m store.IDMapping) (bool, error) {
	if err := w.fail("InsertIDMapping"); err != nil {
		return false, err
	}
	return w.Store.InsertIDMapping(ctx, m)
}
func (w *flakyWriter) InsertMessage(ctx context.Context, m store.Message) (bool, error) {
	if err := w.fail("InsertMessage"); err != nil {
		return false, err
	}
	return w.Store.InsertMessage(ctx, m)
}
func (w *flakyWriter) UpsertChat(ctx context.Context, c store.Chat) error {
	if err := w.fail("UpsertChat"); err != nil {
		return err
	}
	return w.Store.UpsertChat(ctx, c)
}
func (w *flakyWriter) InsertHistoryChunk(ctx context.Context, h store.HistoryChunk) error {
	if err := w.fail("InsertHistoryChunk"); err != nil {
		return err
	}
	return w.Store.InsertHistoryChunk(ctx, h)
}

// flakyQueue fails the first `failures` Enqueue calls, then delegates to a recording queue.
type flakyQueue struct {
	queueFake
	failures, calls int
}

func (q *flakyQueue) Enqueue(ctx context.Context, m store.Media) error {
	q.calls++
	if q.failures > 0 {
		q.failures--
		return errors.New("database is locked")
	}
	return q.queueFake.Enqueue(ctx, m)
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newPipeline(w ingest.Writer, q ingest.MediaQueue, h *healthFake, ft *fakeTime) *ingest.Pipeline {
	return ingest.NewPipeline(w, ingest.NewFilter(nil, nil), q, h, quiet()).WithClock(ft.clock.Now, ft.sleep)
}

func messageCount(t *testing.T, st *store.Store) int {
	t.Helper()
	stats, err := st.ChatStats(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, s := range stats {
		n += s.MessageCount
	}
	return n
}

// B1: write failures never drop an event. The retry.Do budget (5) is exhausted, WriteFailed fires once,
// and the write keeps being retried until it succeeds.
func TestHandleWriteFailureRetriesUntilStored(t *testing.T) {
	st := testkit.Store(t)
	w := newFlakyWriter(st, "InsertMessage", 7)
	h := &healthFake{}
	p := newPipeline(w, &queueFake{}, h, newFakeTime())
	p.Handle(messageEvent("g@g.us", "A", text("important")))
	if n := messageCount(t, st); n != 1 {
		t.Fatalf("messages stored=%d", n)
	}
	if h.failed != 1 || h.seen != 1 || w.calls["InsertMessage"] != 8 {
		t.Fatalf("WriteFailed=%d seen=%d insert calls=%d", h.failed, h.seen, w.calls["InsertMessage"])
	}
}

func TestMappingWriteFailureIsRetried(t *testing.T) {
	st := testkit.Store(t)
	w := newFlakyWriter(st, "InsertIDMapping", 7)
	h := &healthFake{}
	p := newPipeline(w, &queueFake{}, h, newFakeTime())
	e := messageEvent("123@lid", "A", text("important"))
	e.Info.RecipientAlt = jid("9665@s.whatsapp.net")
	p.Handle(e)
	aliases, err := st.Aliases(t.Context(), "123@lid")
	if err != nil || len(aliases) != 1 {
		t.Fatalf("aliases=%v err=%v", aliases, err)
	}
	if n := messageCount(t, st); n != 1 || h.failed != 1 {
		t.Fatalf("messages=%d WriteFailed=%d", n, h.failed)
	}
}

func TestEnqueueFailureIsRetried(t *testing.T) {
	st := testkit.Store(t)
	q := &flakyQueue{failures: 7}
	h := &healthFake{}
	p := newPipeline(st, q, h, newFakeTime())
	image := &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Mimetype: proto.String("image/jpeg"), FileLength: proto.Uint64(10)}}
	p.Handle(messageEvent("g@g.us", "IMG", image))
	if len(q.items) != 1 || q.calls != 8 || h.failed != 1 || h.seen != 1 {
		t.Fatalf("queued=%d enqueue calls=%d WriteFailed=%d seen=%d", len(q.items), q.calls, h.failed, h.seen)
	}
}

func TestImportHistoryRetriesLedgerWrite(t *testing.T) {
	st := testkit.Store(t)
	w := newFlakyWriter(st, "InsertHistoryChunk", 7)
	h := &healthFake{}
	p := newPipeline(w, &queueFake{}, h, newFakeTime())
	stored, _, err := p.ImportHistory(t.Context(), history("g@g.us", "A", 5))
	if err != nil || stored != 1 {
		t.Fatalf("stored=%d err=%v", stored, err)
	}
	coverage, err := st.HistoryCoverage(t.Context())
	if err != nil || coverage.Chunks != 1 || h.failed != 1 || w.calls["InsertHistoryChunk"] != 8 {
		t.Fatalf("coverage=%+v err=%v WriteFailed=%d ledger calls=%d", coverage, err, h.failed, w.calls["InsertHistoryChunk"])
	}
}

func TestImportHistoryRetriesMappingAndChatWrites(t *testing.T) {
	for _, method := range []string{"InsertIDMapping", "UpsertChat"} {
		t.Run(method, func(t *testing.T) {
			st := testkit.Store(t)
			w := newFlakyWriter(st, method, 3)
			h := &healthFake{}
			p := newPipeline(w, &queueFake{}, h, newFakeTime())
			hs := history("g@g.us", "A", 5)
			hs.Data.Conversations[0].Name = proto.String("Study Group")
			hs.Data.PhoneNumberToLidMappings = []*waHistorySync.PhoneNumberToLIDMapping{{LidJID: proto.String("1@lid"), PnJID: proto.String("2@s.whatsapp.net")}}
			stored, _, err := p.ImportHistory(t.Context(), hs)
			if err != nil || stored != 1 {
				t.Fatalf("stored=%d err=%v", stored, err)
			}
			aliases, _ := st.Aliases(t.Context(), "1@lid")
			if len(aliases) != 1 || w.calls[method] < 4 {
				t.Fatalf("aliases=%v %s calls=%d", aliases, method, w.calls[method])
			}
		})
	}
}

// Handle must not swallow errors: a history import into a writer without a ledger fails and must be logged.
func TestHandleLogsIngestErrors(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	p := ingest.NewPipeline(struct{ ingest.Writer }{testkit.Store(t)}, ingest.NewFilter(nil, nil), &queueFake{}, &healthFake{}, log)
	p.Handle(history("g@g.us", "A", 5))
	out := buf.String()
	if !strings.Contains(out, "level=ERROR") || !strings.Contains(out, "events.HistorySync") || !strings.Contains(out, "ledger") {
		t.Fatalf("log=%q", out)
	}
}
