package media

import (
	"testing"

	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

// Task 7: Enqueue only records the row and returns; a sticker and a 150 MB video are stored as skipped with their
// reason, and no download is ever made for them, while a document goes through.
func TestEnqueueRecordsDecisionWithoutDownloading(t *testing.T) {
	st := testkit.Store(t)
	d := &fakeDown{}
	rules := config.MediaRules{Download: []string{"document", "video"}, Skip: []string{"sticker", "gif"}, VideoMaxMB: 100, Workers: 1}
	q := newTestQueue(t, st, d, rules, newClock(), nil)
	enqueue(t, q, store.Media{ChatJID: "g@g.us", MessageID: "STICK", Type: "sticker", RawProto: []byte("STICK")})
	enqueue(t, q, store.Media{ChatJID: "g@g.us", MessageID: "BIG", Type: "video", DeclaredSize: 150 << 20, RawProto: []byte("BIG")})
	enqueue(t, q, store.Media{ChatJID: "g@g.us", MessageID: "DOC", Type: "document", RawProto: []byte("DOC")})
	if n := len(d.calls()); n != 0 {
		t.Fatalf("Enqueue downloaded %d times before any worker ran", n)
	}
	if s, b, doc := row(t, st, "g@g.us", "STICK"), row(t, st, "g@g.us", "BIG"), row(t, st, "g@g.us", "DOC"); s.State != "skipped" || s.Reason != "type skipped" || b.State != "skipped" || b.Reason != "over 100 MB cap" || doc.State != "pending" {
		t.Fatalf("sticker=%+v video=%+v document=%+v", s, b, doc)
	}
	stop := start(t, q)
	eventually(t, "document done", func() bool { return row(t, st, "g@g.us", "DOC").State == "done" })
	stop()
	if calls := d.calls(); len(calls) != 1 || calls[0] != "DOC" {
		t.Fatalf("downloads = %v, want only DOC", calls)
	}
}
