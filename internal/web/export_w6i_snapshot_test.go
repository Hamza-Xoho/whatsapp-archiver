package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/store"
)

type blockingExportWriter struct {
	header           http.Header
	body             bytes.Buffer
	status           int
	started, release chan struct{}
	once             sync.Once
}

func (w *blockingExportWriter) Header() http.Header { return w.header }
func (w *blockingExportWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *blockingExportWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	w.once.Do(func() {
		close(w.started)
		<-w.release
	})
	return w.body.Write(p)
}

func TestJSONExportSnapshotDoesNotCrossConcurrentDuplicateRows(t *testing.T) {
	u := newChatsUI(t)
	u.cfg = &config.Config{DataDir: t.TempDir(), TimeZone: "UTC"}
	ctx := t.Context()
	if _, err := u.st.InsertIDMapping(ctx, store.IDMapping{LID: "7@lid", PN: "700@s.whatsapp.net"}); err != nil {
		t.Fatal(err)
	}
	if _, err := u.st.InsertMessage(ctx, store.Message{ChatJID: "7@lid", ID: "same", Kind: "text", Text: "snapshot", Source: "live", Timestamp: 100, ReceivedAt: 10, RawProto: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	w := &blockingExportWriter{header: make(http.Header), started: make(chan struct{}), release: make(chan struct{})}
	r := httptest.NewRequest(http.MethodGet, "/chats/7@lid/export/download?format=json", nil)
	r.Host = "localhost:8080"
	done := make(chan struct{})
	go func() {
		u.handler().ServeHTTP(w, r)
		close(done)
	}()
	<-w.started // validation and its duplicate decision have completed; the export body has not queried messages yet.
	if _, err := u.st.InsertMessage(ctx, store.Message{ChatJID: "700@s.whatsapp.net", ID: "same", Kind: "text", Text: "concurrent duplicate", Source: "history", Timestamp: 101, ReceivedAt: 20, RawProto: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	close(w.release)
	<-done
	var document struct {
		Messages []struct {
			ID, Text string
		} `json:"messages"`
	}
	if err := json.Unmarshal(w.body.Bytes(), &document); err != nil {
		t.Fatalf("JSON after concurrent duplicate: %v\n%s", err, w.body.String())
	}
	if w.status != http.StatusOK || len(document.Messages) != 1 || document.Messages[0].ID != "same" || document.Messages[0].Text != "snapshot" {
		t.Fatalf("export crossed its snapshot: status=%d messages=%+v", w.status, document.Messages)
	}
}
