package logsafe

import (
	"strings"
	"testing"
)

func TestRedactURLsRemovesQuery(t *testing.T) {
	in := "download failed: https://mmg.whatsapp.net/v/t62.7117-24/12345_n.enc?ccb=11-4&oh=01_AbCdEf&oe=65F0A1B2&_nc_sid=5e03e0#frag 403"
	want := "download failed: https://mmg.whatsapp.net/v/t62.7117-24/12345_n.enc?REDACTED 403"
	if got := RedactURLs(in); got != want {
		t.Fatalf("RedactURLs = %q, want %q", got, want)
	}
	if got := RedactURLs("see https://h.example/a/b#token=SECRET now"); got != "see https://h.example/a/b?REDACTED now" {
		t.Fatalf("fragment only: %q", got)
	}
	if got := RedactURLs("http://h.example/a?x=1 and https://i.example/b?y=2"); got != "http://h.example/a?REDACTED and https://i.example/b?REDACTED" {
		t.Fatalf("two URLs: %q", got)
	}
}

func TestRedactURLsKeepsNonURLText(t *testing.T) {
	for _, s := range []string{
		"",
		"plain text with a question? and a # sign",
		"1234567890@s.whatsapp.net",
		"123456789-1700000000@g.us",
		"open /var/lib/archiver/archive.db?mode=ro: busy",
		"dial tcp web.whatsapp.com:443: i/o timeout",
		"https://mmg.whatsapp.net/v/t62/file.enc",
	} {
		if got := RedactURLs(s); got != s {
			t.Fatalf("RedactURLs(%q) = %q, want it unchanged", s, got)
		}
	}
}

// F7: the topic is the secret on ntfy.sh, so the configured URL's path never reaches a log line.
func TestRedactNtfyReplacesTheTopic(t *testing.T) {
	const cfg = "https://ntfy.sh/secret-topic-123"
	in := `Post "https://ntfy.sh/secret-topic-123": dial tcp: lookup ntfy.sh: no such host; retry /secret-topic-123 failed`
	got := RedactNtfy(in, cfg)
	if strings.Contains(got, "secret-topic-123") {
		t.Fatalf("RedactNtfy kept the topic: %q", got)
	}
	if !strings.Contains(got, "https://ntfy.sh/REDACTED") || !strings.Contains(got, "no such host") {
		t.Fatalf("RedactNtfy = %q, want the host and the rest of the error kept", got)
	}
	for _, c := range []struct{ s, url string }{{"no ntfy here", cfg}, {"ntfy status 403 Forbidden", cfg}, {in, ""}, {in, "https://ntfy.sh"}, {in, "https://ntfy.sh/"}} {
		if got := RedactNtfy(c.s, c.url); got != c.s {
			t.Errorf("RedactNtfy(%q, %q) = %q, want it unchanged", c.s, c.url, got)
		}
	}
}
