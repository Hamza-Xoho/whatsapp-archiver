package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestLoggingSettingsHasSeparateFormsAndEffectiveTimeCopy(t *testing.T) {
	u := settingsUI(t)
	body, code, _ := get(t, u.handler(), "/logging/settings")
	if code != http.StatusOK {
		t.Fatalf("status %d: %s", code, body)
	}
	page := string(body)
	for _, want := range []string{
		`action="/settings/media"`, `action="/settings/alerts"`, `action="/settings/backup"`,
		`action="/settings/heartbeat"`, `action="/settings/heartbeat-remove"`, `action="/backups/test-alert"`,
		"Takes effect after restart", "Lowering this deletes the oldest snapshots", "Installation",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("settings lacks %q", want)
		}
	}
	if strings.Contains(page, `sse-connect="/events"`) {
		t.Fatal("settings opens SSE")
	}
}

func TestHeartbeatSettingIsWriteOnlyMaskedAndBlankKeepsIt(t *testing.T) {
	u := settingsUI(t)
	const secret = "https://heartbeat.example/private/token/ping-secret?credential=hidden"
	body, code := postForm(t, u.handler(), "/settings/heartbeat", url.Values{"heartbeat_url": {secret}})
	if code != http.StatusSeeOther {
		t.Fatalf("save = %d %s", code, body)
	}
	page, _, _ := get(t, u.handler(), "/logging/settings")
	raw := string(page)
	if !strings.Contains(raw, "https://heartbeat.example/…/ping-secret") {
		t.Fatalf("masked heartbeat missing: %s", raw)
	}
	for _, leaked := range []string{"private", "token", "credential", "hidden"} {
		if strings.Contains(raw, leaked) {
			t.Fatalf("heartbeat page leaks %q", leaked)
		}
	}
	if body, code = postForm(t, u.handler(), "/settings/heartbeat", url.Values{"heartbeat_url": {""}}); code != http.StatusSeeOther {
		t.Fatalf("blank save = %d %s", code, body)
	}
	if got, ok, err := u.st.HeartbeatURL(t.Context()); err != nil || !ok || got != secret {
		t.Fatalf("blank changed heartbeat to %q ok=%v err=%v", got, ok, err)
	}
}

func TestHeartbeatInvalidWritePreservesValueAndRemoveNeedsConfirmation(t *testing.T) {
	u := settingsUI(t)
	const original = "https://heartbeat.example/path/ping"
	if err := u.st.SetHeartbeatURL(t.Context(), original); err != nil {
		t.Fatal(err)
	}
	body, code := postForm(t, u.handler(), "/settings/heartbeat", url.Values{"heartbeat_url": {"file:///tmp/not-network"}})
	if code != http.StatusUnprocessableEntity || !strings.Contains(visibleText(body), "Heartbeat URL") {
		t.Fatalf("invalid save = %d %s", code, body)
	}
	if got, _, _ := u.st.HeartbeatURL(t.Context()); got != original {
		t.Fatalf("invalid write changed heartbeat to %q", got)
	}
	if _, code = postForm(t, u.handler(), "/settings/heartbeat-remove", nil); code != http.StatusUnprocessableEntity {
		t.Fatalf("unconfirmed remove = %d", code)
	}
	if _, code = postForm(t, u.handler(), "/settings/heartbeat-remove", url.Values{"confirm": {"yes"}}); code != http.StatusSeeOther {
		t.Fatalf("confirmed remove = %d", code)
	}
	if _, ok, _ := u.st.HeartbeatURL(t.Context()); ok {
		t.Fatal("confirmed remove kept heartbeat")
	}
}

func TestLoggingSettingsGETNeverCreatesHeartbeatSetting(t *testing.T) {
	u := settingsUI(t)
	if _, code, _ := get(t, u.handler(), "/logging/settings"); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if _, ok, err := u.st.HeartbeatURL(t.Context()); err != nil || ok {
		t.Fatalf("GET wrote heartbeat ok=%v err=%v", ok, err)
	}
}
