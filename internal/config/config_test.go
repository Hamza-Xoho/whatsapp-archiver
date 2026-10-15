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

// M10: an exclude entry that is not a chat address would silently exclude nothing, so Load rejects it and names it.
func TestLoadRejectsExcludeEntryThatIsNotAChatAddress(t *testing.T) {
	for _, bad := range []string{"+4915111222333", "4915111222333", "4915111222333@whatsapp.net", "Family group", "+4915111222333@s.whatsapp.net", "abc@lid", "4915111222333@s.whatsapp.net "} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte("exclude:\n  - \"441234567890@s.whatsapp.net\"\n  - \""+bad+"\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := config.Load(path)
		if err == nil || !strings.Contains(err.Error(), `"`+bad+`"`) || !strings.Contains(err.Error(), "@s.whatsapp.net") || !strings.Contains(err.Error(), "@lid") || !strings.Contains(err.Error(), "@g.us") {
			t.Fatalf("entry %q: error = %v, want it named with the expected forms", bad, err)
		}
	}
}

// M10: the three chat address forms load.
func TestLoadAcceptsChatAddresses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	good := []string{"441234567890@s.whatsapp.net", "204871@lid", "120363111@g.us", "441234567890-1500000000@g.us"}
	if err := os.WriteFile(path, []byte("exclude: [\""+strings.Join(good, "\", \"")+"\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(path)
	if err != nil || strings.Join(c.Exclude, ",") != strings.Join(good, ",") {
		t.Fatalf("exclude=%v err=%v", c, err)
	}
}

// M10: the configuration example of the setup guide loads.
func TestSetupGuideConfigExampleLoads(t *testing.T) {
	guide, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	_, rest, ok := strings.Cut(string(guide), "```yaml\n")
	example, _, closed := strings.Cut(rest, "```")
	if !ok || !closed || !strings.Contains(example, "exclude:") {
		t.Fatal("no yaml example with an exclude list in README.md")
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err = os.WriteFile(path, []byte(example), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = config.Load(path); err != nil {
		t.Fatalf("the guide's example does not load: %v", err)
	}
}
