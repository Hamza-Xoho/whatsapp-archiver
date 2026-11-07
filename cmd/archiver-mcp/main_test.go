//go:build linux

package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	_ "modernc.org/sqlite"
)

func cliFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	archive := filepath.Join(dir, "archive.db")
	db, err := sql.Open("sqlite", archive)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := os.ReadFile("../../internal/store/testdata/pre_v1/0001_raw.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(schema)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO chats(jid,name,type) VALUES('synthetic@g.us','Synthetic catalog chat','group')"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO messages(chat_jid,id,ts,received_at,source,text,raw_proto) VALUES('synthetic@g.us','m',1,1,'live','PRIVATE_SYNTHETIC_BODY',X'')"); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, "config.yaml")
	if err = os.WriteFile(config, []byte(fmt.Sprintf("archive: %s\nruntime_dir: %s\npolicy:\n  mode: selected\n  allow_chats: []\n", archive, dir)), 0600); err != nil {
		t.Fatal(err)
	}
	return config
}

func TestOperatorCommandsAndRedactedFailures(t *testing.T) {
	config := cliFixture(t)
	for _, command := range []string{"inspect", "catalog", "version"} {
		args := []string{command}
		if command != "version" {
			args = append(args, "--config", config)
		}
		var out, diag bytes.Buffer
		if code := run(context.Background(), args, io.NopCloser(strings.NewReader("")), &out, &diag); code != 0 {
			t.Fatalf("%s: %d %s", command, code, diag.String())
		}
		if strings.Contains(out.String(), "PRIVATE_SYNTHETIC_BODY") {
			t.Fatal("message body in operator command")
		}
		if command == "catalog" && !strings.Contains(out.String(), "Synthetic catalog chat") {
			t.Fatal("catalog omitted chat")
		}
		if command != "version" && !json.Valid(out.Bytes()) {
			t.Fatalf("invalid JSON: %s", out.String())
		}
	}
	for _, args := range [][]string{{"inspect", "--config", config + "-SECRET_PATH"}, {"serve", "--config", config, "--unknown", "SECRET_ARG"}, {"catalog", "--config", config, "extra"}} {
		var out, diag bytes.Buffer
		if code := run(context.Background(), args, io.NopCloser(strings.NewReader("")), &out, &diag); code == 0 {
			t.Fatal("bad command succeeded")
		}
		if out.Len() != 0 || strings.Contains(diag.String(), "SECRET") || strings.Contains(diag.String(), config) {
			t.Fatal("unsafe CLI error")
		}
	}
}

func TestStdioProtocolProcessLockAndEOF(t *testing.T) {
	config := cliFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	defer inW.Close()
	defer outR.Close()
	done := make(chan int, 1)
	var diag bytes.Buffer
	go func() { done <- run(ctx, []string{"serve", "--config", config}, inR, outW, &diag); _ = outW.Close() }()
	client, err := mcp.NewClient(&mcp.Implementation{Name: "stdio-test", Version: "1"}, nil).Connect(ctx, &mcp.IOTransport{Reader: outR, Writer: inW}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tools, err := client.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 8 {
		t.Fatalf("list: %v", err)
	}
	r, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "archive_status", Arguments: map[string]any{}})
	if err != nil || r.IsError {
		t.Fatalf("status: %v", err)
	}
	var secondOut, secondDiag bytes.Buffer
	if code := run(ctx, []string{"serve", "--config", config}, io.NopCloser(strings.NewReader("")), &secondOut, &secondDiag); code == 0 {
		t.Fatal("second process accepted")
	}
	if secondOut.Len() != 0 || !strings.Contains(secondDiag.String(), "BUSY") {
		t.Fatal("lock failure not safe")
	}
	_ = client.Close()
	_ = inW.Close()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("EOF code %d: %s", code, diag.String())
		}
	case <-ctx.Done():
		t.Fatal("EOF leaked server")
	}
	var out, errout bytes.Buffer
	if code := run(ctx, []string{"serve", "--config", config}, io.NopCloser(strings.NewReader("")), &out, &errout); code != 0 {
		t.Fatalf("lock not released: %s", errout.String())
	}
	if out.Len() != 0 {
		t.Fatal("stdout contains nonprotocol output")
	}
}

func TestServeCancellationReleasesLock(t *testing.T) {
	config := cliFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	inR, inW := io.Pipe()
	defer inW.Close()
	done := make(chan int, 1)
	var out, diag bytes.Buffer
	go func() { done <- run(ctx, []string{"serve", "--config", config}, inR, &out, &diag) }()
	// A successful client initialize proves the server owns its lock before cancel.
	time.AfterFunc(50*time.Millisecond, cancel)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation blocked")
	}
	var again, againDiag bytes.Buffer
	if code := run(context.Background(), []string{"serve", "--config", config}, io.NopCloser(strings.NewReader("")), &again, &againDiag); code != 0 {
		t.Fatalf("lock retained: %s", againDiag.String())
	}
}
