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

// The UI has no login in phase 2, so it listens on loopback only.
func TestLoadRejectsNonLoopbackListen(t *testing.T) {
	c, err := config.Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Web.Listen != "127.0.0.1:8080" {
		t.Fatalf("default web.listen = %q", c.Web.Listen)
	}
	load := func(listen string) error {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte("web:\n  listen: \""+listen+"\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		c, err := config.Load(path)
		if err == nil && c.Web.Listen != listen {
			t.Fatalf("web.listen = %q, want %q", c.Web.Listen, listen)
		}
		return err
	}
	for _, bad := range []string{"0.0.0.0:8080", "192.168.0.104:8080", ":8080", "example.com:8080", "127.0.0.1"} {
		if err := load(bad); err == nil || !strings.Contains(err.Error(), "web.listen") {
			t.Errorf("listen %q: error = %v, want a web.listen error", bad, err)
		}
	}
	for _, good := range []string{"127.0.0.1:9000", "localhost:8080", "[::1]:8080"} {
		if err := load(good); err != nil {
			t.Errorf("listen %q rejected: %v", good, err)
		}
	}
}

func TestValidateMediaRules(t *testing.T) {
	good := config.MediaRules{Download: []string{"image", "video"}, Skip: []string{"sticker"}, VideoMaxMB: 100, Workers: 2, RetryRequestsPerMinute: 10}
	if err := config.ValidateMediaRules(good); err != nil {
		t.Fatalf("good rules rejected: %v", err)
	}
	if err := config.ValidateMediaRules(config.MediaRules{Workers: 1, RetryRequestsPerMinute: 1}); err != nil {
		t.Fatalf("no types and no cap is allowed (text only): %v", err)
	}
	for name, mutate := range map[string]func(*config.MediaRules){
		"negative cap":     func(r *config.MediaRules) { r.VideoMaxMB = -1 },
		"huge cap":         func(r *config.MediaRules) { r.VideoMaxMB = 100001 },
		"zero workers":     func(r *config.MediaRules) { r.Workers = 0 },
		"too many workers": func(r *config.MediaRules) { r.Workers = 17 },
		"zero retries":     func(r *config.MediaRules) { r.RetryRequestsPerMinute = 0 },
		"too many retries": func(r *config.MediaRules) { r.RetryRequestsPerMinute = 601 },
		"unknown download": func(r *config.MediaRules) { r.Download = []string{"image", "hologram"} },
		"unknown skip":     func(r *config.MediaRules) { r.Skip = []string{"hologram"} },
	} {
		r := good
		mutate(&r)
		if err := config.ValidateMediaRules(r); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestValidateAlertThresholds(t *testing.T) {
	good := config.AlertConfig{NoEventsAfter: time.Hour, DisconnectedAfter: time.Minute, PhoneQuietAfter: 240 * time.Hour, DiskFreeMinGB: 0}
	if err := config.ValidateAlertThresholds(good); err != nil {
		t.Fatalf("good thresholds rejected: %v", err)
	}
	for name, mutate := range map[string]func(*config.AlertConfig){
		"zero no-events":     func(a *config.AlertConfig) { a.NoEventsAfter = 0 },
		"negative offline":   func(a *config.AlertConfig) { a.DisconnectedAfter = -time.Minute },
		"zero phone quiet":   func(a *config.AlertConfig) { a.PhoneQuietAfter = 0 },
		"negative disk free": func(a *config.AlertConfig) { a.DiskFreeMinGB = -1 },
	} {
		a := good
		mutate(&a)
		if err := config.ValidateAlertThresholds(a); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestValidateBackupSchedule(t *testing.T) {
	for _, ok := range []string{"00:00", "03:30", "23:59"} {
		if err := config.ValidateBackupSchedule(ok, 7); err != nil {
			t.Errorf("%s rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "25:00", "24:00", "12:60", "3:30", "0330", "03:30:00", "ab:cd", " 03:30"} {
		if err := config.ValidateBackupSchedule(bad, 7); err == nil {
			t.Errorf("time %q accepted", bad)
		}
	}
	for _, keep := range []int{0, -1, 3651} {
		if err := config.ValidateBackupSchedule("03:30", keep); err == nil {
			t.Errorf("keep %d accepted", keep)
		}
	}
	if err := config.ValidateBackupSchedule("03:30", 1); err != nil {
		t.Errorf("keep 1 rejected: %v", err)
	}
}

// Load and the settings screen share one rule set.
func TestLoadRejectsInvalidRuntimeSections(t *testing.T) {
	for name, yaml := range map[string]string{
		"backup time":   "backup:\n  time: \"25:00\"\n",
		"backup keep":   "backup:\n  keep: 0\n",
		"video cap":     "media:\n  video_max_mb: -3\n",
		"alert horizon": "alerts:\n  no_events_after: 0s\n",
	} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := config.Load(path); err == nil {
			t.Errorf("%s: Load accepted %q", name, yaml)
		}
	}
}
