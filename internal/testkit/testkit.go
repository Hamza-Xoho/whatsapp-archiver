package testkit

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"whatsapp-archiver/internal/store"
)

func Store(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "archive.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}
func Message(chat, id string, ts time.Time) store.Message {
	return store.Message{ChatJID: chat, ID: id, SenderJID: "sender@lid", Timestamp: ts.Unix(), ReceivedAt: ts.Unix(), Source: "live", Text: id, RawProto: []byte(id)}
}

// InsertMedia puts a media row in the archive the way a test needs it, without going through the pipeline. A row with a
// RawProto also gets the message row the downloader reads it from (R4: the raw proto is not a media column); a row
// without one is queued alone, which Apply allows.
func InsertMedia(ctx context.Context, st *store.Store, m store.Media) error {
	b := store.Batch{Media: &m}
	if len(m.RawProto) > 0 {
		b.Message = &store.Message{ChatJID: m.ChatJID, ID: m.MessageID, SenderJID: m.SenderJID, FromMe: m.FromMe, Kind: m.Type, Source: m.Source,
			Timestamp: m.MessageTimestamp, ReceivedAt: m.MessageTimestamp, RawProto: m.RawProto}
		if !store.ValidMessageKind(b.Message.Kind) {
			b.Message.Kind = "unknown"
		}
		if b.Message.Source == "" {
			b.Message.Source = "live"
		}
	}
	_, err := st.Apply(ctx, b)
	return err
}

type Clock struct{ Current time.Time }

func (c *Clock) Now() time.Time          { return c.Current }
func (c *Clock) Advance(d time.Duration) { c.Current = c.Current.Add(d) }
