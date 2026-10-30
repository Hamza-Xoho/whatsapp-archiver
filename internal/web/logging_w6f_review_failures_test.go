package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/health"
	"whatsapp-archiver/internal/ingest"
)

func TestLoggingOverviewIsolatesStorageAndBackupFailures(t *testing.T) {
	d := newDash(t)
	s := New(Deps{Store: d.st, Rules: ingest.NewRuleSet(nil), Health: func() health.Snapshot { return d.snap },
		Activity: d.bus, Config: d.cfg, Now: func() time.Time { return dashNow }})
	s.loggingStorage = func(context.Context, time.Time, *time.Location) (storageFacts, error) {
		return storageFacts{}, errors.New("secret storage backend detail")
	}
	s.loggingBackups = func(*http.Request) (backupsData, error) {
		return backupsData{}, errors.New("secret backup backend detail")
	}

	body, code, contentType := get(t, s.Handler(), "/logging")
	if code != http.StatusOK || !strings.HasPrefix(contentType, "text/html") {
		t.Fatalf("overview failure isolation status=%d content-type=%q", code, contentType)
	}
	page := string(body)
	for _, want := range []string{"Connected", "Storage details are temporarily unavailable", "Backup details are temporarily unavailable", `href="/logging/storage"`} {
		if !strings.Contains(page, want) {
			t.Errorf("overview lacks isolated failure copy %q: %s", want, page)
		}
	}
	if strings.Contains(page, "secret storage") || strings.Contains(page, "secret backup") {
		t.Fatalf("overview exposed backend error: %s", page)
	}
}

func TestLoggingSettingsWriteFailuresStayIntegratedAndPreserveTypedValues(t *testing.T) {
	u := settingsUI(t)
	s := New(Deps{Store: u.st, Rules: u.rules, Config: u.cfg, Now: func() time.Time { return dashNow }})
	writeErr := errors.New("database path /secret/archive.db token=private")
	s.setMediaRules = func(context.Context, config.MediaRules) error { return writeErr }
	s.setAlertThresholds = func(context.Context, config.AlertConfig) error { return writeErr }
	s.setBackupSchedule = func(context.Context, config.BackupConfig) error { return writeErr }

	tests := []struct {
		section, typed string
		form           url.Values
	}{
		{"media", "77", mediaForm("77")},
		{"alerts", "17h", url.Values{"no_events_after": {"17h"}, "disconnected_after": {"21m"}, "phone_quiet_after": {"201h"}, "disk_free_min_gb": {"11"}}},
		{"backup", "04:37", url.Values{"time": {"04:37"}, "keep": {"13"}}},
	}
	for _, tc := range tests {
		for _, hx := range []bool{false, true} {
			body, code, header := loggingSettingsPost(t, s.Handler(), "/settings/"+tc.section, tc.form, hx)
			if code != http.StatusInternalServerError || !strings.HasPrefix(header.Get("Content-Type"), "text/html") {
				t.Errorf("%s hx=%v status=%d content-type=%q", tc.section, hx, code, header.Get("Content-Type"))
			}
			for _, want := range []string{`id="` + tc.section + `"`, tc.typed, "The setting could not be saved. Your previous setting is still in use."} {
				if !strings.Contains(body, want) {
					t.Errorf("%s hx=%v lacks %q: %s", tc.section, hx, want, body)
				}
			}
			if !hx && !strings.Contains(body, "Logging") {
				t.Errorf("%s no-JS failure is not the integrated settings page: %s", tc.section, body)
			}
			if strings.Contains(body, "/secret/archive.db") || strings.Contains(body, "token=private") {
				t.Fatalf("%s exposed write error: %s", tc.section, body)
			}
		}
	}
}

func TestLoggingHeartbeatWriteFailureKeepsOldValueAndNeverEchoesSecret(t *testing.T) {
	u := settingsUI(t)
	if err := u.st.SetHeartbeatURL(t.Context(), "https://heartbeat.invalid/old-secret"); err != nil {
		t.Fatal(err)
	}
	s := New(Deps{Store: u.st, Rules: u.rules, Config: u.cfg, Now: func() time.Time { return dashNow }})
	s.setHeartbeatURL = func(context.Context, string) error { return errors.New("write failed at /secret/archive.db") }
	form := url.Values{"heartbeat_url": {"https://heartbeat.invalid/new-secret"}}
	for _, hx := range []bool{false, true} {
		body, code, header := loggingSettingsPost(t, s.Handler(), "/settings/heartbeat", form, hx)
		if code != http.StatusInternalServerError || !strings.HasPrefix(header.Get("Content-Type"), "text/html") {
			t.Fatalf("heartbeat hx=%v status=%d content-type=%q", hx, code, header.Get("Content-Type"))
		}
		for _, want := range []string{`id="heartbeat"`, "The heartbeat endpoint could not be saved. The previous endpoint is still in use.", "heartbeat.invalid/…/old-secret"} {
			if !strings.Contains(body, want) {
				t.Errorf("heartbeat hx=%v lacks %q: %s", hx, want, body)
			}
		}
		if strings.Contains(body, "new-secret") || strings.Contains(body, "/secret/archive.db") {
			t.Fatalf("heartbeat hx=%v exposed secret: %s", hx, body)
		}
	}
	got, ok, err := u.st.HeartbeatURL(t.Context())
	if err != nil || !ok || got != "https://heartbeat.invalid/old-secret" {
		t.Fatalf("heartbeat changed after failed write: %q ok=%v err=%v", got, ok, err)
	}
}
