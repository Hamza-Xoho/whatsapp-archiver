package main

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/store"
)

// I3a: after the import the dashboard owns the chat rules, so a JID added to config.yaml exclude later does nothing.
// serve says so once per such JID, naming it only by the last 4 characters of its user part ("…2222@g.us").
func TestWarnsAboutIgnoredConfigExclusions(t *testing.T) {
	st := liveStore(t) // liveLID and livePN are one chat
	rules := ingest.NewRuleSet([]store.ChatRule{{JID: "111111@g.us", Logging: false}, {JID: "222222@g.us", Logging: true}, {JID: livePN, Logging: false}})
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	warnIgnoredExclusions(log, []string{"111111@g.us", "222222@g.us", "333333@g.us", liveLID}, ingest.NewFilter(rules, storeAliases(st)))
	out := buf.String()
	if n := strings.Count(out, "level=WARN"); n != 2 {
		t.Fatalf("%d warnings, want 2 (222222 turned back on, 333333 never imported):\n%s", n, out)
	}
	for _, want := range []string{"config.yaml exclude is ignored since the dashboard owns chat rules", "…2222@g.us is being logged", "…3333@g.us is being logged"} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q:\n%s", want, out)
		}
	}
	for _, full := range []string{"222222@g.us", "333333@g.us", "1111", "@lid"} {
		if strings.Contains(out, full) {
			t.Errorf("log names %q:\n%s", full, out)
		}
	}
}

func TestServeWarnsAboutIgnoredExclusions(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "serve" {
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "warnIgnoredExclusions" {
						found = true
					}
				}
				return true
			})
		}
	}
	if !found {
		t.Fatal("serve never calls warnIgnoredExclusions")
	}
}

// I3b: once the archive owns the chat rules, `archiver chats` shows what is captured, from the archive's rules and
// through aliases, not config.yaml's stale exclude list.
func TestChatsShowsArchiveRulesAfterImport(t *testing.T) {
	dir := seedFolder(t) // config.yaml excludes Layla's phone form and Work
	ctx := context.Background()
	st, err := store.Open(filepath.Join(dir, "archive.db"))
	mustDo(t, err)
	_, err = st.ImportConfigOnce(ctx, &config.Config{Exclude: []string{laylaPN, workJID}})
	mustDo(t, err)
	mustDo(t, st.SetChatRules(ctx, []store.ChatRule{{JID: workJID, Logging: true}, {JID: familyJID, Logging: false}}))
	mustDo(t, st.Close())
	code, out, errOut := runCmd(t, "chats", "--data", dir)
	if code != 0 || errOut != "" {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	want := "120363111@g.us\tFamily\t5\texcluded=yes\n120363222@g.us\tWork\t1\texcluded=no\n204871@lid\tLayla\t4\texcluded=yes\n"
	if out != want {
		t.Fatalf("chats:\n%s\nwant:\n%s", out, want)
	}
}

// I4: a manual `archiver backup` keeps as many snapshots as the dashboard's backup.schedule says, not config.yaml's
// stale keep, so it never prunes snapshots the UI asked to keep.
func TestManualBackupUsesTheArchivesKeep(t *testing.T) {
	dir := t.TempDir()
	dest := t.TempDir()
	ctx := context.Background()
	st, err := store.Open(filepath.Join(dir, "archive.db"))
	mustDo(t, err)
	mustDo(t, st.SetBackupSchedule(ctx, config.BackupConfig{Time: "03:30", Keep: 9}))
	mustDo(t, st.Close())
	for day := 1; day <= 10; day++ {
		mustDo(t, os.MkdirAll(filepath.Join(dest, fmt.Sprintf("202001%02dT000000.000000000Z", day)), 0o700))
	}
	writeFile(t, filepath.Join(dir, "config.yaml"), "backup:\n  destination: "+dest+"\n  keep: 2\nwa:\n  check_latest_version: false\n")
	code, _, errOut := runCmd(t, "backup", "--data", dir)
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	entries, err := os.ReadDir(dest)
	mustDo(t, err)
	if len(entries) != 9 {
		t.Fatalf("%d snapshots kept, want 9 (the archive's keep)", len(entries))
	}
}

// Before the import (no backup.schedule) the manual backup keeps config.yaml's number.
func TestManualBackupFallsBackToConfigKeep(t *testing.T) {
	dir := t.TempDir()
	dest := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "archive.db"))
	mustDo(t, err)
	mustDo(t, st.Close())
	for day := 1; day <= 5; day++ {
		mustDo(t, os.MkdirAll(filepath.Join(dest, fmt.Sprintf("202001%02dT000000.000000000Z", day)), 0o700))
	}
	writeFile(t, filepath.Join(dir, "config.yaml"), "backup:\n  destination: "+dest+"\n  keep: 2\nwa:\n  check_latest_version: false\n")
	if code, _, errOut := runCmd(t, "backup", "--data", dir); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	entries, err := os.ReadDir(dest)
	mustDo(t, err)
	if len(entries) != 2 {
		t.Fatalf("%d snapshots kept, want 2 (config.yaml's keep)", len(entries))
	}
}
