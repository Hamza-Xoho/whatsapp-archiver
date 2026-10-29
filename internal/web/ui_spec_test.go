package web

import (
	"os"
	"strings"
	"testing"
)

func TestUIHistoryStallCopyUsesPlanThreshold(t *testing.T) {
	spec, err := os.ReadFile("../../docs/v1/ui.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(spec), "no new chunk for 48 hours") {
		t.Fatal("UI spec must not label history stalled before the plan's 48-hour threshold")
	}
}
