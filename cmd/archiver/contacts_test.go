package main

import (
	"context"
	"database/sql"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"whatsapp-archiver/internal/store"
)

type staticContacts struct {
	mu    sync.Mutex
	names map[types.JID]types.ContactInfo
}

func (s *staticContacts) GetAllContacts(context.Context) (map[types.JID]types.ContactInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.names, nil
}

func openArchive(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "archive.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func chatName(t *testing.T, st *store.Store, canonical string) string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(st.Path())+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var name string
	if err = db.QueryRow(`SELECT name FROM chat_stats WHERE canonical=?`, canonical).Scan(&name); errors.Is(err, sql.ErrNoRows) {
		return ""
	} else if err != nil {
		t.Fatal(err)
	}
	return name
}

// The contact names the client reads reach the archive through the store's single writer: an event asks, the syncer
// reads the source, and the chat takes the contact's name.
func TestContactNamesReachTheArchive(t *testing.T) {
	st := openArchive(t)
	ctx := context.Background()
	if _, err := st.Apply(ctx, store.Batch{Message: &store.Message{ChatJID: "9665@s.whatsapp.net", ID: "A", SenderJID: "9665@s.whatsapp.net", Kind: "text", Text: "x", Source: "live", Timestamp: 10, ReceivedAt: 10, RawProto: []byte("A")}}); err != nil {
		t.Fatal(err)
	}
	src := &staticContacts{names: map[types.JID]types.ContactInfo{
		types.NewJID("9665", types.DefaultUserServer): {Found: true, FullName: "Layla Khan", PushName: "lay"},
		types.NewJID("777", types.DefaultUserServer):  {Found: true, PushName: "no contact name"},
	}}
	syncer := newContactSync(src, st, slog.New(slog.DiscardHandler)).WithDebounce(0)

	var seen []any
	verdict := true
	handler := withContactSync(syncer, func(evt any) bool { seen = append(seen, evt); return verdict })

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); syncer.Run(runCtx) }()
	defer func() { cancel(); <-done }()

	connected := &events.Connected{}
	if !handler(connected) {
		t.Error("the wrapper changed the handler's verdict (true)")
	}
	verdict = false
	if handler(&events.PushName{}) {
		t.Error("the wrapper changed the handler's verdict (false)")
	}
	if len(seen) != 2 || seen[0] != any(connected) {
		t.Errorf("the wrapped handler saw %v", seen)
	}

	deadline := time.Now().Add(5 * time.Second)
	for chatName(t, st, "9665@s.whatsapp.net") != "Layla Khan" {
		if time.Now().After(deadline) {
			t.Fatalf("chat name = %q after the connect event, want the contact name", chatName(t, st, "9665@s.whatsapp.net"))
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := chatName(t, st, "777@s.whatsapp.net"); got != "" {
		t.Errorf("a contact without a name made a chat name %q", got)
	}
}

// serve builds the syncer, wraps the event handler with it and runs it among the service loops. (The same kind of
// check as the other serve wiring tests: serve cannot be started against WhatsApp in a test.)
func TestServeWiresContactSync(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
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
						if id, ok := sel.X.(*ast.Ident); ok && id.Name == "contacts" {
							runInLoops = true
						}
					}
				}
			}
			return true
		})
	}
	for _, name := range []string{"newContactSync", "withContactSync", "ContactNames"} {
		if calls[name] != 1 {
			t.Errorf("serve calls %s %d times, want 1", name, calls[name])
		}
	}
	if !runInLoops {
		t.Error("serve does not run contacts.Run among its loops")
	}
}
