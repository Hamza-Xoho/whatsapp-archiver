package mcpbridge_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	_ "modernc.org/sqlite"
	"whatsapp-archiver/internal/mcpbridge"
)

// Fixture content is synthetic. Removing policy enforcement, strict decoding,
// complete response budgeting, or safe error mapping must break these tests.
func fixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "archive.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := os.ReadFile("../store/testdata/pre_v1/0001_raw.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(schema)); err != nil {
		t.Fatal(err)
	}
	for _, chat := range []string{"allowed@g.us", "denied@g.us"} {
		if _, err = db.Exec("INSERT INTO chats(jid,name,type) VALUES(?,?,?)", chat, chat, "group"); err != nil {
			t.Fatal(err)
		}
		for n := 0; n < 30; n++ {
			body := "needle " + strings.Repeat("界", 9000)
			if chat == "denied@g.us" {
				body = "FORBIDDEN_CONTENT"
			}
			if _, err = db.Exec("INSERT INTO messages(chat_jid,id,ts,received_at,source,text,raw_proto) VALUES(?,?,?,?,?,?,X'')", chat, fmt.Sprint(n), time.Now().Unix(), n, "live", body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, "config.yaml")
	if err = os.WriteFile(config, []byte(fmt.Sprintf("archive: %s\nruntime_dir: %s\npolicy:\n  mode: selected\n  allow_chats: [allowed@g.us]\n  deny_chats: [denied@g.us]\n", dbPath, dir)), 0600); err != nil {
		t.Fatal(err)
	}
	return config
}

func session(t *testing.T, config string) (*mcp.ClientSession, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	server, err := mcpbridge.New(config, "test")
	if err != nil {
		t.Fatal(err)
	}
	ct, st := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "synthetic-test", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close(); _ = ss.Close(); cancel() })
	return cs, ctx
}

func call(t *testing.T, cs *mcp.ClientSession, ctx context.Context, name string, args any) *mcp.CallToolResult {
	t.Helper()
	r, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func encoded(t *testing.T, r *mcp.CallToolResult) string {
	t.Helper()
	b, e := json.Marshal(r)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}

func TestDiscoveryAndEquivalentBoundedResults(t *testing.T) {
	cs, ctx := session(t, fixture(t))
	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"archive_status": true, "list_chats": true, "search_messages": true, "read_conversation": true, "get_message": true, "get_context": true, "list_artifacts": true, "get_artifact": true}
	if len(tools.Tools) != len(want) {
		t.Fatalf("tools=%d", len(tools.Tools))
	}
	for _, tool := range tools.Tools {
		if !want[tool.Name] {
			t.Fatalf("unexpected tool %s", tool.Name)
		}
		delete(want, tool.Name)
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint || tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint {
			t.Fatalf("unsafe hints: %s", tool.Name)
		}
	}
	for _, tc := range []struct {
		name string
		args any
	}{{"archive_status", map[string]any{}}, {"list_chats", map[string]any{}}, {"search_messages", map[string]any{"query": "needle", "all_time": true, "limit": 100}}, {"read_conversation", map[string]any{"chat_id": "allowed@g.us", "limit": 100}}} {
		t.Run(tc.name, func(t *testing.T) {
			r := call(t, cs, ctx, tc.name, tc.args)
			if r.IsError {
				t.Fatalf("%s", encoded(t, r))
			}
			b := encoded(t, r)
			if len(b) > 64*1024 {
				t.Fatalf("oversized result: %d", len(b))
			}
			if strings.Contains(b, "FORBIDDEN_CONTENT") || strings.Contains(b, "denied@g.us") {
				t.Fatal("policy leak")
			}
			if len(r.Content) != 1 {
				t.Fatal("missing text")
			}
			text, ok := r.Content[0].(*mcp.TextContent)
			if !ok {
				t.Fatal("not text")
			}
			var a, bv any
			if err = json.Unmarshal([]byte(text.Text), &a); err != nil {
				t.Fatal(err)
			}
			structured, _ := json.Marshal(r.StructuredContent)
			if err = json.Unmarshal(structured, &bv); err != nil {
				t.Fatal(err)
			}
			aa, _ := json.Marshal(a)
			bb, _ := json.Marshal(bv)
			if string(aa) != string(bb) {
				t.Fatal("representations differ")
			}
			obj, ok := a.(map[string]any)
			if !ok || obj["schema_version"] != float64(1) {
				t.Fatal("bad envelope")
			}
			for _, key := range []string{"data", "scope", "observed_at", "warnings", "next_cursor", "has_more"} {
				if _, ok := obj[key]; !ok {
					t.Fatalf("missing %s", key)
				}
			}
		})
	}
}

func TestSafeErrorsAndStrictArguments(t *testing.T) {
	cs, ctx := session(t, fixture(t))
	for _, tc := range []struct {
		name string
		args any
		code string
	}{
		{"archive_status", map[string]any{"private_query": "SECRET_QUERY"}, "INVALID_ARGUMENT"},
		{"search_messages", map[string]any{"query": strings.Repeat("x", 70000)}, "INVALID_ARGUMENT"},
		{"search_messages", map[string]any{}, "INVALID_ARGUMENT"},
		{"list_chats", map[string]any{"limit": "20"}, "INVALID_ARGUMENT"},
		{"read_conversation", map[string]any{"chat_id": "denied@g.us"}, "NOT_FOUND_OR_NOT_ALLOWED"},
		{"get_message", map[string]any{"message_ref": "invalid"}, "NOT_FOUND_OR_NOT_ALLOWED"},
		{"list_artifacts", map[string]any{"message_ref": "invalid"}, "FEATURE_UNAVAILABLE"},
		{"get_artifact", map[string]any{"artifact_ref": "invalid"}, "FEATURE_UNAVAILABLE"},
	} {
		t.Run(tc.name+tc.code, func(t *testing.T) {
			r := call(t, cs, ctx, tc.name, tc.args)
			s := encoded(t, r)
			if !r.IsError || !strings.Contains(s, tc.code) {
				t.Fatalf("want %s: %s", tc.code, s)
			}
			for _, bad := range []string{"SECRET_QUERY", "FORBIDDEN_CONTENT", "denied@g.us", "SELECT", "archive.db"} {
				if strings.Contains(s, bad) {
					t.Fatalf("diagnostic leak: %s", bad)
				}
			}
		})
	}
}

func TestCancellationDoesNotPoisonSession(t *testing.T) {
	cs, ctx := session(t, fixture(t))
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err := cs.CallTool(cancelled, &mcp.CallToolParams{Name: "search_messages", Arguments: map[string]any{"query": "needle"}})
	if err == nil {
		t.Fatal("cancelled request succeeded")
	}
	if r := call(t, cs, ctx, "archive_status", map[string]any{}); r.IsError {
		t.Fatal(encoded(t, r))
	}
}
