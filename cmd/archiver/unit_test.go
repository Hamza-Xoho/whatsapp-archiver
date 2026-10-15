package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestServiceUnitCanWriteDataAndBackupFolders(t *testing.T) {
	unit, err := os.ReadFile(filepath.Join("..", "..", "deploy", "archiver.service"))
	if err != nil {
		t.Fatal(err)
	}
	guide, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	var writable []string
	for _, line := range strings.Split(string(unit), "\n") {
		if rest, ok := strings.CutPrefix(line, "ReadWritePaths="); ok {
			writable = append(writable, strings.Fields(rest)...)
		}
	}
	data := regexp.MustCompile(`--data (\S+)`).FindSubmatch(unit)
	dest := regexp.MustCompile(`destination: "([^"]+)"`).FindSubmatch(guide)
	if data == nil || dest == nil {
		t.Fatalf("data folder %q or backup destination %q not found", data, dest)
	}
	for _, want := range []string{string(data[1]), string(dest[1])} {
		found := false
		for _, p := range writable {
			found = found || p == want
		}
		if !found {
			t.Errorf("ReadWritePaths %v lacks %s", writable, want)
		}
	}
}
