package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestServiceWatchdogRequiresWriterAndEventLoopProgress(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	writer := &aliveWriterFake{}
	handlers := &inFlight{}
	var sent []string
	w := serviceWatchdog{writer: writer, handlers: handlers, now: func() time.Time { return now }, notify: func(s string) error {
		sent = append(sent, s)
		return nil
	}}

	if err := w.tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	writer.err = errors.New("database stalled")
	if err := w.tick(t.Context()); err == nil {
		t.Fatal("writer failure did not fail the watchdog tick")
	}
	writer.err = nil
	handlers.mu.Lock()
	handlers.active = 1
	handlers.oldestStarted = now.Add(-2*time.Minute - time.Second)
	handlers.mu.Unlock()
	if err := w.tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(sent, []string{"WATCHDOG=1"}) {
		t.Fatalf("notifications=%v", sent)
	}
}

type aliveWriterFake struct{ err error }

func (f *aliveWriterFake) RecordAlive(context.Context, int64) error { return f.err }

func TestServiceUnitUsesNotifyWatchdogAndNetworkOrdering(t *testing.T) {
	unit := readUnit(t)
	for _, line := range []string{
		"Type=notify", "WatchdogSec=120", "Restart=always", "RestartSec=5",
		"After=network-online.target", "Wants=network-online.target",
	} {
		if !slices.Contains(unit, line) {
			t.Errorf("unit lacks %q", line)
		}
	}
}

func readUnit(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "deploy", "archiver.service"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(string(data), "\n")
}
