package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types/events"

	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/media"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/wa"
)

// settingsUI is the settings screen over a real store; cfg holds the config.yaml values.
func settingsUI(t *testing.T) *chatsUI {
	t.Helper()
	u := newChatsUI(t)
	u.cfg = &config.Config{
		DataDir: "/srv/whatsapp-archiver/data", TimeZone: "Asia/Riyadh",
		Media:  config.MediaRules{Download: []string{"image"}, VideoMaxMB: 50, Workers: 2, RetryRequestsPerMinute: 10},
		Alerts: config.AlertConfig{NoEventsAfter: 6 * time.Hour, DisconnectedAfter: 15 * time.Minute, PhoneQuietAfter: 240 * time.Hour, DiskFreeMinGB: 5},
		Backup: config.BackupConfig{Destination: "/mnt/data/backups", Time: "03:30", Keep: 7},
		WA:     config.WAConfig{CheckLatestVersion: true},
		Web:    config.Web{Listen: "127.0.0.1:8080"},
	}
	return u
}

type fakeDown struct{}

func (fakeDown) Download(context.Context, []byte, string) ([]byte, error) { return nil, nil }
func (fakeDown) RequestMediaRetry(context.Context, wa.MediaRef, []byte) error {
	return nil
}
func (fakeDown) ApplyMediaRetry(*events.MediaRetry, []byte) (string, error) { return "", nil }

type fixedClock struct{}

func (fixedClock) Now() time.Time { return dashNow }

func mediaForm(cap string) url.Values {
	return url.Values{"download": {"image", "video"}, "skip": {"sticker"}, "video_max_mb": {cap}, "workers": {"2"}, "retry_requests_per_minute": {"10"}}
}

