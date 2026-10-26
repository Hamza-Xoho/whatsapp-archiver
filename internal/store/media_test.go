package store_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"whatsapp-archiver/internal/store"
)

func mediaRow(t *testing.T, s *store.Store, chat, id, source string, ts int64, state string) {
	t.Helper()
	m := message(chat, id, time.Unix(ts, 0))
	m.Kind, m.Source = "image", source
	m.RawProto = []byte("proto-" + id)
	_, err := s.Apply(context.Background(), store.Batch{Message: &m, Media: &store.Media{ChatJID: chat, MessageID: id, Source: source, Type: "image", State: state, MessageTimestamp: ts}})
	if err != nil {
		t.Fatal(err)
	}
}

// Queue order: asked-for first (rank 0), live (1) before history and replay (2), newest first within a rank.
func TestClaimMediaOrdersBySourceRankThenNewest(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	mediaRow(t, s, "g@g.us", "hist-new", "history", 900, "pending")
	mediaRow(t, s, "g@g.us", "live-old", "live", 100, "pending")
	mediaRow(t, s, "g@g.us", "live-new", "live", 500, "pending")
	mediaRow(t, s, "g@g.us", "replay", "replay", 950, "pending")
	mediaRow(t, s, "g@g.us", "done", "live", 999, "done")
	var got []string
	for {
		m, err := s.ClaimMedia(ctx, time.Unix(10_000, 0))
		if err != nil {
			t.Fatal(err)
		}
		if m == nil {
			break
		}
		if m.State != "downloading" {
			t.Fatalf("claimed %s in state %s", m.MessageID, m.State)
		}
		got = append(got, m.MessageID)
	}
	want := []string{"live-new", "live-old", "replay", "hist-new"} // within rank 2 the newest message (950) goes first
	if !slices.Equal(got, want) {
		t.Fatalf("claim order = %v, want %v", got, want)
	}
}

func TestClaimMediaWaitsForNextAttemptAndCarriesTheRawProtoFromTheMessage(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	mediaRow(t, s, "g@g.us", "A", "live", 100, "pending")
	if _, err := s.Apply(ctx, store.Batch{Media: &store.Media{ChatJID: "g@g.us", MessageID: "B", Source: "live", State: "pending", NextAttemptAt: 5000, MessageTimestamp: 200}}); err != nil {
		t.Fatal(err)
	}
	m, err := s.ClaimMedia(ctx, time.Unix(1000, 0))
	if err != nil || m == nil || m.MessageID != "A" {
		t.Fatalf("claim = %+v, %v; want A", m, err)
	}
	if string(m.RawProto) != "proto-A" {
		t.Fatalf("RawProto = %q: the downloader reads messages.raw_proto (R4)", m.RawProto)
	}
	if m, err = s.ClaimMedia(ctx, time.Unix(1000, 0)); err != nil || m != nil {
		t.Fatalf("claim before B is due = %+v, %v; want nil", m, err)
	}
	if m, err = s.ClaimMedia(ctx, time.Unix(5000, 0)); err != nil || m == nil || m.MessageID != "B" || len(m.RawProto) != 0 {
		t.Fatalf("claim when B is due = %+v, %v", m, err)
	}
}

func TestUpdateMediaRecordsDoneAtAndErrorFields(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	mediaRow(t, s, "g@g.us", "A", "live", 100, "pending")
	m, err := s.MediaByID(ctx, "g@g.us", "A")
	if err != nil {
		t.Fatal(err)
	}
	m.State, m.Path, m.SHA256, m.UpdatedAt, m.DoneAt, m.HTTPStatus, m.ErrorClass = "done", "media/x.jpg", "abc", 2000, 2000, 200, ""
	if err = s.UpdateMedia(ctx, m); err != nil {
		t.Fatal(err)
	}
	got, err := s.MediaByID(ctx, "g@g.us", "A")
	if err != nil || got.State != "done" || got.DoneAt != 2000 || got.Path != "media/x.jpg" || got.SourceRank != 1 {
		t.Fatalf("media = %+v, %v", got, err)
	}
	m.State, m.ErrorClass, m.HTTPStatus = "failed", "gone", 404
	if err = s.UpdateMedia(ctx, m); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.MediaByID(ctx, "g@g.us", "A"); got.ErrorClass != "gone" || got.HTTPStatus != 404 {
		t.Fatalf("media = %+v", got)
	}
	m.ErrorClass = "gremlins"
	if err = s.UpdateMedia(ctx, m); err == nil {
		t.Fatal("an unknown error_class was accepted")
	}
}

func TestNextExpiredMediaUsesTheQueueOrderAndStaleRetriesFail(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	mediaRow(t, s, "g@g.us", "h", "history", 900, "expired")
	mediaRow(t, s, "g@g.us", "l", "live", 100, "expired")
	mediaRow(t, s, "g@g.us", "old", "live", 50, "retry_requested")
	m, err := s.NextExpiredMedia(ctx)
	if err != nil || m == nil || m.MessageID != "l" {
		t.Fatalf("next expired = %+v, %v; want the live row", m, err)
	}
	if err = s.FailStaleRetries(ctx, time.Unix(24*3600+1, 0)); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.MediaByID(ctx, "g@g.us", "old"); got.State != "failed" || got.Reason != "no re-upload" {
		t.Fatalf("stale retry = %+v", got)
	}
}

func TestResetMediaOnStartRequeuesDownloads(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	mediaRow(t, s, "g@g.us", "A", "live", 100, "downloading")
	if err := s.ResetMediaOnStart(ctx, time.Unix(10, 0)); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.MediaByID(ctx, "g@g.us", "A"); got.State != "pending" {
		t.Fatalf("state = %s, want pending", got.State)
	}
}
