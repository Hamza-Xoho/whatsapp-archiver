package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// Hard constraint: exactly one connection writes archive.db. serve opens the archive once; the only other opener of
// the writable store in the commands is the offline replay tool, which needs a fresh data folder.
func TestServeOpensTheArchiveOnce(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	opens := map[string]int{}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Open" {
					if id, ok := sel.X.(*ast.Ident); ok && id.Name == "store" {
						opens[fn.Name.Name]++
					}
				}
				return true
			})
		}
	}
	if opens["serve"] != 1 {
		t.Fatalf("serve opens the writable archive %d times, want exactly 1 (all openers: %v)", opens["serve"], opens)
	}
	for fn, n := range opens {
		if fn != "serve" && fn != "replay" {
			t.Errorf("%s opens the writable archive %d times; only serve and the replay tool may", fn, n)
		}
	}
}
