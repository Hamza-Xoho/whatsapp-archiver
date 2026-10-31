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
	storeSource, err := os.ReadFile(filepath.Join("..", "store", "export_media.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(zipSource), "ExportMediaNameFacts(") {
		t.Error("ZIP still performs per-media naming queries")
	}
	if strings.Contains(string(storeSource), "ROW_NUMBER() OVER (PARTITION BY message_id ORDER BY chat_jid)") ||
		strings.Contains(string(storeSource), "name_counts AS") {
		t.Error("ExportMediaPage still reranks the full canonical media window on each page")
	}
	for _, required := range []string{"messages_chat_ts", "bounded", "collision"} {
		if !strings.Contains(string(storeSource), required) && !strings.Contains(string(zipSource), required) {
			t.Errorf("dense export sources lack %q query/batching evidence", required)
		}
	}
}

func TestDenseMediaPagingCrossesTwoBoundariesWithStableCollisionNames(t *testing.T) {
	u := newChatsUI(t)
	u.cfg = &config.Config{DataDir: t.TempDir(), TimeZone: "UTC"}
	const count = 2201
	for i := range count {
		id := fmt.Sprintf("page-%04d", i)
		message := store.Message{ChatJID: "paged@g.us", ID: id, Kind: "document", Source: "live", Timestamp: int64(i + 1), ReceivedAt: int64(i + 1), RawProto: []byte("x")}
		media := store.Media{ChatJID: message.ChatJID, MessageID: id, Source: "live", Type: "document", Filename: "same.txt", State: "failed", MessageTimestamp: message.Timestamp}
		if _, err := u.st.Apply(t.Context(), store.Batch{Message: &message, Media: &media}); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := u.st.NewExportSnapshot(t.Context(), "paged@g.us")
	if err != nil {
		t.Fatal(err)
	}
	x := exportScope{jid: "paged@g.us", snapshot: snapshot}
	seen := make(map[string]bool, count)
	var ts int64
	var id string
	calls := 0
	for {
		page, pageErr := u.st.ExportMediaPage(t.Context(), store.ExportParams{CanonicalJID: x.jid, Snapshot: snapshot,
			AfterTS: ts, AfterID: id, HasCursor: calls > 0, Limit: 1000})
		if pageErr != nil {
			t.Fatal(pageErr)
		}
		calls++
		for _, record := range page {
			name := exportArchiveName(x, record)
			if seen[name] {
				t.Fatalf("collision across page boundary for %q", name)
			}
			seen[name] = true
			ts, id = record.Message.Timestamp, record.Message.ID
		}
		if len(page) < 1000 {
			break
		}
	}
	if calls != 3 || len(seen) != count {
		t.Fatalf("bounded pages/names = %d/%d, want 3/%d", calls, len(seen), count)
	}
}

func TestTextExportIteratorHasNoArchiveSizedCursorOrSenderCache(t *testing.T) {
	body, err := os.ReadFile("export_text_iter.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"OpenExportText", "names map[string]string", "ChatName(i.ctx"} {
		if strings.Contains(string(body), forbidden) {
			t.Errorf("text iterator retains archive-wide state/query %q", forbidden)
		}
	}
	for _, required := range []string{"ExportPage", "Limit: exportPageSize", "SkipEvents: true"} {
		if !strings.Contains(string(body), required) {
			t.Errorf("text iterator lacks bounded page contract %q", required)
		}
	}
}
