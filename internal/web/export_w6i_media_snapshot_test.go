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
	"sync"
	"testing"

	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

type transcriptBlockingWriter struct {
	header           http.Header
	body             bytes.Buffer
	status           int
	started, release chan struct{}
	once             sync.Once
}

func (w *transcriptBlockingWriter) Header() http.Header    { return w.header }
func (w *transcriptBlockingWriter) WriteHeader(status int) { w.status = status }
func (w *transcriptBlockingWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.body.Write(p)
	if bytes.Contains(w.body.Bytes(), []byte("_chat.txt")) {
		w.once.Do(func() {
			close(w.started)
			<-w.release
		})
	}
	return n, err
}

func TestZIPMediaSnapshotSurvivesConcurrentStateAndFileChanges(t *testing.T) {
	for _, tc := range []struct {
		name, initial, changed string
		deleteFile             bool
		wantManifest, wantFile bool
	}{
		{name: "pending row becomes done", initial: "pending", changed: "done", wantManifest: true},
		{name: "done file disappears", initial: "done", changed: "done", deleteFile: true, wantManifest: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
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
				Path: path, State: tc.initial, MessageTimestamp: message.Timestamp, DeclaredSize: 15}
			if _, err := u.st.Apply(t.Context(), store.Batch{Media: &original}); err != nil {
				t.Fatal(err)
			}

			w := &transcriptBlockingWriter{header: make(http.Header), started: make(chan struct{}), release: make(chan struct{})}
			r := httptest.NewRequest(http.MethodGet, "/chats/"+chat+"/export/download?format=zip", nil)
			r.Host = "localhost:8080"
			done := make(chan struct{})
			go func() { u.handler().ServeHTTP(w, r); close(done) }()
			<-w.started // the manifest pass is complete; media streaming has not begun.
			if tc.deleteFile {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else {
				changed := original
				changed.State = tc.changed
				if err := u.st.UpdateMedia(t.Context(), changed); err != nil {
					t.Fatal(err)
				}
			}
			close(w.release)
			<-done

			manifest, hasFile := zipSnapshotFacts(t, w.body.Bytes())
			hasManifest := strings.Contains(manifest, id+"\t")
			if w.status != http.StatusOK || hasManifest != tc.wantManifest || hasFile != tc.wantFile {
				t.Fatalf("snapshot status/manifest/file = %d/%v/%v, want 200/%v/%v; manifest=%q",
					w.status, hasManifest, hasFile, tc.wantManifest, tc.wantFile, manifest)
			}
		})
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
