// Package logsafe keeps secrets out of log lines. It imports only the standard library.
package logsafe

import (
	"net/url"
	"regexp"
	"strings"
)

// signedURL matches an http(s) URL up to its query or fragment; group 1 is the part that is kept.
var signedURL = regexp.MustCompile(`(https?://[^\s?#"'<>]*)[?#][^\s"'<>]*`)

// RedactURLs replaces the query string and fragment of every http(s) URL in s with "?REDACTED". WhatsApp's CDN URLs
// carry their authorisation in the query, so they must never reach the journal. Scheme, host and path stay.
func RedactURLs(s string) string { return signedURL.ReplaceAllString(s, "${1}?REDACTED") }

// RedactNtfy replaces the configured ntfy URL's path, the topic that works as a password on ntfy.sh, with "/REDACTED"
// wherever it appears in s. A URL with no topic (no path) leaves s unchanged. Stub: not yet implemented.
func RedactNtfy(s string, ntfyURL string) string {
	_, _, _ = url.Parse, strings.ReplaceAll, ntfyURL
	return s
}
