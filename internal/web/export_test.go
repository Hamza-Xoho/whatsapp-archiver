package web

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/store"
)

func exportUI(t *testing.T) (*chatsUI, string) {
	t.Helper()
	u := newChatsUI(t)
	u.cfg = &config.Config{DataDir: t.TempDir(), TimeZone: "America/New_York"}
	chat := "family@g.us"
	if err := u.st.UpsertChat(t.Context(), store.Chat{JID: chat, Name: `Family <script>`}); err != nil {
		t.Fatal(err)
	}
	for _, m := range []store.Message{
		{ChatJID: chat, ID: "a", SenderJID: "1@lid", PushName: "Alice", Kind: "text", Text: "hello\nsecond line <script>alert(1)</script>", Source: "live", Timestamp: time.Date(2026, 3, 8, 6, 59, 0, 0, time.UTC).Unix(), ReceivedAt: 1, RawProto: []byte("DO-NOT-EXPORT")},
		{ChatJID: chat, ID: "b", SenderJID: "2@lid", PushName: "زهرة", Kind: "document", Caption: "ملف عربي", Source: "live", Timestamp: time.Date(2026, 3, 8, 7, 1, 0, 0, time.UTC).Unix(), ReceivedAt: 2, RawProto: []byte("SECRET")},
	} {
		if _, err := u.st.InsertMessage(t.Context(), m); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range []store.MessageEvent{
		{ChatJID: chat, TargetID: "a", Kind: "edit", EventID: "e", NewText: "hello edited", Timestamp: time.Date(2026, 3, 8, 7, 0, 0, 0, time.UTC).Unix()},
		{ChatJID: chat, TargetID: "a", Kind: "revoke", EventID: "r", Timestamp: time.Date(2026, 3, 8, 7, 0, 1, 0, time.UTC).Unix()},
		{ChatJID: chat, TargetID: "a", Kind: "reaction", EventID: "x", SenderJID: "2@lid", Value: "👍", Timestamp: time.Date(2026, 3, 8, 7, 0, 2, 0, time.UTC).Unix()},
	} {
		if _, err := u.st.InsertEvent(t.Context(), e); err != nil {
			t.Fatal(err)
		}
	}
	mediaDir := filepath.Join(u.cfg.DataDir, "media")
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(mediaDir, "report.txt")
	if err := os.WriteFile(path, []byte("MEDIA-CONTENT"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := u.st.Apply(t.Context(), store.Batch{Media: &store.Media{ChatJID: chat, MessageID: "b", Type: "document", MIME: "text/plain", Filename: "report.txt", Path: path, State: "done", DeclaredSize: 13}}); err != nil {
		t.Fatal(err)
	}
	return u, chat
}

func exportGet(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Host = "localhost:8080"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestExportPageAndDownloadHeaders(t *testing.T) {
	u, chat := exportUI(t)
	h := u.handler()
	if w := exportGet(t, h, "/chats/unknown/export"); w.Code != http.StatusNotFound {
		t.Fatalf("unknown = %d", w.Code)
	}
	w := exportGet(t, h, "/chats/"+chat+"/export")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "All archived messages") || !strings.Contains(w.Body.String(), "Typed JSON") {
		t.Fatalf("page = %d %s", w.Code, w.Body.String())
	}
	for format, ctype := range map[string]string{"json": "application/json", "html": "text/html", "txt": "text/plain", "zip": "application/zip"} {
		w = exportGet(t, h, "/chats/"+chat+"/export/download?format="+format)
		if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), ctype) || w.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(w.Header().Get("Content-Disposition"), "attachment") {
			t.Errorf("%s headers/status = %d %#v", format, w.Code, w.Header())
		}
	}
	if w = exportGet(t, h, "/chats/"+chat+"/export/download?format=bad"); w.Code != http.StatusBadRequest {
		t.Fatalf("bad format = %d", w.Code)
	}
}

func TestJSONAndHTMLExportAreScopedTypedAndSafe(t *testing.T) {
	u, chat := exportUI(t)
	h := u.handler()
	w := exportGet(t, h, "/chats/"+chat+"/export/download?format=json")
	var doc map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	raw := w.Body.String()
	for _, forbidden := range []string{"raw_proto", "DO-NOT-EXPORT", "EVENT-SECRET", "FOREIGN"} {
		if strings.Contains(raw, forbidden) {
			t.Errorf("JSON leaked %q", forbidden)
		}
	}
	for _, required := range []string{`"schema_version"`, `"canonical_jid"`, `"aliases"`, `"events"`, `"hello edited"`, `"reaction"`} {
		if !strings.Contains(raw, required) {
			t.Errorf("JSON lacks %s: %s", required, raw)
		}
	}
	w = exportGet(t, h, "/chats/"+chat+"/export/download?format=html")
	html := w.Body.String()
	for _, forbidden := range []string{"<script>alert(1)</script>", "<script", "https://", "http://"} {
		if strings.Contains(html, forbidden) {
			t.Errorf("HTML contains %q", forbidden)
		}
	}
	for _, required := range []string{"&lt;script&gt;alert(1)&lt;/script&gt;", "data:text/plain;base64,", "Deleted", "Edited"} {
		if !strings.Contains(html, required) {
			t.Errorf("HTML lacks %q: %s", required, html)
		}
	}
}

func TestWhatsAppTextRoundTripsMultilineArabicAttachmentsEditsAndDST(t *testing.T) {
	u, chat := exportUI(t)
	w := exportGet(t, u.handler(), "/chats/"+chat+"/export/download?format=txt")
	lines, err := parseWhatsAppText(bytes.NewReader(w.Body.Bytes()), time.FixedZone("ignored", 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) < 5 {
		t.Fatalf("round trip lines = %+v\n%s", lines, w.Body.String())
	}
	joined := w.Body.String()
	for _, want := range []string{"second line", "ملف عربي", "report.txt", "[edited]", "[deleted]", "[reaction 👍]", "01:59", "03:01"} {
		if !strings.Contains(joined, want) {
			t.Errorf("text lacks %q: %s", want, joined)
		}
	}
}

func TestZIPIncludesTranscriptMediaAndMissingManifestWithSafeUniqueNames(t *testing.T) {
	u, chat := exportUI(t)
	missing := store.Message{ChatJID: chat, ID: "c", Kind: "document", Caption: "missing", Source: "live", Timestamp: time.Now().Unix(), ReceivedAt: 3, RawProto: []byte("x")}
	if _, err := u.st.InsertMessage(t.Context(), missing); err != nil {
		t.Fatal(err)
	}
	if _, err := u.st.Apply(t.Context(), store.Batch{Media: &store.Media{ChatJID: chat, MessageID: "c", Type: "document", Filename: "../report.txt", Path: filepath.Join(u.cfg.DataDir, "outside"), State: "done"}}); err != nil {
		t.Fatal(err)
	}
	w := exportGet(t, u.handler(), "/chats/"+chat+"/export/download?format=zip")
	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
		if strings.Contains(f.Name, "..") || strings.HasPrefix(f.Name, "/") {
			t.Errorf("unsafe zip name %q", f.Name)
		}
		if f.Name == "_chat.txt" {
			r, _ := f.Open()
			b, _ := io.ReadAll(r)
			_ = r.Close()
			if !strings.Contains(string(b), "hello") {
				t.Error("transcript incomplete")
			}
		}
	}
	if !names["_chat.txt"] || !names["report.txt"] || !names["missing-media.txt"] {
		t.Fatalf("zip names = %v", names)
	}
}
