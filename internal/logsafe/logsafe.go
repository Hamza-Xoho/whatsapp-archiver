// Package logsafe keeps secrets out of log lines. It imports only the standard library.
package logsafe

import "regexp"

// signedURL matches an http(s) URL up to its query or fragment; group 1 is the part that is kept.
var signedURL = regexp.MustCompile(`(https?://[^\s?#"'<>]*)[?#][^\s"'<>]*`)

// RedactURLs replaces the query string and fragment of every http(s) URL in s with "?REDACTED". WhatsApp's CDN URLs
// carry their authorisation in the query, so they must never reach the journal. Scheme, host and path stay.
func RedactURLs(s string) string { return signedURL.ReplaceAllString(s, "${1}?REDACTED") }
