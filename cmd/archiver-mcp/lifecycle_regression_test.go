//go:build linux

package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestSymlinkLockFailsClosedWithoutTouchingTarget(t *testing.T) {
	config := cliFixture(t)
	target := filepath.Join(t.TempDir(), "target")
	sentinel := []byte("SYNTHETIC_PRIVATE_SENTINEL")
	if err := os.WriteFile(target, sentinel, 0600); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(filepath.Dir(config), "archiver-mcp.lock")
	if err := os.Symlink(target, lock); err != nil {
		t.Fatal(err)
	}
	var out, diag bytes.Buffer
	code := run(context.Background(), []string{"serve", "--config", config}, io.NopCloser(strings.NewReader("")), &out, &diag)
	if code == 0 || out.Len() != 0 || diag.String() != "ARCHIVE_UNAVAILABLE\n" {
		t.Fatal("symlink lock accepted or diagnostics disclosed details")
	}
	actual, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, sentinel) {
		t.Fatal("lock target changed")
	}
}

func TestOversizedStdioFrameExitsSafelyAndReleasesLock(t *testing.T) {
	config := cliFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var out, diag bytes.Buffer
	frame := `{"jsonrpc":"2.0","id":1,"method":"SYNTHETIC_SECRET_` + strings.Repeat("x", 130*1024) + `"}` + "\n"
	code := run(ctx, []string{"serve", "--config", config}, io.NopCloser(strings.NewReader(frame)), &out, &diag)
	if ctx.Err() != nil {
		t.Fatal("oversized frame failed to terminate promptly")
	}
	if code == 0 || out.Len() != 0 || diag.String() != "INVALID_ARGUMENT\n" {
		t.Fatal("oversized frame accepted or disclosed diagnostics")
	}
	var again, againDiag bytes.Buffer
	if code = run(context.Background(), []string{"serve", "--config", config}, io.NopCloser(strings.NewReader("")), &again, &againDiag); code != 0 {
		t.Fatal("oversized-frame exit retained lock")
	}
}

func TestCancellationAfterSDKHandshakeReleasesLock(t *testing.T) {
	config := cliFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	serverContext, stop := context.WithCancel(ctx)
	defer stop()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	defer inW.Close()
	defer outR.Close()
	done := make(chan int, 1)
	var diag bytes.Buffer
	go func() {
		code := run(serverContext, []string{"serve", "--config", config}, inR, outW, &diag)
		_ = outW.Close()
		done <- code
	}()
	client, err := mcp.NewClient(&mcp.Implementation{Name: "cancel-test", Version: "1"}, nil).Connect(ctx, &mcp.IOTransport{Reader: outR, Writer: inW}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Successful tools/list guarantees protocol initialization completed while the
	// server owns the lock. Cancellation has no timing/sleep assumption.
	if _, err = client.ListTools(ctx, nil); err != nil {
		t.Fatal(err)
	}
	stop()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("cancel exit=%d", code)
		}
	case <-ctx.Done():
		t.Fatal("initialized server failed to stop")
	}
	_ = client.Close()
	var out, againDiag bytes.Buffer
	if code := run(ctx, []string{"serve", "--config", config}, io.NopCloser(strings.NewReader("")), &out, &againDiag); code != 0 {
		t.Fatal("cancellation retained lock")
	}
}
