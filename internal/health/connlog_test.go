package health

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"
	"whatsapp-archiver/internal/config"
)

func TestConnectionLinesOnlyOnTransition(t *testing.T) {
	var buf bytes.Buffer
	c := &clock{time.Unix(1_700_000_000, 0)}
	m := NewMonitor(&config.Config{DataDir: t.TempDir()}, &note{}, c, nil, func() string { return "2.3000.1" }).
		WithLogger(slog.New(slog.NewJSONHandler(&buf, nil)))
	m.SetState("connected", "") // first connect after start: nothing to report
	m.SetState("connected", "")
	c.now = c.now.Add(time.Minute)
	m.SetState("disconnected", "network")
	c.now = c.now.Add(90 * time.Second)
	m.SetState("disconnected", "network again")
	c.now = c.now.Add(30 * time.Second)
	m.SetState("connected", "")

	var recs []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var r map[string]any
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatal(err)
		}
		recs = append(recs, r)
	}
	if len(recs) != 2 {
		t.Fatalf("got %d lines, want lost then restored:\n%s", len(recs), buf.String())
	}
	if recs[0]["msg"] != "connection lost" || recs[0]["level"] != "INFO" || recs[0]["state"] != "disconnected" || recs[0]["detail"] != "network" {
		t.Fatalf("lost line = %v", recs[0])
	}
	if recs[1]["msg"] != "connection restored" || recs[1]["level"] != "INFO" || recs[1]["down_for"] != "2m0s" {
		t.Fatalf("restored line = %v", recs[1])
	}
}
