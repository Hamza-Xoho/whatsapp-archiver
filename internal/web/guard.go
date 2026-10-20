package web

import (
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// guard wraps every route. The UI has no login in phase 2 and is reached through an SSH tunnel, so it trusts only
// requests a page served by itself could make:
//   - Host must be localhost, 127.0.0.1 or [::1] with a port. A page on another site that rebinds its own name to
//     127.0.0.1 (DNS rebinding) still sends its own name as Host, and gets 403.
//   - A request that changes something (any method but GET and HEAD) must carry Origin, or failing that Referer,
//     equal to http://<Host>. A form on another site posting here gets 403.
//   - A request the browser marks Sec-Fetch-Site: cross-site gets 403 on every method, GET included, so another site
//     cannot make the browser load pages or open the event stream. Without the header (curl, old browsers) nothing
//     changes.
//
// Every response also forbids framing, so a foreign page cannot overlay the UI to trick a click.
func guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", "frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		if !loopbackHost(r.Host) {
			http.Error(w, "forbidden: open the dashboard as http://localhost:<port> through the SSH tunnel", http.StatusForbidden)
			return
		}
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			http.Error(w, "forbidden: this request came from another site", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !sameOrigin(r) {
			http.Error(w, "forbidden: this request did not come from the dashboard", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// loopbackHost reports whether host is localhost, 127.0.0.1 or [::1] followed by a numeric port.
func loopbackHost(host string) bool {
	name, port, err := net.SplitHostPort(host)
	if err != nil {
		return false
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return false
	}
	switch strings.ToLower(name) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

// sameOrigin reports whether the request's Origin, or its Referer when there is no Origin, is http://<Host>.
func sameOrigin(r *http.Request) bool {
	want := "http://" + r.Host
	if origin := r.Header.Get("Origin"); origin != "" {
		return origin == want
	}
	ref, err := url.Parse(r.Header.Get("Referer"))
	if err != nil || ref.Scheme != "http" || ref.User != nil {
		return false
	}
	return "http://"+ref.Host == want
}
