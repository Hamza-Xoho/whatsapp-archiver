package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

// H11: a panicking download is recovered, its row is marked failed with reason panic, the panic is reported once as
// "media", and the worker goes on to the next row.
func TestWorkerPanicMarksRowFailedAndIsCounted(t *testing.T) {
	d := &fakeDown{download: func(raw []byte, _ string) ([]byte, error) {
		if string(raw) == "poison" {
			panic("boom in download")
		}
		return []byte("ok"), nil
	}}
	var mu sync.Mutex
	var panics []string
	st := testkit.Store(t)
	q := newTestQueue(t, st, d, config.MediaRules{Download: []string{"image"}, Workers: 1}, newClock(), func(where string) {
		mu.Lock()
		panics = append(panics, where)
		mu.Unlock()
	})
	enqueue(t, q, store.Media{ChatJID: "g@g.us", MessageID: "P", Type: "image", MIME: "image/png", RawProto: []byte("poison"), MessageTimestamp: 100})
	enqueue(t, q, store.Media{ChatJID: "g@g.us", MessageID: "OK", Type: "image", MIME: "image/png", RawProto: []byte("fine"), MessageTimestamp: 50})
	stop := start(t, q)
	eventually(t, "poison row failed and sibling done", func() bool {
		return row(t, st, "g@g.us", "P").State == "failed" && row(t, st, "g@g.us", "OK").State == "done"
	})
	stop()
	if p := row(t, st, "g@g.us", "P"); p.Reason != "panic" {
		t.Fatalf("poison row = %+v", p)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(panics) != 1 || panics[0] != "media" {
		t.Fatalf("panics reported = %v, want [media]", panics)
	}
	poisonCalls := 0
	for _, raw := range d.calls() {
		if raw == "poison" {
			poisonCalls++
		}
	}
	if poisonCalls != 1 {
		t.Fatalf("poison downloaded %d times, want 1 (a failed row is not claimed again)", poisonCalls)
	}
}

// Rows left pending or downloading by a previous run are downloaded after a restart (Task 7).
func TestPendingAndDownloadingRowsRecoverAfterRestart(t *testing.T) {
	st := testkit.Store(t)
	for id, state := range map[string]string{"PEND": "pending", "DOWN": "downloading"} {
		if err := st.InsertMedia(t.Context(), store.Media{ChatJID: "g@g.us", MessageID: id, Type: "document", State: state, RawProto: []byte(id)}); err != nil {
			t.Fatal(err)
		}
	}
	q := newTestQueue(t, st, &fakeDown{}, config.MediaRules{Download: []string{"document"}, Workers: 1}, newClock(), nil)
	stop := start(t, q)
	eventually(t, "both rows done", func() bool {
		return row(t, st, "g@g.us", "PEND").State == "done" && row(t, st, "g@g.us", "DOWN").State == "done"
	})
	stop()
}

// A document lands at the layout path with its sha256; while it downloads its row is claimed as downloading.
func TestMediaMetadataRecorded(t *testing.T) {
	st := testkit.Store(t)
	var during string
	d := &fakeDown{}
	d.download = func([]byte, string) ([]byte, error) {
		m, _ := st.MediaByID(context.Background(), "123@g.us", "DOC1")
		during = m.State
		return []byte("pdf bytes"), nil
	}
	q := newTestQueue(t, st, d, config.MediaRules{Download: []string{"document"}, Workers: 1}, newClock(), nil)
	ts := time.Date(2023, 11, 14, 12, 0, 0, 0, time.UTC).Unix()
	enqueue(t, q, store.Media{ChatJID: "123@g.us", MessageID: "DOC1", Type: "document", MIME: "application/pdf", RawProto: []byte("x"), MessageTimestamp: ts})
	stop := start(t, q)
	eventually(t, "document done", func() bool { return row(t, st, "123@g.us", "DOC1").State == "done" })
	stop()
	m := row(t, st, "123@g.us", "DOC1")
	want := filepath.Join(q.dir, "media", "123@g.us", "2023-11", "DOC1.pdf")
	sum := sha256.Sum256([]byte("pdf bytes"))
	if m.Path != want || m.SHA256 != hex.EncodeToString(sum[:]) || m.Reason != "" {
		t.Fatalf("row = %+v, want path %s", m, want)
	}
	if data, err := os.ReadFile(want); err != nil || string(data) != "pdf bytes" {
		t.Fatalf("file %q err=%v", data, err)
	}
	if during != "downloading" {
		t.Fatalf("state while downloading = %q", during)
	}
}

// SC-3 under backlog: a live item enqueued after 100 history items is downloaded first.
func TestLiveMediaBeforeHistoryBacklog(t *testing.T) {
	st := testkit.Store(t)
	d := &fakeDown{}
	q := newTestQueue(t, st, d, config.MediaRules{Download: []string{"image"}, Workers: 1}, newClock(), nil)
	for i := 0; i < 100; i++ {
		enqueue(t, q, store.Media{ChatJID: "g@g.us", MessageID: fmt.Sprintf("H%03d", i), Source: "history", Type: "image", RawProto: []byte(fmt.Sprintf("H%03d", i)), MessageTimestamp: int64(1000 + i)})
	}
	enqueue(t, q, store.Media{ChatJID: "g@g.us", MessageID: "LIVE", Source: "live", Type: "image", RawProto: []byte("LIVE"), MessageTimestamp: 10})
	stop := start(t, q)
	eventually(t, "live item downloaded", func() bool { return row(t, st, "g@g.us", "LIVE").State == "done" })
	stop()
	if calls := d.calls(); len(calls) == 0 || calls[0] != "LIVE" {
		t.Fatalf("first downloads = %v", calls[:min(3, len(calls))])
	}
}
