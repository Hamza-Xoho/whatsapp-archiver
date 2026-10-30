package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func loggingSettingsPost(t *testing.T, h http.Handler, path string, form url.Values, hx bool) (string, int, http.Header) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	r.Host = "localhost:8080"
	r.Header.Set("Origin", "http://localhost:8080")
	r.Header.Set("Referer", "http://localhost:8080/logging/settings")
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if hx {
		r.Header.Set("HX-Request", "true")
		r.Header.Set("HX-Current-URL", "http://localhost:8080/logging/settings")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	body, err := io.ReadAll(w.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body), w.Code, w.Header()
}

func TestLoggingSettingsNoJSSavesReturnToOrigin(t *testing.T) {
	u := settingsUI(t)
	for _, tc := range []struct {
		section string
		form    url.Values
		copy    string
	}{
		{"media", mediaForm("75"), "Saved. The next media item uses these rules."},
		{"alerts", url.Values{"no_events_after": {"8h"}, "disconnected_after": {"20m"}, "phone_quiet_after": {"200h"}, "disk_free_min_gb": {"9"}}, "Saved. The next health check uses these thresholds."},
		{"backup", url.Values{"time": {"04:15"}, "keep": {"14"}}, "Saved. The scheduler reads it after its next wait."},
	} {
		_, code, header := loggingSettingsPost(t, u.handler(), "/settings/"+tc.section, tc.form, false)
		if code != http.StatusSeeOther || header.Get("Location") != "/logging/settings?saved="+tc.section+"#"+tc.section {
			t.Errorf("%s no-JS response = %d Location %q", tc.section, code, header.Get("Location"))
			continue
		}
		body, got, _ := get(t, u.handler(), header.Get("Location"))
		if got != http.StatusOK || !strings.Contains(visibleText(string(body)), tc.copy) {
			t.Errorf("%s saved page lacks precise result copy: %s", tc.section, body)
		}
	}
}

func TestLoggingSettingsHTMXReturnsOnlyAffectedSection(t *testing.T) {
	u := settingsUI(t)
	body, code, _ := loggingSettingsPost(t, u.handler(), "/settings/media", mediaForm("80"), true)
	if code != http.StatusOK {
		t.Fatalf("valid htmx save status = %d: %s", code, body)
	}
	for _, want := range []string{`id="media"`, `role="status"`, "Saved. The next media item uses these rules."} {
		if !strings.Contains(body, want) {
			t.Errorf("valid media fragment lacks %q: %s", want, body)
		}
	}
	for _, not := range []string{"<!doctype", `id="alerts"`, `id="offline-banner"`} {
		if strings.Contains(body, not) {
			t.Errorf("valid media response is not a scoped fragment; contains %q", not)
		}
	}

	bad := mediaForm("typed-bad-value")
	body, code, _ = loggingSettingsPost(t, u.handler(), "/settings/media", bad, true)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid htmx save status = %d: %s", code, body)
	}
	for _, want := range []string{`id="media"`, `role="alert"`, `value="typed-bad-value"`} {
		if !strings.Contains(body, want) {
			t.Errorf("invalid media fragment lacks %q: %s", want, body)
		}
	}
}

func TestLoggingSettingsFormsDeclareScopedEnhancementAndTiming(t *testing.T) {
	body, code, _ := get(t, settingsUI(t).handler(), "/logging/settings")
	if code != http.StatusOK {
		t.Fatalf("GET settings = %d", code)
	}
	raw := string(body)
	for _, section := range []string{"media", "alerts", "backup", "heartbeat"} {
		start := strings.Index(raw, `id="`+section+`"`)
		if start < 0 {
			t.Fatalf("missing %s section", section)
		}
		end := strings.Index(raw[start:], "</section>")
		fragment := raw[start : start+end]
		for _, want := range []string{`hx-post=`, `hx-target="closest section"`, `hx-swap="outerHTML"`} {
			if !strings.Contains(fragment, want) {
				t.Errorf("%s form lacks %s", section, want)
			}
		}
	}
	media := raw[strings.Index(raw, `id="media"`):strings.Index(raw, `id="alerts"`)]
	if note, submit := strings.Index(media, "Takes effect after restart"), strings.Index(media, "Save media rules"); note < 0 || note > submit {
		t.Error("restart-only media note is not before submit")
	}
}

func TestLoggingSettingsTestAlertStaysScopedAndRedacted(t *testing.T) {
	r := newBackupsRig(t, true)
	r.n.err = &secretTestAlertError{}
	body, code, _ := loggingSettingsPost(t, r.h, "/backups/test-alert", nil, true)
	if code != http.StatusOK {
		t.Fatalf("test alert status = %d: %s", code, body)
	}
	if !strings.Contains(body, `id="alerts"`) || !strings.Contains(body, "Could not send the test alert") || strings.Contains(body, "secret-topic") {
		t.Fatalf("test alert fragment is unscoped or unsafe: %s", body)
	}
	body, code, _ = loggingSettingsPost(t, r.h, "/backups/test-alert", nil, true)
	if code != http.StatusOK || !strings.Contains(body, "Wait a few seconds") {
		t.Fatalf("throttled test alert was not kept in settings: %d %s", code, body)
	}
}

type secretTestAlertError struct{}

func (*secretTestAlertError) Error() string {
	return `Post "https://ntfy.example/secret-topic": network failed`
}
