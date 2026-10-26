package ingest_test

import (
	"context"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

// orderWriter records the order of the writer's calls and the batches it was given.
type orderWriter struct {
	*store.Store
	log     *[]string
	batches []store.Batch
}

func (w *orderWriter) Apply(ctx context.Context, b store.Batch) (store.Applied, error) {
	*w.log = append(*w.log, "apply")
	w.batches = append(w.batches, b)
	return w.Store.Apply(ctx, b)
}
func (w *orderWriter) InsertIDMapping(ctx context.Context, m store.IDMapping) (bool, error) {
	*w.log = append(*w.log, "mapping")
	return w.Store.InsertIDMapping(ctx, m)
}

// orderQueue records Decide and Wake in the same log and skips every item it is asked about.
type orderQueue struct {
	queueFake
	log *[]string
}

func (q *orderQueue) Decide(ctx context.Context, m *store.Media) error {
	*q.log = append(*q.log, "decide")
	m.State, m.Reason = "skipped", "type skipped"
	return q.queueFake.Decide(ctx, m)
}
func (q *orderQueue) Wake() { *q.log = append(*q.log, "wake") }

// R2: an event is one transaction. The id mapping is its own, earlier one (the exclusion filter must see it); the
// media rules are decided before the write; the chat, the sender, the message and its media row are one batch; the
// media workers are woken after the row exists.
func TestIngestWritesAMediaMessageAsOneBatchInTheDocumentedOrder(t *testing.T) {
	st := testkit.Store(t)
	var log []string
	w := &orderWriter{Store: st, log: &log}
	q := &orderQueue{log: &log}
	p := newPipeline(w, q, &healthFake{}, newFakeTime())
	image := &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Mimetype: proto.String("image/jpeg"), Caption: proto.String("look"), FileLength: proto.Uint64(10)}}
	e := messageEvent("123@lid", "IMG", image)
	e.Info.PushName = "Alice"
	e.Info.RecipientAlt = jid("9665@s.whatsapp.net")
	p.Handle(e)
	if want := "mapping decide apply wake"; join(log) != want {
		t.Fatalf("call order = %q, want %q", join(log), want)
	}
	if len(w.batches) != 1 {
		t.Fatalf("%d batches, want 1", len(w.batches))
	}
	b := w.batches[0]
	if b.Chat == nil || b.Sender == nil || b.Message == nil || b.Media == nil || b.Event != nil || b.ChatEvent != nil {
		t.Fatalf("batch = %+v, want chat, sender, message and media", b)
	}
	if b.Sender.PushName != "Alice" || b.Message.Kind != "image" || b.Message.Caption != "look" || b.Media.Type != "image" {
		t.Fatalf("batch content = %+v / %+v / %+v", b.Sender, b.Message, b.Media)
	}
	if got := rows(t, st, "SELECT message_id,state,reason,source_rank FROM media"); len(got) != 1 || got[0] != "IMG|skipped|type skipped|1" {
		t.Fatalf("media rows = %v: the row carries the queue's decision and the live rank", got)
	}
}

// A message without media wakes nobody and decides nothing; an event is a batch with an event and no media.
func TestIngestPlainMessagesAndEventsDoNotTouchTheMediaQueue(t *testing.T) {
	st := testkit.Store(t)
	var log []string
	w := &orderWriter{Store: st, log: &log}
	p := newPipeline(w, &orderQueue{log: &log}, &healthFake{}, newFakeTime())
	p.Handle(messageEvent("g@g.us", "A", text("hello")))
	react := messageEvent("g@g.us", "R", &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{Text: proto.String("+"), Key: nil}})
	p.Handle(react)
	if join(log) != "apply apply" {
		t.Fatalf("call order = %q, want two plain applies", join(log))
	}
	if w.batches[0].Message == nil || w.batches[1].Event == nil || w.batches[1].Message != nil {
		t.Fatalf("batches = %+v", w.batches)
	}
}

func join(s []string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += " "
		}
		out += v
	}
	return out
}

// The bodyless message of the old schema becomes kind unknown; plain text is kind text; a voice note is kind voice.
func TestNormaliserMapsOldMediaTypesOntoKinds(t *testing.T) {
	cases := []struct {
		name string
		msg  *waE2E.Message
		kind string
	}{
		{"text", text("hello"), "text"},
		{"extended text", &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("link")}}, "text"},
		{"voice note", &waE2E.Message{AudioMessage: &waE2E.AudioMessage{PTT: proto.Bool(true)}}, "voice"},
		{"audio", &waE2E.Message{AudioMessage: &waE2E.AudioMessage{}}, "audio"},
		{"gif", &waE2E.Message{VideoMessage: &waE2E.VideoMessage{GifPlayback: proto.Bool(true)}}, "gif"},
		{"bodyless", &waE2E.Message{}, "unknown"},
	}
	for _, c := range cases {
		r := ingest.FromMessage(messageEvent("1@lid", "M", c.msg), "live", time.Unix(20, 0))
		if r.Message == nil || r.Message.Kind != c.kind {
			t.Errorf("%s: message = %+v, want kind %s", c.name, r.Message, c.kind)
		}
	}
}
