package web

import (
	"net/http"
	"strings"
	"testing"

	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

func TestLoggingOverviewRedactsUntrustedHealthDetail(t *testing.T) {
	d := newDash(t)
	d.snap.State = "disconnected"
	d.snap.Detail = "class=dns GET https://secret.invalid/private?credential=OVERVIEW_SECRET_91 /home/shakeel/private-file"
	body, code, _ := get(t, d.handler(), "/logging")
	if code != http.StatusOK {
		t.Fatalf("status %d: %s", code, body)
	}
	page := string(body)
	for _, secret := range []string{"OVERVIEW_SECRET_91", "secret.invalid", "/home/shakeel/private-file"} {
		if strings.Contains(page, secret) {
			t.Fatalf("overview leaks %q: %s", secret, page)
		}
	}
	if !strings.Contains(visibleText(page), "Connection interrupted (DNS)") {
		t.Fatalf("overview lacks controlled reason copy: %s", page)
	}
}

func TestLoggingMediaReasonsAreSafeDisplayCategories(t *testing.T) {
	d := newDash(t)
	reason := "dns failed at https://secret.invalid/private?token=MEDIA_SECRET_92 path=/home/shakeel/media.key"
	if err := testkit.InsertMedia(t.Context(), d.st, store.Media{ChatJID: "family@g.us", MessageID: "unsafe-reason",
		Source: "live", Type: "image", State: "failed", ErrorClass: "network", Reason: reason}); err != nil {
		t.Fatal(err)
	}
	body, code, _ := get(t, d.handler(), "/logging/gaps-media")
	if code != http.StatusOK {
		t.Fatalf("status %d: %s", code, body)
	}
	page := string(body)
	for _, secret := range []string{"MEDIA_SECRET_92", "secret.invalid", "/home/shakeel/media.key"} {
		if strings.Contains(page, secret) {
			t.Fatalf("media reason leaks %q: %s", secret, page)
		}
	}
	if !strings.Contains(visibleText(page), "DNS lookup failed") {
		t.Fatalf("media reason lacks safe category: %s", page)
	}
}
