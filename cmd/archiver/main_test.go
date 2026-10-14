package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
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

func writeConfig(t *testing.T, dir, ntfyURL string) {
	t.Helper()
	cfg := "wa:\n  check_latest_version: false\n"
	if ntfyURL != "" {
		cfg += "alerts:\n  ntfy:\n    url: " + ntfyURL + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRunWithUnpairedSessionFileSaysPairFirst(t *testing.T) {
	var mu sync.Mutex
	var titles []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		titles = append(titles, r.Header.Get("Title"))
		mu.Unlock()
	}))
	defer srv.Close()
	data := t.TempDir()
	writeConfig(t, data, srv.URL+"/topic")
	// pair creates session.db before any scan; an interrupted pair or a logout leaves it unpaired.
	if err := os.WriteFile(filepath.Join(data, "session.db"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := run([]string{"run", "--data", data}, &out, &errOut)
	if code != exitNotPaired || exitNotPaired != 3 {
		t.Fatalf("code=%d, want %d (=3)", code, exitNotPaired)
	}
	if !strings.Contains(errOut.String(), "not paired: run `archiver pair` in a terminal first") {
		t.Fatalf("stderr=%q", errOut.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(titles) != 1 || titles[0] != "WhatsApp Archiver is not paired" {
		t.Fatalf("ntfy titles=%q, want one not-paired alert", titles)
	}
}

func TestRunWithoutSessionDoesNotCreateIt(t *testing.T) {
	data := t.TempDir()
	writeConfig(t, data, "")
	var out, errOut bytes.Buffer
	if code := run([]string{"run", "--data", data}, &out, &errOut); code != exitNotPaired {
		t.Fatalf("code=%d stderr=%q", code, errOut.String())
	}
	if _, err := os.Stat(filepath.Join(data, "session.db")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("session.db created by run: %v", err)
	}
}

func TestServiceUnitDoesNotRestartWhenNotPaired(t *testing.T) {
	unit, err := os.ReadFile(filepath.Join("..", "..", "deploy", "archiver.service"))
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("RestartPreventExitStatus=%d", exitNotPaired)
	if !slices.Contains(strings.Split(string(unit), "\n"), want) {
		t.Fatalf("deploy/archiver.service lacks %q", want)
	}
}