// A saved media rule decides the next item: the queue reads the store at each Enqueue, as serve's live settings do.
func TestSettingsMediaSaveAppliesToNextDownloadDecision(t *testing.T) {
	u := settingsUI(t)
	ctx := t.Context()
	if err := u.st.SetMediaRules(ctx, config.MediaRules{Download: []string{"image", "video"}, VideoMaxMB: 100, Workers: 1, RetryRequestsPerMinute: 10}); err != nil {
		t.Fatal(err)
	}
	global := func() (config.MediaRules, error) {
		r, _, err := u.st.MediaRules(context.Background())
		return r, err
	}
	q := media.NewQueue(t.TempDir(), global, nil, u.st, fakeDown{}, fixedClock{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	enqueue := func(id string) store.Media {
		t.Helper()
		if err := q.Enqueue(ctx, store.Media{ChatJID: "g@g.us", MessageID: id, Type: "video", DeclaredSize: 150 << 20, RawProto: []byte(id)}); err != nil {
			t.Fatal(err)
		}
		m, err := u.st.MediaByID(ctx, "g@g.us", id)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	if m := enqueue("BEFORE"); m.State != "skipped" || m.Reason != "over 100 MB cap" {
		t.Fatalf("before the change: %+v", m)
	}
	body, code := postForm(t, u.handler(), "/settings/media", mediaForm("200"))
	if code != http.StatusSeeOther {
		t.Fatalf("POST /settings/media: %d %s", code, body)
	}
	if m := enqueue("AFTER"); m.State != "pending" {
		t.Fatalf("after raising the cap to 200 MB: %+v", m)
	}
	got, ok, err := u.st.MediaRules(ctx)
	if err != nil || !ok || got.VideoMaxMB != 200 || strings.Join(got.Download, ",") != "image,video" || strings.Join(got.Skip, ",") != "sticker" || got.Workers != 2 {
		t.Fatalf("stored rules = %+v ok=%v err=%v", got, ok, err)
	}
	if page := u.page(t, "/settings"); !strings.Contains(page, "200") {
		t.Fatalf("the page does not show the saved cap: %s", page)
	}
}

func TestSettingsRejectsInvalidVideoCap(t *testing.T) {
	u := settingsUI(t)
	ctx := t.Context()
	before := config.MediaRules{Download: []string{"image"}, VideoMaxMB: 100, Workers: 1, RetryRequestsPerMinute: 10}
	if err := u.st.SetMediaRules(ctx, before); err != nil {
		t.Fatal(err)
	}
	for _, cap := range []string{"-5", "abc", "", "99999999"} {
		body, code := postForm(t, u.handler(), "/settings/media", mediaForm(cap))
		if code != http.StatusUnprocessableEntity {
			t.Fatalf("cap %q: status %d, want 422", cap, code)
		}
		if text := visibleText(body); !strings.Contains(text, "Largest video") || !strings.Contains(text, "whole number") {
			t.Fatalf("cap %q: no field error in %s", cap, text)
		}
		got, _, err := u.st.MediaRules(ctx)
		if err != nil || got.VideoMaxMB != 100 || len(got.Download) != 1 {
			t.Fatalf("cap %q: rules changed to %+v (err %v)", cap, got, err)
		}
	}
}

func TestSettingsMediaRejectsUnknownType(t *testing.T) {
	u := settingsUI(t)
	f := mediaForm("10")
	f.Add("download", "hologram")
	if _, code := postForm(t, u.handler(), "/settings/media", f); code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422", code)
	}
	if _, ok, _ := u.st.MediaRules(t.Context()); ok {
		t.Fatal("an invalid post wrote the rules")
	}
}

func TestSettingsNeverShowsNtfyToken(t *testing.T) {
	u := settingsUI(t)
	u.cfg.Alerts.Ntfy = &config.NtfyConfig{URL: "https://ntfy.example.org/archiver-7f3a91c2topic", Token: "tk_SECRETtoken123"}
	raw, code, _ := get(t, u.handler(), "/settings")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	for _, secret := range []string{"tk_SECRETtoken123", "7f3a91c2topic", "ntfy.example.org"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("the page shows %q", secret)
		}
	}
	if text := visibleText(string(raw)); !strings.Contains(text, "Phone alerts (ntfy) configured") {
		t.Fatalf("no configured notice: %s", text)
	}
	u.cfg.Alerts.Ntfy = nil
	if text := u.page(t, "/settings"); !strings.Contains(text, "Phone alerts (ntfy) not configured") {
		t.Fatalf("no not-configured notice: %s", text)
	}
	// An error re-render must not leak either.
	u.cfg.Alerts.Ntfy = &config.NtfyConfig{URL: "https://ntfy.example.org/archiver-7f3a91c2topic", Token: "tk_SECRETtoken123"}
	body, _ := postForm(t, u.handler(), "/settings/alerts", url.Values{"no_events_after": {"nonsense"}})
	if strings.Contains(body, "tk_SECRETtoken123") || strings.Contains(body, "7f3a91c2topic") {
		t.Fatal("the error page shows the ntfy secret")
	}
}

func TestSettingsBackupTimeValidated(t *testing.T) {
	u := settingsUI(t)
	ctx := t.Context()
	body, code := postForm(t, u.handler(), "/settings/backup", url.Values{"time": {"25:00"}, "keep": {"7"}})
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("25:00: status %d, want 422", code)
	}
	if text := visibleText(body); !strings.Contains(text, "Backup time") || !strings.Contains(text, "HH:MM") {
		t.Fatalf("no field error: %s", text)
	}
	if _, ok, _ := u.st.BackupSchedule(ctx); ok {
		t.Fatal("an invalid time was written")
	}
	if _, code = postForm(t, u.handler(), "/settings/backup", url.Values{"time": {"03:30"}, "keep": {"0"}}); code != http.StatusUnprocessableEntity {
		t.Fatalf("keep 0: status %d, want 422", code)
	}
	if _, code = postForm(t, u.handler(), "/settings/backup", url.Values{"time": {"04:15"}, "keep": {"14"}}); code != http.StatusSeeOther {
		t.Fatalf("valid: status %d, want 303", code)
	}
	got, ok, err := u.st.BackupSchedule(ctx)
	if err != nil || !ok || got.Time != "04:15" || got.Keep != 14 || got.Destination != "" {
		t.Fatalf("stored schedule = %+v ok=%v err=%v", got, ok, err)
	}
}

