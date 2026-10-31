package web

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"whatsapp-archiver/internal/store"
)

func TestZIPTranscriptAttachmentNamesMatchEntriesWhenIDAndTimeOrderDiffer(t *testing.T) {
	u := newChatsUI(t)
	mediaDir := filepath.Join(u.cfg.DataDir, "media")
	if err := os.MkdirAll(mediaDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for i, item := range []struct {
		id      string
		payload string
	}{
		{id: "z-first-in-time", payload: "earlier"},
		{id: "a-second-in-time", payload: "later"},
	} {
		path := filepath.Join(mediaDir, item.id+".bin")
		if err := os.WriteFile(path, []byte(item.payload), 0o600); err != nil {
			t.Fatal(err)
		}
		message := store.Message{ChatJID: "collision@g.us", ID: item.id, Kind: "document", Source: "live",
			Timestamp: int64(i + 1), ReceivedAt: int64(i + 1), RawProto: []byte("x")}
		media := store.Media{ChatJID: message.ChatJID, MessageID: message.ID, Type: "document", Source: "live",
			Filename: "same.txt", Path: path, State: "done", MessageTimestamp: message.Timestamp}
		if _, err := u.st.Apply(t.Context(), store.Batch{Message: &message, Media: &media}); err != nil {
			t.Fatal(err)
		}
	}

	w := exportGet(t, u.handler(), "/chats/collision@g.us/export/download?format=zip")
	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	entries := make(map[string]bool)
	transcript := ""
	for _, entry := range zr.File {
		entries[entry.Name] = true
		if entry.Name != "_chat.txt" {
			continue
		}
		r, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(r)
		_ = r.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		transcript = string(body)
	}
	refs := attachmentReferences(transcript)
	if len(refs) != 2 {
		t.Fatalf("attachment references = %v, want two; transcript=%q", refs, transcript)
	}
	for _, ref := range refs {
		if !entries[ref] {
			t.Errorf("transcript attachment %q has no matching ZIP entry; entries=%v", ref, entries)
		}
	}
}

func attachmentReferences(transcript string) []string {
	var refs []string
	for rest := transcript; ; {
		start := strings.Index(rest, "[attachment: ")
		if start < 0 {
			return refs
		}
		rest = rest[start+len("[attachment: "):]
		end := strings.IndexByte(rest, ']')
		if end < 0 {
			return refs
		}
		refs = append(refs, rest[:end])
		rest = rest[end+1:]
	}
}
