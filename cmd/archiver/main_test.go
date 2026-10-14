package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunWithoutSessionSaysPairFirst(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{"run", "--data", t.TempDir()}, &out, &errOut)
	if code == 0 || !strings.Contains(errOut.String(), "pair") {
		t.Fatalf("code=%d stderr=%q", code, errOut.String())
	}
}
func TestVersionPrints(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"version"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "whatsapp-archiver") {
		t.Fatalf("code=%d out=%q err=%q", code, out.String(), errOut.String())
	}
}
