//go:build linux

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// The helper executes the actual CLI entry point with inherited, blocking stdio
// descriptors, rather than io.Pipe or os.Pipe descriptors created in the server.
func TestMCPBackpressureProcessHelper(t *testing.T) {
	if os.Getenv("ARCHIVER_MCP_BACKPRESSURE_HELPER") != "1" {
		return
	}
	os.Args = []string{"archiver-mcp", "serve", "--config", os.Getenv("ARCHIVER_MCP_BACKPRESSURE_CONFIG")}
	main()
}

// A pending response must not prevent shutdown or retain the reader lock when
// the client stops draining stdout. Removing either output interruption or
// pollable ownership of inherited pipe descriptors breaks this contract.
func TestBackpressuredProcessShutdownReleasesLock(t *testing.T) {
	for _, shutdown := range []string{"SIGTERM", "stdin EOF"} {
		t.Run(shutdown, func(t *testing.T) {
			dir := t.TempDir()
			archive := filepath.Join(dir, "absent.db")
			config := filepath.Join(dir, "config.yaml")
			if err := os.WriteFile(config, []byte(fmt.Sprintf("archive: %s\nruntime_dir: %s\npolicy:\n  mode: selected\n  allow_chats: []\n", archive, dir)), 0600); err != nil {
				t.Fatal(err)
			}
			outR, outW, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer outR.Close()
			defer outW.Close()
			capacity, err := unix.FcntlInt(outW.Fd(), unix.F_SETPIPE_SZ, 4096)
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestMCPBackpressureProcessHelper$")
			cmd.Env = append(os.Environ(), "ARCHIVER_MCP_BACKPRESSURE_HELPER=1", "ARCHIVER_MCP_BACKPRESSURE_CONFIG="+config)
			in, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer in.Close()
			var diagnostics bytes.Buffer
			cmd.Stdout, cmd.Stderr = outW, &diagnostics
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			_ = outW.Close()
			done := make(chan struct{})
			var waitErr error
			go func() { waitErr = cmd.Wait(); close(done) }()
			t.Cleanup(func() { _ = cmd.Process.Kill(); <-done })
			if _, err = fmt.Fprintln(in, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"backpressure-test","version":"1"}}}`); err != nil {
				t.Fatal(err)
			}
			reply := make(chan string, 1)
			go func() { line, _ := bufio.NewReader(outR).ReadString('\n'); reply <- line }()
			select {
			case line := <-reply:
				if !json.Valid([]byte(line)) || !strings.Contains(line, `"result"`) || strings.Contains(line, `"error"`) {
					t.Fatalf("initialize did not succeed: %q", line)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("initialize did not reply")
			}
			if _, err = fmt.Fprintln(in, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); err != nil {
				t.Fatal(err)
			}
			for id := 2; id < 22; id++ {
				// A valid string ID makes each response exceed PIPE_BUF, so the
				// kernel can fill the pipe even when a shorter frame fits once.
				if _, err = fmt.Fprintf(in, "{\"jsonrpc\":\"2.0\",\"id\":\"%s%d\",\"method\":\"tools/list\"}\n", strings.Repeat("r", 256), id); err != nil {
					t.Fatal(err)
				}
			}
			// No stdout is read after initialize. Wait for the kernel to confirm
			// a full pipe, rather than assuming a sleep produced backpressure.
			deadline := time.Now().Add(5 * time.Second)
			for {
				pending, pipeErr := unix.IoctlGetInt(int(outR.Fd()), unix.TIOCINQ)
				if pipeErr != nil {
					t.Fatal(pipeErr)
				}
				if pending == capacity {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("stdout did not fill: %d/%d bytes", pending, capacity)
				}
				time.Sleep(time.Millisecond)
			}
			if shutdown == "SIGTERM" {
				err = cmd.Process.Signal(syscall.SIGTERM)
			} else {
				err = in.Close()
			}
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-done:
				if waitErr != nil || diagnostics.Len() != 0 {
					t.Fatalf("shutdown failed: %v, stderr=%q", waitErr, diagnostics.String())
				}
			case <-time.After(3 * time.Second):
				t.Fatal("backpressured process did not exit; reader lock remains held")
			}
			lock, err := acquireLock(dir)
			if err != nil {
				t.Fatalf("shutdown retained reader lock: %v", err)
			}
			_ = lock.Close()
			if _, err = os.Stat(archive); !os.IsNotExist(err) {
				t.Fatal("discovery created or required an archive")
			}
		})
	}
}
