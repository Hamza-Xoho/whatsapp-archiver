package main

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"sync"
	"testing"
	"time"

	"whatsapp-archiver/internal/wa"
	"whatsapp-archiver/internal/web"
)

type fakeWAPairer struct {
	mu     sync.Mutex
	paired bool
	ch     chan wa.PairUpdate
}

func (f *fakeWAPairer) Paired() bool { f.mu.Lock(); defer f.mu.Unlock(); return f.paired }
func (f *fakeWAPairer) StartPairing(context.Context) (<-chan wa.PairUpdate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ch, nil
}

// The adapter copies every update field for field, closes after wa does, and gives a second call during the same
// pairing the same channel.
func TestBrowserPairerForwardsAndReusesTheChannel(t *testing.T) {
	f := &fakeWAPairer{ch: make(chan wa.PairUpdate, 4)}
	b := &browserPairer{p: f}
	ch1, err := b.StartPairing(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ch2, _ := b.StartPairing(t.Context())
	if ch1 != ch2 {
		t.Fatal("second StartPairing during the same pairing returned a new channel")
	}
	f.ch <- wa.PairUpdate{QR: "2@a"}
	f.ch <- wa.PairUpdate{Err: "Pairing failed: x"}
	f.ch <- wa.PairUpdate{Done: true}
	close(f.ch)
	var got []web.PairUpdate
	for u := range ch1 {
		got = append(got, u)
	}
	want := []web.PairUpdate{{QR: "2@a"}, {Err: "Pairing failed: x"}, {Done: true}}
	if len(got) != len(want) {
		t.Fatalf("updates=%+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("updates=%+v, want %+v", got, want)
		}
	}
	f.mu.Lock()
	f.ch = make(chan wa.PairUpdate, 1)
	f.paired = true
	f.mu.Unlock()
	ch3, _ := b.StartPairing(t.Context())
	if ch3 == ch1 {
		t.Fatal("a new pairing reused the ended channel")
	}
	if !b.Paired() {
		t.Fatal("Paired not passed through")
	}
	close(f.ch)
}

// Capture's own loops wait for pairing: nothing runs before ready, and a stop before then returns at once.
func TestAfterPairedWaitsForReady(t *testing.T) {
	ready := make(chan struct{})
	ran := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); afterPaired(ready, func(context.Context) { ran <- struct{}{} })(ctx) }()
	select {
	case <-ran:
		t.Fatal("loop ran before pairing")
	case <-time.After(50 * time.Millisecond):
	}
	close(ready)
	select {
	case <-ran:
	case <-time.After(5 * time.Second):
		t.Fatal("loop did not run once paired")
	}
	<-done
	cancel()
	stopped := make(chan struct{})
	go func() { defer close(stopped); afterPaired(make(chan struct{}), func(context.Context) {})(ctx) }()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("afterPaired did not return on stop")
	}
}

// serve gates the media queue and the backup schedule on pairing, never the monitor or the dashboard.
func TestServeGatesQueueAndBackupOnPairing(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	gated := map[string]bool{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "serve" {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "afterPaired" && len(call.Args) == 2 {
				ast.Inspect(call.Args[1], func(m ast.Node) bool {
					if sel, ok := m.(*ast.SelectorExpr); ok {
						if x, ok := sel.X.(*ast.Ident); ok {
							gated[x.Name+"."+sel.Sel.Name] = true
						}
					}
					return true
				})
			}
			return true
		})
	}
	if !gated["queue.Run"] || !gated["backup.Schedule"] || gated["mon.Run"] || gated["ui.Run"] {
		t.Fatalf("gated on pairing: %v; want queue.Run and backup.Schedule only", gated)
	}
}

// FR-1 end to end on a fake socket: an unpaired run connects only once the dashboard starts pairing, once.
func TestRunConnectsOnlyAfterPairingIsStarted(t *testing.T) {
	r := startRun(t, t.TempDir(), "")
	r.waitPairPage(t)
	if n := r.sock.connects(); n != 0 {
		t.Fatalf("connects=%d before pairing, want 0", n)
	}
	for range 2 {
		req, err := http.NewRequest(http.MethodPost, r.base+"/pair/start", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Origin", r.base)
		client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("POST /pair/start: %d", resp.StatusCode)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for r.sock.connects() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	if n := r.sock.connects(); n != 1 {
		t.Fatalf("connects=%d after starting pairing twice, want 1", n)
	}
	if code, errOut := r.stop(t); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
}
