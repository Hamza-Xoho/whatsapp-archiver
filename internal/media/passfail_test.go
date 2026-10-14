package media

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

// If the write that fails an exhausted row keeps failing (disk full, read-only database), the request pass ends
// instead of reading the same expired row again and again; a later wake or tick tries it once more.
func TestRequestPassEndsWhenFailingARowCannotBeSaved(t *testing.T) {
	st := testkit.Store(t)
	if err := st.InsertMedia(t.Context(), store.Media{ChatJID: "123@g.us", MessageID: "STUCK", SenderJID: "5@lid", Type: "image", State: "expired", RetryDirectPath: "new", RawProto: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	// Make every write that sets a row failed abort, as a full disk would.
	db, err := sql.Open("sqlite", "file:"+st.Path()+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TRIGGER no_failed BEFORE UPDATE ON media WHEN NEW.state='failed' BEGIN SELECT RAISE(ABORT,'disk full'); END`); err != nil {
		t.Fatal(err)
	}
	d := expiredDown()
	q := newTestQueue(t, st, d, config.MediaRules{Download: []string{"image"}, Workers: 1}, newClock(), nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() { var cur *store.Media; q.requestPass(ctx, &cur); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		cancel() // stop the spinning pass before failing
		<-done
		t.Fatal("request pass kept looping over a row it could not save")
	}
	if n := len(d.requests()); n != 0 {
		t.Fatalf("re-upload requests = %d, want 0", n)
	}
	if m := row(t, st, "123@g.us", "STUCK"); m.State != "expired" {
		t.Fatalf("row state=%q, want expired (the write failed)", m.State)
	}
}
