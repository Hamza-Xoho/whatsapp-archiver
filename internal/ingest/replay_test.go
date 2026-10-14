package ingest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"whatsapp-archiver/internal/ingest"
)

func TestRecordLoadMessageRoundTrip(t *testing.T) {
	dir := t.TempDir()
	var got any
	r := ingest.NewRecorder(dir)
	r.Wrap(func(v any) { got = v })(messageEvent("123@lid", "A", text("hello")))
	events, err := ingest.Load(dir)
	if err != nil || len(events) != 1 {
		t.Fatalf("events=%d err=%v", len(events), err)
	}
	if got == nil {
		t.Fatal("handler not called")
	}
}
func TestScrubRemovesIdentifiers(t *testing.T) {
	in := t.TempDir()
	out := t.TempDir()
	p := filepath.Join(in, "x.json")
	if err := os.WriteFile(p, []byte(`{"jid":"123456@s.whatsapp.net","lid":"98765@lid"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ingest.Scrub(in, out); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(out, "x.json"))
	if strings.Contains(string(data), "123456") || strings.Contains(string(data), "98765") {
		t.Fatal(string(data))
	}
}