func TestSettingsAlertsSaveAndValidate(t *testing.T) {
	u := settingsUI(t)
	ctx := t.Context()
	good := url.Values{"no_events_after": {"8h"}, "disconnected_after": {"20m"}, "phone_quiet_after": {"200h"}, "disk_free_min_gb": {"9"}}
	if body, code := postForm(t, u.handler(), "/settings/alerts", good); code != http.StatusSeeOther {
		t.Fatalf("valid: %d %s", code, body)
	}
	got, ok, err := u.st.AlertThresholds(ctx)
	if err != nil || !ok || got.NoEventsAfter != 8*time.Hour || got.DisconnectedAfter != 20*time.Minute || got.PhoneQuietAfter != 200*time.Hour || got.DiskFreeMinGB != 9 || got.Ntfy != nil {
		t.Fatalf("stored thresholds = %+v ok=%v err=%v", got, ok, err)
	}
	for field, value := range map[string]string{"no_events_after": "0s", "disconnected_after": "-5m", "phone_quiet_after": "soon", "disk_free_min_gb": "-1"} {
		f := url.Values{}
		for k, v := range good {
			f[k] = v
		}
		f.Set(field, value)
		body, code := postForm(t, u.handler(), "/settings/alerts", f)
		if code != http.StatusUnprocessableEntity {
			t.Fatalf("%s=%q: status %d, want 422", field, value, code)
		}
		if !strings.Contains(visibleText(body), "Alert thresholds") {
			t.Fatalf("%s: no error shown: %s", field, body)
		}
		if after, _, _ := u.st.AlertThresholds(ctx); after.NoEventsAfter != 8*time.Hour || after.DiskFreeMinGB != 9 {
			t.Fatalf("%s=%q changed the stored thresholds: %+v", field, value, after)
		}
	}
}

// Fields the user did not mean to change stay as the form had them: a rejected post shows what was typed.
func TestSettingsErrorKeepsTypedValues(t *testing.T) {
	u := settingsUI(t)
	body, _ := postForm(t, u.handler(), "/settings/backup", url.Values{"time": {"25:00"}, "keep": {"12"}})
	if !strings.Contains(body, `value="25:00"`) || !strings.Contains(body, `value="12"`) {
		t.Fatalf("typed values lost: %s", body)
	}
}

func TestSettingsShowsConfigFileValuesReadOnly(t *testing.T) {
	u := settingsUI(t)
	text := u.page(t, "/settings")
	for _, want := range []string{"/srv/whatsapp-archiver/data", "Asia/Riyadh", "/mnt/data/backups", "127.0.0.1:8080", "set in config.yaml"} {
		if !strings.Contains(text, want) {
			t.Errorf("page lacks %q: %s", want, text)
		}
	}
	// With nothing saved the form shows config.yaml's values, the ones capture is using.
	raw, _, _ := get(t, u.handler(), "/settings")
	if !strings.Contains(string(raw), `name="video_max_mb"`) || !strings.Contains(string(raw), `value="50"`) {
		t.Fatalf("config.yaml cap not prefilled: %s", raw)
	}
}

func TestSettingsPageShowsSavedValuesInsteadOfConfig(t *testing.T) {
	u := settingsUI(t)
	if err := u.st.SetBackupSchedule(t.Context(), config.BackupConfig{Time: "05:05", Keep: 21}); err != nil {
		t.Fatal(err)
	}
	raw, _, _ := get(t, u.handler(), "/settings")
	if !strings.Contains(string(raw), `value="05:05"`) || !strings.Contains(string(raw), `value="21"`) || strings.Contains(string(raw), `value="03:30"`) {
		t.Fatalf("saved schedule not shown: %s", raw)
	}
}

// A GET only reads (no write on a page view).
func TestSettingsGetWritesNothing(t *testing.T) {
	u := settingsUI(t)
	u.page(t, "/settings")
	for _, key := range []string{store.KeyMediaRules, store.KeyAlertThresholds, store.KeyBackupSchedule} {
		if _, ok, err := u.st.Setting(t.Context(), key); err != nil || ok {
			t.Fatalf("GET /settings wrote %s (ok=%v err=%v)", key, ok, err)
		}
	}
}

func TestSettingsPostNeedsSameOrigin(t *testing.T) {
	u := settingsUI(t)
	r, _ := http.NewRequest(http.MethodPost, "/settings/backup", strings.NewReader("time=04:00&keep=3"))
	r.Host = "localhost:8080"
	r.Header.Set("Origin", "http://evil.example")
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	u.handler().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403", w.Code)
	}
	if _, ok, _ := u.st.BackupSchedule(t.Context()); ok {
		t.Fatal("a cross-origin post wrote the schedule")
	}
}

func TestSettingsUnknownSectionIs404(t *testing.T) {
	u := settingsUI(t)
	if _, code := postForm(t, u.handler(), "/settings/engine", url.Values{"x": {"1"}}); code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", code)
	}
}
