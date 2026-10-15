package main

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"sync"
	"testing"
	"time"
)

// trace records what the fakes did, in order.
type trace struct {
	mu sync.Mutex
	ev []string
}

func (t *trace) add(s string) { t.mu.Lock(); t.ev = append(t.ev, s); t.mu.Unlock() }
func (t *trace) events() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.Clone(t.ev)
}

// slowLoop is a background loop that takes a moment to stop once its context ends, so a missing join shows.
func slowLoop(tr *trace, name string) func(context.Context) {
	return func(ctx context.Context) {
		<-ctx.Done()
		time.Sleep(30 * time.Millisecond)
		tr.add(name + " stopped")
	}
}
func closer(tr *trace, name string, err error) func() error {
	return func() error { tr.add("close " + name); return err }
}

func runAsync(f func() error) <-chan error {
	done := make(chan error, 1)
	go func() { done <- f() }()
	return done
}
func within(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("runService did not return: a loop or the client was not stopped")
		return nil
	}
}

// M5: on shutdown the client's Run returns, every background loop is joined, and only then are the client and the
// store closed, in that order.
func TestRunServiceJoinsLoopsBeforeClosingClientThenStore(t *testing.T) {
	var tr trace
	sigterm, stop := context.WithCancel(t.Context())
	ctx, cancel := context.WithCancel(sigterm)
	run := func(ctx context.Context) error { <-ctx.Done(); tr.add("run returned"); return nil }
	loops := []func(context.Context){slowLoop(&tr, "queue"), slowLoop(&tr, "monitor"), slowLoop(&tr, "backup")}
	done := runAsync(func() error {
		return runService(ctx, cancel, run, loops, closer(&tr, "client", nil), closer(&tr, "store", nil))
	})
	stop()
	if err := within(t, done); err != nil {
		t.Fatal(err)
	}
	got := tr.events()
	closeAt := slices.Index(got, "close client")
	if len(got) != 6 || closeAt != 4 || got[5] != "close store" || !slices.Contains(got[:4], "run returned") {
		t.Fatalf("order = %v, want run and the three loops stopped, then close client, then close store", got)
	}
	for _, name := range []string{"queue", "monitor", "backup"} {
		if !slices.Contains(got[:4], name+" stopped") {
			t.Fatalf("%s not stopped before the closers ran: %v", name, got)
		}
	}
}

// When the client's Run returns by itself (an error, not SIGTERM) the loops are still stopped and joined, the
// closers still run, and the error is returned.
func TestRunServiceStopsLoopsWhenRunReturnsOnItsOwn(t *testing.T) {
	var tr trace
	ctx, cancel := context.WithCancel(t.Context())
	boom := errors.New("connect: boom")
	run := func(context.Context) error { return boom }
	done := runAsync(func() error {
		return runService(ctx, cancel, run, []func(context.Context){slowLoop(&tr, "queue")}, closer(&tr, "client", nil), closer(&tr, "store", nil))
	})
	if err := within(t, done); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the Run error", err)
	}
	if got := fmt.Sprint(tr.events()); got != "[queue stopped close client close store]" {
		t.Fatalf("order = %s", got)
	}
}

// A failing closer does not stop the later closers, and its error is reported with the Run error.
func TestRunServiceClosesEverythingAndReportsCloseErrors(t *testing.T) {
	var tr trace
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	badClient := errors.New("close client: busy")
	err := within(t, runAsync(func() error {
		return runService(ctx, cancel, func(context.Context) error { return nil }, nil, closer(&tr, "client", badClient), closer(&tr, "store", nil))
	}))
	if !errors.Is(err, badClient) || fmt.Sprint(tr.events()) != "[close client close store]" {
		t.Fatalf("err = %v, order = %v", err, tr.events())
	}
}

// The wiring in serve: no goroutine is started outside runService, and the pipeline gets the serve context.
func TestServeOwnsItsGoroutinesAndGivesThePipelineItsContext(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var calls []string
	goStmts := 0
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "serve" {
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.GoStmt:
					goStmts++
				case *ast.CallExpr:
					switch f := x.Fun.(type) {
					case *ast.Ident:
						calls = append(calls, f.Name)
					case *ast.SelectorExpr:
						calls = append(calls, f.Sel.Name)
					}
				}
				return true
			})
		}
	}
	if goStmts != 0 {
		t.Fatalf("serve starts %d goroutines of its own; they must run under runService", goStmts)
	}
	for _, want := range []string{"runService", "WithContext"} {
		if !slices.Contains(calls, want) {
			t.Fatalf("serve never calls %s (calls: %v)", want, calls)
		}
	}
}
