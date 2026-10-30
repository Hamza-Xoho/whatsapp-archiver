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
	"time"

	"whatsapp-archiver/internal/store"
)

func TestHTMLExportShowsFullProvenanceAndAbsoluteLiveLinks(t *testing.T) {
	u, chat := exportUI(t)
	missing := store.Message{ChatJID: chat, ID: "missing-html", Kind: "document", Source: "live", Timestamp: time.Date(2026, 3, 8, 8, 0, 0, 0, time.UTC).Unix(), ReceivedAt: 8, RawProto: []byte("x")}
	if _, err := u.st.InsertMessage(t.Context(), missing); err != nil {
		t.Fatal(err)
	}
	if _, err := u.st.Apply(t.Context(), store.Batch{Media: &store.Media{ChatJID: chat, MessageID: missing.ID, Type: "document", MIME: "application/pdf", Filename: "missing.pdf", Path: filepath.Join(u.cfg.DataDir, "media", "absent"), State: "done", MessageTimestamp: missing.Timestamp}}); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/chats/"+chat+"/export/download?format=html&from=2026-03-08&to=2026-03-08", nil)
	r.Host = "localhost:8080"
	r.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	u.handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("HTML export=%d %s", w.Code, w.Body.String())
	}
	html := w.Body.String()
	for _, want := range []string{
		"Aliases included:",
		"Applied range: 2026-03-08 to 2026-03-08",
		"Messages: 3",
		"Message events: 3",
		"Chat events: 0",
		`href="https://localhost:8080/media?`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("HTML export lacks %q: %s", want, html)
		}
	}
}

func TestZIPBoundsUntrustedMediaNames(t *testing.T) {
	u, chat := exportUI(t)
	mediaDir := filepath.Join(u.cfg.DataDir, "media")
	path := filepath.Join(mediaDir, "long-name-content")
	if err := os.WriteFile(path, []byte("LONG-NAME-CONTENT"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Keep the message textual so inserting the adversarial archive name does not also build a 70,000-byte
	// document-filename FTS entry; the export path associates media independently of message kind.
	m := store.Message{ChatJID: chat, ID: "long-name", Kind: "text", Source: "live", Timestamp: time.Now().Unix(), ReceivedAt: 9, RawProto: []byte("x")}
	if _, err := u.st.InsertMessage(t.Context(), m); err != nil {
		t.Fatal(err)
	}
	filename := strings.Repeat("a", 70_000) + ".txt"
	if _, err := u.st.Apply(t.Context(), store.Batch{Media: &store.Media{ChatJID: chat, MessageID: m.ID, Type: "document", MIME: "text/plain", Filename: filename, Path: path, State: "done", MessageTimestamp: m.Timestamp}}); err != nil {
		t.Fatal(err)
	}
	w := exportGet(t, u.handler(), "/chats/"+chat+"/export/download?format=zip")
	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatalf("long media filename aborted ZIP: %v", err)
	}
	found := false
	for _, f := range zr.File {
		if len(f.Name) > 240 {
			t.Errorf("ZIP name has %d bytes: %q", len(f.Name), f.Name)
		}
		r, openErr := f.Open()
		if openErr != nil {
			t.Fatal(openErr)
		}
		b, readErr := io.ReadAll(r)
		_ = r.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		found = found || string(b) == "LONG-NAME-CONTENT"
	}
	if !found {
		t.Fatal("bounded long-name media is absent from ZIP")
	}
}

func TestExportFailureUsesSpecifiedRecoverableCopy(t *testing.T) {
	u, chat := exportUI(t)
	if err := u.st.Close(); err != nil {
		t.Fatal(err)
	}
	w := exportGet(t, u.handler(), "/chats/"+chat+"/export/download?format=json")
	if w.Code != http.StatusInternalServerError || strings.TrimSpace(w.Body.String()) != "The chat could not be exported. Try again." {
		t.Fatalf("export failure=%d %q", w.Code, w.Body.String())
	}
}
