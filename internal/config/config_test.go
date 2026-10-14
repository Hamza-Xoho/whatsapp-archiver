package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"whatsapp-archiver/internal/config"
)

func TestLoadDefaults(t *testing.T) {
	c, err := config.Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Media.VideoMaxMB != 100 {
		t.Errorf("VideoMaxMB = %d", c.Media.VideoMaxMB)
	}
	wantDownload := []string{"document", "image", "audio", "video"}
	if strings.Join(c.Media.Download, ",") != strings.Join(wantDownload, ",") {
		t.Errorf("Download = %v", c.Media.Download)
	}
	if strings.Join(c.Media.Skip, ",") != "sticker,gif" {
		t.Errorf("Skip = %v", c.Media.Skip)
	}
	if c.Media.RetryRequestsPerMinute != 10 || c.History.DaysLimit != 3650 || c.History.SizeMBLimit != 4096 {
		t.Errorf("unexpected limits: %+v %+v", c.Media, c.History)
	}
	if c.Backup.Keep != 7 || c.Alerts.NoEventsAfter != 6*time.Hour || c.Alerts.PhoneQuietAfter != 240*time.Hour || !c.WA.CheckLatestVersion {
		t.Errorf("unexpected defaults: %+v", c)
	}
}

func TestLoadKeepsDefaultsForMissingFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("exclude: [\"123@s.whatsapp.net\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Media.VideoMaxMB != 100 || len(c.Exclude) != 1 {
		t.Fatalf("partial config lost defaults: %+v", c)
	}
}

func TestLoadRejectsUnknownKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("exlude: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := config.Load(path)
	if err == nil || !strings.Contains(err.Error(), "exlude") {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadRejectsBadVersionOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("wa:\n  version_override: \"2.3000\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := config.Load(path)
	if err == nil || !strings.Contains(err.Error(), "version_override") {
		t.Fatalf("error = %v", err)
	}
}
