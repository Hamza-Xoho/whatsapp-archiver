package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/store"
)

func TestSnapshotVerifiesAndNeverIncludesSession(t *testing.T) {
	data := t.TempDir()
	st, err := store.Open(filepath.Join(data, "archive.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = st.InsertMessage(context.Background(), store.Message{ChatJID: "g@g.us", ID: "A", Source: "live", Timestamp: 1, ReceivedAt: 1, RawProto: []byte("x")})
	_ = st.Close()
	if err = os.WriteFile(filepath.Join(data, "session.db"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{DataDir: data, Backup: config.BackupConfig{Destination: filepath.Join(t.TempDir(), "backups"), Keep: 2}}
	report, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = Verify(report.Snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(filepath.Dir(report.Snapshot), "session.db")); !os.IsNotExist(err) {
		t.Fatal("session backed up")
	}
}
func TestVerifyRejectsTruncated(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bad.db")
	_ = os.WriteFile(p, []byte("bad"), 0o600)
	if Verify(p) == nil {
		t.Fatal("expected failure")
	}
}

var _ = time.Second
