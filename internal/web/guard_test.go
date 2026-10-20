package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The UI has no login and is reached through an SSH tunnel: a page on another site must not reach it, either by
// DNS rebinding (a foreign Host) or by a cross-site form post (a foreign Origin).
func TestRejectsForeignHost(t *testing.T) {
	h := New(Deps{}).Handler()
	for _, host := range []string{"evil.example", "evil.example:8080", "localhost", "127.0.0.1", "127.0.0.2:8080",
		"localhost.evil.example:8080", "[::2]:8080", "localhost:", "localhost:80a", ""} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Host = host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Errorf("Host %q: status %d, want 403", host, w.Code)
		}
	}
	for _, host := range []string{"localhost:8080", "127.0.0.1:8080", "[::1]:8080", "LOCALHOST:9000"} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Host = host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("Host %q: status %d, want 200", host, w.Code)
		}
	}
}

// okHandler records whether the guard let the request through.
type okHandler struct{ called bool }

func (o *okHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	o.called = true
	w.WriteHeader(http.StatusOK)
}

func post(t *testing.T, origin, referer string) (int, bool) {
	t.Helper()
	next := &okHandler{}
	r := httptest.NewRequest(http.MethodPost, "/settings", nil)
	r.Host = "localhost:8080"
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if referer != "" {
		r.Header.Set("Referer", referer)
	}
	w := httptest.NewRecorder()
	guard(next).ServeHTTP(w, r)
	return w.Code, next.called
}

func TestPostWithoutMatchingOriginIs403(t *testing.T) {
	for _, c := range []struct{ origin, referer string }{
		{"", ""},                                                  // neither header
		{"http://evil.example", ""},                               // another site
		{"https://localhost:8080", ""},                            // another scheme
		{"http://localhost:8081", ""},                             // another port
		{"http://127.0.0.1:8080", ""},                             // another host name for the same address
		{"null", ""},                                              // sandboxed or privacy-stripped origin
		{"", "http://evil.example/page"},                          // a foreign Referer
		{"", "http://localhost:8080.evil.ex/"},                    // a Referer that only starts with the host
		{"", "not a url\x7f"},                                     // an unparsable Referer
		{"http://evil.example", "http://localhost:8080/settings"}, // Origin wins over Referer
	} {
		code, called := post(t, c.origin, c.referer)
		if code != http.StatusForbidden || called {
			t.Errorf("Origin %q Referer %q: status %d, handler called %v; want 403 and not called", c.origin, c.referer, code, called)
		}
	}
	// Every unsafe method is checked, not only POST.
	for _, m := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
		next := &okHandler{}
		r := httptest.NewRequest(m, "/settings", nil)
		r.Host = "localhost:8080"
		r.Header.Set("Origin", "http://evil.example")
		w := httptest.NewRecorder()
		guard(next).ServeHTTP(w, r)
		if w.Code != http.StatusForbidden || next.called {
			t.Errorf("%s from another origin: status %d, handler called %v", m, w.Code, next.called)
		}
	}
}

func TestPostWithSameOriginPasses(t *testing.T) {
	for _, c := range []struct{ origin, referer string }{
		{"http://localhost:8080", ""},
		{"", "http://localhost:8080/settings?x=1"},
		{"http://localhost:8080", "http://localhost:8080/"},
	} {
		code, called := post(t, c.origin, c.referer)
		if code != http.StatusOK || !called {
			t.Errorf("Origin %q Referer %q: status %d, handler called %v; want 200 and called", c.origin, c.referer, code, called)
		}
	}
}

// A GET needs no Origin: a link followed or a page reloaded carries none.
func TestGetNeedsNoOrigin(t *testing.T) {
	next := &okHandler{}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Host = "127.0.0.1:8080"
	w := httptest.NewRecorder()
	guard(next).ServeHTTP(w, r)
	if w.Code != http.StatusOK || !next.called {
		t.Fatalf("status %d, called %v", w.Code, next.called)
	}
}

// With no login, a foreign page must not frame the UI to trick a click (clickjacking).
func TestResponsesForbidFraming(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Host = "localhost:8080"
	w := httptest.NewRecorder()
	New(Deps{}).Handler().ServeHTTP(w, r)
	if got := w.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options %q, want DENY", got)
	}
	if got := w.Header().Get("Content-Security-Policy"); got != "frame-ancestors 'none'" {
		t.Errorf("Content-Security-Policy %q", got)
	}
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options %q, want nosniff", got)
	}
}
