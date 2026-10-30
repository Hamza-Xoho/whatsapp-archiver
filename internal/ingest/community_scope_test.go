package ingest_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types/events"
	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/store"
)

type communityWriter struct{ writes int }

func (w *communityWriter) Apply(context.Context, store.Batch) (store.Applied, error) {
	w.writes++
	return store.Applied{New: true}, nil
}
func (*communityWriter) InsertIDMapping(context.Context, store.IDMapping) (bool, error) {
	return true, nil
}

type communityQueue struct{}

func (communityQueue) Decide(context.Context, *store.Media) error  { return nil }
func (communityQueue) Wake()                                       {}
func (communityQueue) OnRetry(context.Context, *events.MediaRetry) {}

type communityHealth struct{}

func (communityHealth) Seen(time.Time, bool)    {}
func (communityHealth) SetState(string, string) {}
func (communityHealth) WriteFailed(error)       {}
func (communityHealth) Panic(string)            {}

func TestFutureCommunityChildIsExcludedBeforeItsFirstWrite(t *testing.T) {
	rules := ingest.NewRuleSet([]store.ChatRule{{JID: "community@g.us", Logging: false}})
	w := &communityWriter{}
	p := ingest.NewPipeline(w, ingest.NewFilter(rules, nil), communityQueue{}, communityHealth{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r := ingest.Result{Kind: ingest.KindMessage, Chat: store.Chat{JID: "future@g.us", Type: "group", ParentJID: "community@g.us"},
		Message: &store.Message{ChatJID: "future@g.us", ID: "first", Kind: "text", Source: "live", RawProto: []byte("x")}, Received: time.Unix(1, 0)}
	if err := p.Ingest(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	if w.writes != 0 {
		t.Fatalf("future child wrote %d batches before inheriting the stopped community; want 0", w.writes)
	}
}
