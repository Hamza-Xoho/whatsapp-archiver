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

// serve imports config.yaml's settings into the archive once, right after opening it and before the chat rules are
// loaded, so the first start after the upgrade captures with the imported exclusions. Capture then reads the live
// settings: the monitor's thresholds, the backup schedule and the first sighting of each chat.
func TestServeImportsConfigBeforeLoadingRules(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	first := map[string]token.Pos{}
	scheduleArgs := -1
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
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			name := sel.Sel.Name
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "store" && name == "Open" {
				name = "store.Open"
			}
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "backup" && name == "Schedule" {
				scheduleArgs = len(call.Args)
			}
			if _, seen := first[name]; !seen {
				first[name] = call.Pos()
			}
			return true
		})
	}
	order := []string{"store.Open", "ImportConfigOnce", "ChatRules", "NewRuleSet"}
	for i, name := range order {
		if first[name] == token.NoPos {
			t.Fatalf("serve never calls %s", name)
		}
		if i > 0 && first[name] < first[order[i-1]] {
			t.Fatalf("serve calls %s before %s", name, order[i-1])
		}
	}
	for _, name := range []string{"WithThresholds", "NewSeenTracker", "WithSeen"} {
		if first[name] == token.NoPos {
			t.Errorf("serve never calls %s", name)
		}
	}
	if scheduleArgs != 5 {
		t.Errorf("backup.Schedule called with %d arguments, want 5 (the live schedule)", scheduleArgs)
	}
}
