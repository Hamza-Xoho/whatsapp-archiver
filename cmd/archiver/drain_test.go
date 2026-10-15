package main

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"testing"
	"time"
)

// I2: an event handler still running when the client's Run returns (a history chunk being imported) finishes before
// the client and the store are closed.
func TestRunServiceWaitsForTheHandlerInFlightBeforeClosing(t *testing.T) {
	var tr trace
	handlers := &inFlight{}
	started := make(chan struct{})
	handled := make(chan struct{})
	go func() { // whatsmeow's dispatching goroutine
		defer close(handled)
		handlers.wrap(func(any) { close(started); time.Sleep(100 * time.Millisecond); tr.add("handler done") })(nil)
	}()
	<-started
	ctx, cancel := context.WithCancel(t.Context())
	run := func(context.Context) error { tr.add("run returned"); return nil }
	if err := within(t, runAsync(func() error {
		return runService(ctx, cancel, run, nil, handlers.drain, closer(&tr, "client", nil), closer(&tr, "store", nil))
	})); err != nil {
		t.Fatal(err)
	}
	<-handled
	if got := fmt.Sprint(tr.events()); got != "[run returned handler done close client close store]" {
		t.Fatalf("order = %s, want the handler to finish before the client and the store are closed", got)
	}
}

// I2: a handler that never finishes holds shutdown only until the bound (30 s, well inside systemd's 90 s stop
// timeout); then the closers run and the cut is reported.
func TestHandlerDrainIsBounded(t *testing.T) {
	if handlerDrainBound != 30*time.Second {
		t.Fatalf("bound = %s, want 30s", handlerDrainBound)
	}
	var tr trace
	bound := make(chan time.Time)
	var asked time.Duration
	handlers := &inFlight{after: func(d time.Duration) <-chan time.Time { asked = d; return bound }}
	started, release := make(chan struct{}), make(chan struct{})
	handled := make(chan struct{})
	go func() {
		defer close(handled)
		handlers.wrap(func(any) { close(started); <-release })(nil)
	}()
	defer func() { close(release); <-handled }()
	<-started
	ctx, cancel := context.WithCancel(t.Context())
	done := runAsync(func() error {
		return runService(ctx, cancel, func(context.Context) error { return nil }, nil, handlers.drain, closer(&tr, "client", nil), closer(&tr, "store", nil))
	})
	select {
	case err := <-done:
		t.Fatalf("runService returned (%v, %v) while the handler was in flight and the bound had not passed", err, tr.events())
	case <-time.After(100 * time.Millisecond):
	}
	close(bound) // the bound passes
	err := within(t, done)
	if err == nil || asked != handlerDrainBound || fmt.Sprint(tr.events()) != "[close client close store]" {
		t.Fatalf("err=%v bound=%s order=%v, want the cut reported, the 30s bound, and both closers run", err, asked, tr.events())
	}
}

// I2: once shutdown has started waiting, a newly arriving event is not handled: the store is about to close.
func TestEventArrivingDuringShutdownIsNotHandled(t *testing.T) {
	handlers := &inFlight{}
	if err := handlers.drain(); err != nil {
		t.Fatal(err)
	}
	handled := false
	handlers.wrap(func(any) { handled = true })(nil)
	if handled {
		t.Fatal("an event that arrived after shutdown began was handled")
	}
}

// I2: serve counts its event handler in flight and drains it after the client's Run returns, before the client and
// the store are closed.
func TestServeDrainsItsHandlerBeforeClosing(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	name := func(e ast.Expr) string {
		if sel, ok := e.(*ast.SelectorExpr); ok {
			if x, ok := sel.X.(*ast.Ident); ok {
				return x.Name + "." + sel.Sel.Name
			}
		}
		return ""
	}
	var calls, closers []string
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "serve" {
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					calls = append(calls, name(call.Fun))
					if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "runService" && len(call.Args) > 4 {
						for _, arg := range call.Args[4:] {
							closers = append(closers, name(arg))
						}
					}
				}
				return true
			})
		}
	}
	if !slices.Contains(calls, "handlers.wrap") || fmt.Sprint(closers) != "[handlers.drain client.Close st.Close]" {
		t.Fatalf("serve must wrap its handler and pass handlers.drain before client.Close and st.Close (closers %v)", closers)
	}
}
