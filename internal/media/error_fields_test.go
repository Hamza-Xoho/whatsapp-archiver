package media

import (
	"errors"
	"strings"
	"testing"

	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

func TestDownloadFailureStoresFullRedactedReasonAndClassification(t *testing.T) {
	st := testkit.Store(t)
	secret := strings.Repeat("detail-", 100) + "SECRET"
	d := &fakeDown{download: func([]byte, string) ([]byte, error) {
		return nil, errors.New("download https://mmg.whatsapp.net/v/t62/file?token=" + secret)
	}}
	q := newTestQueue(t, st, d, config.MediaRules{Download: []string{"image"}, Workers: 1}, newClock(), nil)
	enqueue(t, q, store.Media{ChatJID: "g@g.us", MessageID: "ERR", Type: "image", RawProto: []byte("raw")})
	stop := start(t, q)
	eventually(t, "failure recorded", func() bool { return row(t, st, "g@g.us", "ERR").Attempts == roundAttempts })
	stop()

	m := row(t, st, "g@g.us", "ERR")
	if m.ErrorClass != "other" || m.HTTPStatus != 0 {
		t.Fatalf("classification = %q/%d, want other/0", m.ErrorClass, m.HTTPStatus)
	}
	if !strings.Contains(m.Reason, "mmg.whatsapp.net/v/t62/file?REDACTED") {
		t.Fatalf("reason lost: %q", m.Reason)
	}
	if strings.Contains(m.Reason, "SECRET") || strings.Contains(m.Reason, "token=") {
		t.Fatalf("reason contains signed URL secret: %q", m.Reason)
	}
	if len(m.Reason) < 50 {
		t.Fatalf("reason was unexpectedly truncated: %q", m.Reason)
	}
}
