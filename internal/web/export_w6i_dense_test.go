package web

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/store"
)

func TestDenseMediaZIPIsCompleteCollisionSafeAndBounded(t *testing.T) {
	u := newChatsUI(t)
	u.cfg = &config.Config{DataDir: t.TempDir(), TimeZone: "UTC"}
	mediaDir := filepath.Join(u.cfg.DataDir, "media")
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("dense-media\n"), 32)
	payloadPath := filepath.Join(mediaDir, "payload.bin")
	if err := os.WriteFile(payloadPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	const doneCount, missingCount = 240, 20
	for i := range doneCount + missingCount {
		id := fmt.Sprintf("dense-%03d", i)
		message := store.Message{ChatJID: "dense@g.us", ID: id, Kind: "document", Source: "live", Timestamp: int64(i + 1), ReceivedAt: int64(i + 1), RawProto: []byte("x")}
		filename, state, path := "same?.txt", "done", payloadPath
		if i%2 == 1 {
			filename = "same*.txt" // both names sanitise to the same basename.
		}
		if i == 2 {
			filename = "_chat.txt"
		}
		if i == 3 {
			filename = "missing-media.txt"
		}
		if i >= doneCount {
			state, path = "failed", ""
		}
		media := store.Media{ChatJID: message.ChatJID, MessageID: id, Source: "live", Type: "document", MIME: "text/plain", Filename: filename, Path: path, State: state, Reason: "synthetic", DeclaredSize: int64(len(payload)), MessageTimestamp: message.Timestamp}
		if _, err := u.st.Apply(t.Context(), store.Batch{Message: &message, Media: &media}); err != nil {
			t.Fatal(err)
		}
	}
	started := time.Now()
	w := exportGet(t, u.handler(), "/chats/dense@g.us/export/download?format=zip")
	elapsed := time.Since(started)
	if w.Code != http.StatusOK || elapsed >= 2*time.Second {
		t.Fatalf("dense ZIP status/time = %d/%s, want 200 and <2s", w.Code, elapsed)
	}
	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	seen, mediaEntries, missing := map[string]bool{}, 0, ""
	for _, entry := range zr.File {
		if seen[entry.Name] {
			t.Fatalf("duplicate ZIP entry %q", entry.Name)
		}
		seen[entry.Name] = true
		if strings.Contains(entry.Name, "..") || strings.ContainsAny(entry.Name, "/\\") {
			t.Fatalf("unsafe ZIP entry %q", entry.Name)
		}
		reader, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(reader)
		closeErr := reader.Close()
		if readErr != nil || closeErr != nil {
			t.Fatalf("read %q: %v/%v", entry.Name, readErr, closeErr)
		}
		switch entry.Name {
		case "_chat.txt":
			if !bytes.Contains(body, []byte("[document]")) {
				t.Fatal("dense transcript is incomplete")
			}
		case "missing-media.txt":
			missing = string(body)
		default:
			mediaEntries++
			if !bytes.Equal(body, payload) {
				t.Fatalf("media entry %q has wrong content", entry.Name)
			}
		}
	}
	if mediaEntries != doneCount {
		t.Fatalf("media entries = %d, want %d", mediaEntries, doneCount)
	}
	for i := doneCount; i < doneCount+missingCount; i++ {
		if id := fmt.Sprintf("dense-%03d", i); !strings.Contains(missing, id) {
			t.Errorf("missing manifest lacks %s", id)
		}
	}
	t.Logf("dense ZIP: %d media + %d missing in %s", doneCount, missingCount, elapsed)
}

func TestDenseMediaQueryShapeHasNoPerMediaNamingOrFullWindowRank(t *testing.T) {
	zipSource, err := os.ReadFile("export_zip.go")
	if err != nil {
		t.Fatal(err)
	}
	storeSource, err := os.ReadFile(filepath.Join("..", "store", "export.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(zipSource), "ExportMediaNameFacts(") {
		t.Error("ZIP still performs per-media naming queries")
	}
	if strings.Contains(string(storeSource), "ROW_NUMBER() OVER (PARTITION BY message_id ORDER BY chat_jid)") {
		t.Error("ExportMediaPage still reranks the full canonical media window on each page")
	}
	for _, required := range []string{"messages_chat_ts", "bounded", "collision"} {
		if !strings.Contains(string(storeSource), required) && !strings.Contains(string(zipSource), required) {
			t.Errorf("dense export sources lack %q query/batching evidence", required)
		}
	}
}
