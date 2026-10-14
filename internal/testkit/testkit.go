package testkit

import (
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

type Clock struct{ Current time.Time }

func (c *Clock) Now() time.Time          { return c.Current }
func (c *Clock) Advance(d time.Duration) { c.Current = c.Current.Add(d) }
