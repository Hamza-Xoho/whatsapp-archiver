package web

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The vendored htmx files are pinned: these are the SHA-256 sums of htmx.org@2.0.4/dist/htmx.min.js and
// htmx-ext-sse@2.2.2/sse.js as extracted from the npm registry tarballs
// (https://registry.npmjs.org/htmx.org/-/htmx.org-2.0.4.tgz, tarball sha1 74fce66b177eb59c6d251ecf1052a2478743bec9;
// https://registry.npmjs.org/htmx-ext-sse/-/htmx-ext-sse-2.2.2.tgz, tarball sha1 f310696d11944b0f9f28e2ab186f5cd78bd310d9).
// The test hashes what the server actually serves from its embedded files.
func TestVendoredAssetsMatchPinnedHashes(t *testing.T) {
	h := New(Deps{}).Handler()
	for path, want := range map[string]string{
		"/static/htmx.min.js": "e209dda5c8235479f3166defc7750e1dbcd5a5c1808b7792fc2e6733768fb447",
		"/static/sse.js":      "83eca6fa0611fe2b0bf1700b424b88b5eced38ef448ef9760a2ea08fbc875611",
	} {
		body, code, ctype := get(t, h, path)
		if code != http.StatusOK {
			t.Fatalf("%s: status %d", path, code)
		}
		sum := sha256.Sum256(body)
		if got := hex.EncodeToString(sum[:]); got != want {
			t.Errorf("%s: sha256 %s, want %s", path, got, want)
		}
		if !strings.Contains(ctype, "javascript") {
			t.Errorf("%s: Content-Type %q", path, ctype)
		}
	}
}

func TestStylesheetIsServed(t *testing.T) {
	body, code, ctype := get(t, New(Deps{}).Handler(), "/static/app.css")
	if code != http.StatusOK || !strings.HasPrefix(ctype, "text/css") {
		t.Fatalf("status %d, Content-Type %q", code, ctype)
	}
	if !strings.Contains(string(body), "prefers-color-scheme: dark") {
		t.Fatal("app.css has no dark colours")
	}
}

// The layout loads the vendored files from this server, never a CDN, and opens the one SSE stream.
func TestLayoutLoadsVendoredAssetsAndOpensTheStream(t *testing.T) {
	body, code, ctype := get(t, New(Deps{}).Handler(), "/")
	if code != http.StatusOK || !strings.HasPrefix(ctype, "text/html") {
		t.Fatalf("status %d, Content-Type %q", code, ctype)
	}
	page := string(body)
	for _, want := range []string{`src="/static/htmx.min.js"`, `src="/static/sse.js"`, `href="/static/app.css"`, `hx-ext="sse"`, `sse-connect="/events"`, `sse-swap="activity"`} {
		if !strings.Contains(page, want) {
			t.Errorf("layout lacks %s", want)
		}
	}
	if strings.Contains(page, "https://") || strings.Contains(page, "//unpkg") || strings.Contains(page, "cdn") {
		t.Error("layout loads something from another site")
	}
}

func TestUnknownPathIs404(t *testing.T) {
	for _, path := range []string{"/nope", "/static/", "/static/missing.js", "/events/x"} {
		if _, code, _ := get(t, New(Deps{}).Handler(), path); code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, code)
		}
	}
}

func get(t *testing.T, h http.Handler, path string) ([]byte, int, string) {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Host = "localhost:8080"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	body, err := io.ReadAll(w.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	return body, w.Code, w.Header().Get("Content-Type")
}
