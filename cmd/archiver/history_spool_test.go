package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// The importer is an owned service loop: it starts with the other loops, stops before the archive closes, and the
// history handler only wakes it after the durable spool transaction.
func TestServeWiresHistorySpoolAsAnOwnedLoop(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	calls := map[string]int{}
	runInLoops := false
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "serve" {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CallExpr:
				switch f := x.Fun.(type) {
				case *ast.Ident:
					calls[f.Name]++
				case *ast.SelectorExpr:
					calls[f.Sel.Name]++
				}
			case *ast.CompositeLit:
				for _, el := range x.Elts {
					if sel, ok := el.(*ast.SelectorExpr); ok && sel.Sel.Name == "Run" {
						if id, ok := sel.X.(*ast.Ident); ok && id.Name == "history" {
							runInLoops = true
						}
					}
				}
			}
			return true
		})
	}
	for _, name := range []string{"NewHistorySpool", "WithHistorySpool"} {
		if calls[name] != 1 {
			t.Errorf("serve calls %s %d times, want 1", name, calls[name])
		}
	}
	if !runInLoops {
		t.Error("serve does not run history.Run among its owned loops")
	}
}
