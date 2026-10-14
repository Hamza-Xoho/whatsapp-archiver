package wa

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

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
