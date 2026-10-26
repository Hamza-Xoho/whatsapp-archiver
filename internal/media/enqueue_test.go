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

// V1 normalises a push-to-talk audio message to kind and media type "voice". The media rules still speak of "audio"
// (config.MediaTypes), so a voice note follows the audio rules: nothing a user configured changes.
func TestVoiceNoteFollowsTheAudioRules(t *testing.T) {
	audio := config.MediaRules{Download: []string{"audio"}}
	if state, _ := decide(store.Media{Type: "voice"}, audio); state != "pending" {
		t.Fatalf("voice note with audio downloads on: %s", state)
	}
	skip := config.MediaRules{Download: []string{"audio", "image"}, Skip: []string{"audio"}}
	if state, reason := decide(store.Media{Type: "voice"}, skip); state != "skipped" || reason != "type skipped" {
		t.Fatalf("voice note with audio skipped: %s %q", state, reason)
	}
	if state, _ := decide(store.Media{Type: "voice"}, config.MediaRules{Download: []string{"image"}}); state != "skipped" {
		t.Fatalf("voice note with audio not enabled: %s", state)
	}
}

// Decide is the rules half of the old Enqueue: it sets state and reason, stamps the time the item was queued (SC-3 starts
// there) and writes nothing; the row itself comes from store.Apply in the same transaction as its message.
func TestDecideStampsTheDecisionAndWritesNothing(t *testing.T) {
	st := testkit.Store(t)
	clock := newClock()
	q := newTestQueue(t, st, &fakeDown{}, config.MediaRules{Download: []string{"image"}, Workers: 1}, clock, nil)
	m := store.Media{ChatJID: "g@g.us", MessageID: "A", Type: "image"}
	if err := q.Decide(t.Context(), &m); err != nil {
		t.Fatal(err)
	}
	if m.State != "pending" || m.Reason != "" || m.UpdatedAt != clock.Now().Unix() || m.EnqueuedAt != clock.Now().Unix() {
		t.Fatalf("decision = %+v", m)
	}
	if _, err := st.MediaByID(t.Context(), "g@g.us", "A"); err == nil {
		t.Fatal("Decide wrote a media row")
	}
}

// Wake is a wake-up signal only: it never blocks, however often it is called with no worker running.
func TestWakeNeverBlocks(t *testing.T) {
	q := newTestQueue(t, nil, &fakeDown{}, config.MediaRules{Workers: 1}, newClock(), nil)
	done := make(chan struct{})
	go func() {
		for range 10 {
			q.Wake()
		}
		close(done)
	}()
	<-done
}
