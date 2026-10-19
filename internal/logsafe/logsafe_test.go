package logsafe

import "testing"

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
