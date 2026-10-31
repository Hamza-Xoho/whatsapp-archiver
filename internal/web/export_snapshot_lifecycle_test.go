package web

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

func TestExportSnapshotsReleaseOnValidationFailureAndBeforeClientBackpressure(t *testing.T) {
	t.Run("empty range validation", func(t *testing.T) {
		u, chat := exportUI(t)
		w := exportGet(t, u.handler(), "/chats/"+chat+"/export/download?format=json&from=2036-01-01")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("empty export status = %d, want 400", w.Code)
		}
		insertCheckpointFrame(t, u.st, chat, "after-empty")
		wantCheckpointUnpinned(t, u.st)
	})

	t.Run("stalled successful client", func(t *testing.T) {
		u, chat := exportUI(t)
		w := &blockingExportWriter{header: make(http.Header), started: make(chan struct{}), release: make(chan struct{})}
		r := httptest.NewRequest(http.MethodGet, "/chats/"+chat+"/export/download?format=json", nil)
		r.Host = "localhost:8080"
		done := make(chan struct{})
		go func() {
			u.handler().ServeHTTP(w, r)
			close(done)
		}()
		defer func() {
			close(w.release)
			<-done
		}()
		<-w.started
		insertCheckpointFrame(t, u.st, chat, "while-client-stalled")
		wantCheckpointUnpinned(t, u.st)
		if w.status != http.StatusOK {
			t.Fatalf("stalled export status = %d, want 200", w.status)
		}
	})
}

func insertCheckpointFrame(t *testing.T, st *store.Store, chat, id string) {
	t.Helper()
	m := testkit.Message(chat, id, dashNow)
	if _, err := st.InsertMessage(t.Context(), m); err != nil {
		t.Fatal(err)
	}
}

func wantCheckpointUnpinned(t *testing.T, st *store.Store) {
	t.Helper()
	dsn := "file:" + filepath.ToSlash(st.Path()) + "?_pragma=busy_timeout(100)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var busy, logFrames, checkpointed int
	if err = db.QueryRowContext(t.Context(), "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &logFrames, &checkpointed); err != nil {
		t.Fatal(err)
	}
	if busy != 0 || logFrames != 0 || checkpointed != 0 {
		t.Fatalf("checkpoint remained pinned: busy/log/checkpointed=%d/%d/%d", busy, logFrames, checkpointed)
	}
}
