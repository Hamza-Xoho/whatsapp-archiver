package web

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"whatsapp-archiver/internal/store"
)

func TestJSONDateScopeHasOnlyTopLevelInRangeEvents(t *testing.T) {
	u, chat := exportUI(t)
	outside := time.Date(2026, 3, 9, 5, 0, 0, 0, time.UTC).Unix()
	if _, err := u.st.InsertEvent(t.Context(), store.MessageEvent{ChatJID: chat, TargetID: "a", Kind: "edit", EventID: "outside", NewText: "OUTSIDE-EVENT", Timestamp: outside}); err != nil {
		t.Fatal(err)
	}
	w := exportGet(t, u.handler(), "/chats/"+chat+"/export/download?format=json&from=2026-03-08&to=2026-03-08")
	var doc struct {
		Messages      []map[string]any `json:"messages"`
		MessageEvents []map[string]any `json:"message_events"`
		Counts        map[string]int   `json:"counts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	for _, m := range doc.Messages {
		if _, ok := m["events"]; ok {
			t.Fatalf("message duplicated nested events: %+v", m)
		}
	}
	for _, e := range doc.MessageEvents {
		if e["event_id"] == "outside" {
			t.Fatal("outside-range event leaked")
		}
	}
	if doc.Counts["message_events"] != 3 || len(doc.MessageEvents) != 3 {
		t.Fatalf("event counts=%v events=%v", doc.Counts, doc.MessageEvents)
	}
	if strings.Contains(w.Body.String(), "OUTSIDE-EVENT") {
		t.Fatal("outside event text leaked")
	}
}

func TestSingleContactDisplayNameRenders(t *testing.T) {
	u, chat := exportUI(t)
	m := store.Message{ChatJID: chat, ID: "contact", Kind: "contact", Details: `{"display_name":"Alice Contact","vcard":"BEGIN:VCARD"}`, Source: "live", Timestamp: time.Now().Unix(), ReceivedAt: 4, RawProto: []byte("x")}
	if _, err := u.st.InsertMessage(t.Context(), m); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"txt", "html"} {
		w := exportGet(t, u.handler(), "/chats/"+chat+"/export/download?format="+f)
		if !strings.Contains(w.Body.String(), "Alice Contact") {
			t.Errorf("%s lacks single contact name: %s", f, w.Body.String())
		}
	}
}

func TestArchiveMediaNameIsStatelessReservedAndCollisionSafe(t *testing.T) {
	seen := map[string]bool{"_chat.txt": true, "missing-media.txt": true}
	for i := range 10_000 {
		name := archiveMediaName("g@g.us", string(rune(i+1)), "a_2.txt")
		if seen[name] || strings.Contains(name, "..") || strings.ContainsAny(name, "/\\") {
			t.Fatalf("unsafe/colliding archive name %q at %d", name, i)
		}
		seen[name] = true
	}
	if a, b := archiveMediaName("g@g.us", "one", "a.txt"), archiveMediaName("g@g.us", "two", "a.txt"); a == b {
		t.Fatalf("duplicate names collide: %q", a)
	}
}

func TestMissingDoneFileManifestSaysMissing(t *testing.T) {
	u, chat := exportUI(t)
	m := store.Message{ChatJID: chat, ID: "gone", Kind: "document", Source: "live", Timestamp: time.Now().Unix(), ReceivedAt: 5, RawProto: []byte("x")}
	if _, err := u.st.InsertMessage(t.Context(), m); err != nil {
		t.Fatal(err)
	}
	if _, err := u.st.Apply(t.Context(), store.Batch{Media: &store.Media{ChatJID: chat, MessageID: "gone", Type: "document", Filename: "gone.txt", Path: "/does/not/exist", State: "done"}}); err != nil {
		t.Fatal(err)
	}
	w := exportGet(t, u.handler(), "/chats/"+chat+"/export/download?format=zip")
	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if f.Name == "missing-media.txt" {
			r, _ := f.Open()
			b, _ := io.ReadAll(r)
			_ = r.Close()
			if strings.Contains(string(b), "\tdone") || !strings.Contains(string(b), "missing") {
				t.Fatalf("manifest=%s", b)
			}
			return
		}
	}
	t.Fatal("manifest absent")
}

type cancelCopyWriter struct{ cancel context.CancelFunc }

func (w cancelCopyWriter) Write(p []byte) (int, error) { w.cancel(); return len(p), nil }
func TestContextCopyStopsLargeMediaAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	src := bytes.NewReader(make([]byte, 1<<20))
	err := copyContext(ctx, cancelCopyWriter{cancel}, src)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("copyContext=%v", err)
	}
}
