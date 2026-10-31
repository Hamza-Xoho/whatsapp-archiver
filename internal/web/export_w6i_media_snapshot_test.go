package web

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

func TestZIPMediaSnapshotSurvivesConcurrentStateAndFileChanges(t *testing.T) {
	u := newChatsUI(t)
	chat, id := "snapshot@g.us", "mutable-media"
	message := testkit.Message(chat, id, dashNow)
	if _, err := u.st.InsertMessage(t.Context(), message); err != nil {
		t.Fatal(err)
	}
	mediaDir := filepath.Join(u.cfg.DataDir, "media")
	if err := os.MkdirAll(mediaDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(mediaDir, "artifact.bin")
	if err := os.WriteFile(path, []byte("frozen artifact"), 0o600); err != nil {
		t.Fatal(err)
	}
	original := store.Media{ChatJID: chat, MessageID: id, Source: "live", Type: "document", Filename: "artifact.bin",
		Path: path, State: "pending", MessageTimestamp: message.Timestamp, DeclaredSize: 15}
	if _, err := u.st.Apply(t.Context(), store.Batch{Media: &original}); err != nil {
		t.Fatal(err)
	}

	w := &blockingExportWriter{header: make(http.Header), started: make(chan struct{}), release: make(chan struct{})}
	r := httptest.NewRequest(http.MethodGet, "/chats/"+chat+"/export/download?format=zip", nil)
	r.Host = "localhost:8080"
	done := make(chan struct{})
	go func() { u.handler().ServeHTTP(w, r); close(done) }()
	<-w.started // validation captured pending; no export media query has run.
	changed := original
	changed.State = "done"
	if err := u.st.UpdateMedia(t.Context(), changed); err != nil {
		t.Fatal(err)
	}
	close(w.release)
	<-done

	manifest, hasFile := zipSnapshotFacts(t, w.body.Bytes())
	if w.status != http.StatusOK || !strings.Contains(manifest, id+"\tartifact.bin\tpending") || hasFile {
		t.Fatalf("snapshot status/manifest/file = %d/%q/%v, want initial pending row only", w.status, manifest, hasFile)
	}
}

func zipSnapshotFacts(t *testing.T, body []byte) (string, bool) {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	var manifest string
	hasFile := false
	for _, f := range zr.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(r)
		_ = r.Close()
		if err != nil {
			t.Fatal(err)
		}
		switch f.Name {
		case "missing-media.txt":
			manifest = string(b)
		case "artifact.bin":
			hasFile = string(b) == "frozen artifact"
		}
	}
	return manifest, hasFile
}
