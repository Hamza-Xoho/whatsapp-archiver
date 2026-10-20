package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// Phase 2 FR-1: `run` on an unpaired folder stays up, says how to pair and pairs from the dashboard.
func TestRunWithoutSessionSaysPairFirst(t *testing.T) {
	data := t.TempDir()
	r := startRun(t, data, "")
	r.waitPairPage(t)
	code, errOut := r.stop(t)
	if code != 0 || !strings.Contains(errOut, "not paired: open the dashboard through the SSH tunnel and pair") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
}
func TestVersionPrints(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"version"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "whatsapp-archiver") {
		t.Fatalf("code=%d out=%q err=%q", code, out.String(), errOut.String())
	}
}

func TestVersionOutputUsesBuildStamp(t *testing.T) {
	saved := version
	t.Cleanup(func() { version = saved })
	version = "abc1234"
	var out, errOut bytes.Buffer
	if code := run([]string{"version"}, &out, &errOut); code != 0 {
		t.Fatalf("code=%d err=%q", code, errOut.String())
	}
	if !strings.HasPrefix(out.String(), "whatsapp-archiver abc1234\n") {
		t.Fatalf("out=%q, want first line %q", out.String(), "whatsapp-archiver abc1234")
	}
}

func writeConfig(t *testing.T, dir, ntfyURL, listen string) {
	t.Helper()
	cfg := "wa:\n  check_latest_version: false\nweb:\n  listen: " + listen + "\n"
	if ntfyURL != "" {
		cfg += "alerts:\n  ntfy:\n    url: " + ntfyURL + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
}

// countingSocket stands in for the WhatsApp connection: it counts connects and reaches nothing.
type countingSocket struct {
	mu sync.Mutex
	n  int
}

func (s *countingSocket) ConnectContext(context.Context) error {
	s.mu.Lock()
	s.n++
	s.mu.Unlock()
	return nil
}
func (s *countingSocket) Disconnect()   {}
func (s *countingSocket) connects() int { s.mu.Lock(); defer s.mu.Unlock(); return s.n }

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

// runRig is `archiver run` in a goroutine, on a fake socket, stopped by cancelling serve's context (the signal).
type runRig struct {
	base        string
	sock        *countingSocket
	cancel      context.CancelFunc
	done        chan int
	out, errOut bytes.Buffer
}

func startRun(t *testing.T, data, ntfyURL string) *runRig {
	t.Helper()
	addr := freeAddr(t)
	writeConfig(t, data, ntfyURL, addr)
	ctx, cancel := context.WithCancel(context.Background())
	r := &runRig{base: "http://" + addr, sock: &countingSocket{}, cancel: cancel, done: make(chan int, 1)}
	savedCtx, savedSock := serveContext, testSocket
	serveContext = func() context.Context { return ctx }
	testSocket = r.sock
	t.Cleanup(func() {
		cancel()
		serveContext, testSocket = savedCtx, savedSock
	})
	go func() { r.done <- run([]string{"run", "--data", data}, &r.out, &r.errOut) }()
	return r
}

// waitPairPage polls GET /pair until the dashboard answers, and fails if run returns first.
func (r *runRig) waitPairPage(t *testing.T) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case code := <-r.done:
			r.done <- code
			t.Fatalf("run returned %d before serving the dashboard; stderr=%q", code, r.errOut.String())
		default:
		}
		resp, err := http.Get(r.base + "/pair")
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return string(body)
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the dashboard did not serve GET /pair within 10 s")
	return ""
}

// stop sends the stop signal and returns run's exit code and stderr.
func (r *runRig) stop(t *testing.T) (int, string) {
	t.Helper()
	r.cancel()
	select {
	case code := <-r.done:
		return code, r.errOut.String()
	case <-time.After(15 * time.Second):
		t.Fatal("run did not return after the stop signal")
		return 0, ""
	}
}

// Phase 2 FR-1: an unpaired `run` sends the not-paired alert once, serves the pair page and connects to nothing until
// pairing is started from the dashboard.
func TestRunWithUnpairedSessionFileSaysPairFirst(t *testing.T) {
	var mu sync.Mutex
	var titles, bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		titles = append(titles, r.Header.Get("Title"))
		bodies = append(bodies, string(body))
		mu.Unlock()
	}))
	defer srv.Close()
	data := t.TempDir()
	// pair creates session.db before any scan; an interrupted pair or a logout leaves it unpaired.
	if err := os.WriteFile(filepath.Join(data, "session.db"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	r := startRun(t, data, srv.URL+"/topic")
	page := r.waitPairPage(t)
	if !strings.Contains(page, `action="/pair/start"`) || !strings.Contains(page, "Pair WhatsApp") {
		t.Fatalf("GET /pair has no Pair WhatsApp button:\n%s", page)
	}
	if n := r.sock.connects(); n != 0 {
		t.Fatalf("connects=%d while unpaired, want 0", n)
	}
	code, errOut := r.stop(t)
	if code != 0 {
		t.Fatalf("code=%d, want 0 (stopped by the signal); stderr=%q", code, errOut)
	}
	if !strings.Contains(errOut, "not paired: open the dashboard through the SSH tunnel and pair") {
		t.Fatalf("stderr=%q", errOut)
	}
	if n := r.sock.connects(); n != 0 {
		t.Fatalf("connects=%d after stopping, want 0", n)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(titles) != 1 || titles[0] != "WhatsApp Archiver is not paired" {
		t.Fatalf("ntfy titles=%q, want one not-paired alert", titles)
	}
	if bodies[0] != "Open the dashboard through the SSH tunnel and pair" {
		t.Fatalf("ntfy body=%q", bodies[0])
	}
}

// Phase 2 FR-1: without a session.db, `run` still serves the pair page and connects to nothing. (It now opens
// session.db, which pairing from the dashboard writes; before phase 2 `run` exited without creating it.)
func TestRunWithoutSessionServesThePairPage(t *testing.T) {
	data := t.TempDir()
	r := startRun(t, data, "")
	page := r.waitPairPage(t)
	if !strings.Contains(page, "Pair WhatsApp") {
		t.Fatalf("GET /pair:\n%s", page)
	}
	if code, errOut := r.stop(t); code != 0 || r.sock.connects() != 0 {
		t.Fatalf("code=%d connects=%d stderr=%q", code, r.sock.connects(), errOut)
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
