package wa

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"whatsapp-archiver/internal/config"
)

func TestWhatsmeowLogsReachSlog(t *testing.T) {
	var buf bytes.Buffer
	l := newWALogger(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	sub := l.Sub("Client").Sub("Socket")
	sub.Debugf("frame %d", 1)
	sub.Infof("info %s", "x")
	sub.Warnf("warn")
	l.Errorf("outdated %s", "2.3000.1")

	want := []struct{ level, msg, module string }{
		{"DEBUG", "frame 1", "Client/Socket"},
		{"INFO", "info x", "Client/Socket"},
		{"WARN", "warn", "Client/Socket"},
		{"ERROR", "outdated 2.3000.1", ""},
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != len(want) {
		t.Fatalf("got %d lines, want %d:\n%s", len(lines), len(want), buf.String())
	}
	for i, w := range want {
		var rec map[string]any
		if err := json.Unmarshal([]byte(lines[i]), &rec); err != nil {
			t.Fatal(err)
		}
		module, _ := rec["module"].(string)
		if rec["level"] != w.level || rec["msg"] != w.msg || module != w.module {
			t.Fatalf("line %d = %s, want %+v", i, lines[i], w)
		}
	}
}

func TestWhatsmeowRepeatedLinesAreCollapsedIntoFiveMinuteSummaries(t *testing.T) {
	var buf bytes.Buffer
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	l := newWALoggerAt(slog.New(slog.NewJSONHandler(&buf, nil)), func() time.Time { return now }).Sub("Socket")
	l.Warnf("keepalive timed out")
	l.Warnf("keepalive timed out")
	l.Warnf("keepalive timed out")
	now = now.Add(5 * time.Minute)
	l.Warnf("keepalive timed out")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want first plus summary:\n%s", len(lines), buf.String())
	}
	var first, summary map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &summary); err != nil {
		t.Fatal(err)
	}
	if first["msg"] != "keepalive timed out" || summary["msg"] != "repeated 3 times in 5 m" || summary["module"] != "Socket" || summary["level"] != "WARN" {
		t.Fatalf("unexpected collapsed lines:\n%s", buf.String())
	}
}

// whatsmeow logs an empty About text as a WARN; it is harmless and ignored by us, so it is Debug.
func TestBenignWhatsmeowWarnIsDebug(t *testing.T) {
	var buf bytes.Buffer
	l := newWALogger(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	l.Sub("Client").Warnf("Set status notification has unexpected content (%v)", nil)
	l.Warnf("something else %d", 1)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	var got []string
	for _, line := range lines {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatal(err)
		}
		got = append(got, rec["level"].(string))
	}
	if len(got) != 2 || got[0] != "DEBUG" || got[1] != "WARN" {
		t.Fatalf("levels = %v, want [DEBUG WARN]:\n%s", got, buf.String())
	}
}

func TestOpenWiresWhatsmeowLogger(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	cfg := &config.Config{DataDir: t.TempDir(), History: config.HistoryRules{DaysLimit: 1, SizeMBLimit: 1}}
	c, err := Open(t.Context(), cfg, log)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.client.Log.Warnf("probe %d", 7)
	if !strings.Contains(buf.String(), `"msg":"probe 7"`) {
		t.Fatalf("whatsmeow client log did not reach slog: %q", buf.String())
	}
}

func TestWhatsmeowLinesAreRedacted(t *testing.T) {
	var buf bytes.Buffer
	l := newWALogger(slog.New(slog.NewJSONHandler(&buf, nil))).Sub("Client")
	l.Warnf("Failed to download %s: %v", "https://mmg.whatsapp.net/v/t62/a.enc?ccb=11-4&oh=01_SECRET&oe=65F0", "403")
	out := buf.String()
	if strings.Contains(out, "SECRET") || strings.Contains(out, "ccb=") {
		t.Fatalf("signed query reached slog: %s", out)
	}
	if !strings.Contains(out, "https://mmg.whatsapp.net/v/t62/a.enc?REDACTED") {
		t.Fatalf("host and path were lost: %s", out)
	}
}
