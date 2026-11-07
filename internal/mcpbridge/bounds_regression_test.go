package mcpbridge_test

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func escapedFixture(t *testing.T, quotes int) (string, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "archive.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := os.ReadFile("../store/migrations/0001_v1.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(schema)); err != nil {
		t.Fatal(err)
	}
	body := strings.Repeat(`"`, quotes) + strings.Repeat("a", 8000-quotes)
	if _, err = db.Exec("INSERT INTO chats(jid,name,type) VALUES('escaped@g.us','Synthetic escaped','group')"); err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= 3; n++ {
		if _, err = db.Exec("INSERT INTO messages(chat_jid,id,kind,text,caption,source,ts,received_at,raw_proto) VALUES('escaped@g.us',?,'image',?,?,'live',?,?,X'')", fmt.Sprint(n), body, body, n*10, n*10); err != nil {
			t.Fatal(err)
		}
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, "config.yaml")
	if err = os.WriteFile(config, []byte(fmt.Sprintf("archive: %s\nruntime_dir: %s\npolicy:\n  mode: selected\n  allow_chats: [escaped@g.us]\n", dbPath, dir)), 0600); err != nil {
		t.Fatal(err)
	}
	ref := base64.RawURLEncoding.EncodeToString([]byte(`["1","escaped@g.us","2"]`))
	return config, ref
}

func equivalentEnvelope(t *testing.T, r *mcp.CallToolResult) map[string]any {
	t.Helper()
	if r.IsError {
		t.Fatalf("valid escaped message returned error: %s", encoded(t, r))
	}
	if n := len(encoded(t, r)); n > 64*1024 {
		t.Fatalf("full MCP result exceeded 64KiB: %d", n)
	}
	if len(r.Content) != 1 {
		t.Fatal("expected equivalent JSON text block")
	}
	text, ok := r.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatal("expected text content")
	}
	var plain, structured map[string]any
	if err := json.Unmarshal([]byte(text.Text), &plain); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(r.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(b, &structured); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plain, structured) {
		t.Fatal("clipped structured/text representations differ")
	}
	return structured
}

func assertFurtherClipped(t *testing.T, message map[string]any) {
	t.Helper()
	for _, field := range []string{"text", "caption"} {
		value, ok := message[field].(string)
		if !ok || !utf8.ValidString(value) || utf8.RuneCountInString(value) >= 8000 || len(value) == 0 {
			t.Fatalf("%s was not safely clipped", field)
		}
		flags, ok := message["clipped"].([]any)
		if !ok {
			t.Fatal("clipping flags absent")
		}
		found := false
		for _, flag := range flags {
			if flag == field {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing %s clipping flag", field)
		}
	}
}

// Regression: escaped JSON text is escaped a second time inside MCP content.
// Returning RESPONSE_TOO_LARGE, silently truncating rows, or clipping without
// flags/equivalent structured content all violate this observable contract.
func TestEscapedMessagesFurtherClipAtCompleteMCPBudget(t *testing.T) {
	for _, quotes := range []int{4300, 5300} {
		t.Run(fmt.Sprint(quotes), func(t *testing.T) {
			config, ref := escapedFixture(t, quotes)
			cs, ctx := session(t, config)
			for _, tc := range []struct {
				name  string
				args  any
				count int
			}{
				{"get_message", map[string]any{"message_ref": ref}, 1},
				{"get_context", map[string]any{"message_ref": ref, "before": 0, "after": 0}, 1},
				{"get_context", map[string]any{"message_ref": ref, "before": 1, "after": 1}, 3},
			} {
				t.Run(fmt.Sprintf("%s-%d", tc.name, tc.count), func(t *testing.T) {
					env := equivalentEnvelope(t, call(t, cs, ctx, tc.name, tc.args))
					data := env["data"].(map[string]any)
					if tc.name == "get_message" {
						assertFurtherClipped(t, data["message"].(map[string]any))
						return
					}
					messages := data["messages"].([]any)
					if len(messages) != tc.count {
						t.Fatalf("lost context rows: %d", len(messages))
					}
					for _, message := range messages {
						assertFurtherClipped(t, message.(map[string]any))
					}
					if data["anchor_ref"] != ref {
						t.Fatal("anchor reference changed")
					}
				})
			}
		})
	}
}

func TestEscapedSingleRowPagesPreserveEveryCursorPosition(t *testing.T) {
	config, _ := escapedFixture(t, 4300)
	cs, ctx := session(t, config)
	cursor := ""
	ids := []string{}
	for page := 0; page < 4; page++ {
		args := map[string]any{"chat_id": "escaped@g.us", "limit": 1}
		if cursor != "" {
			args["cursor"] = cursor
		}
		env := equivalentEnvelope(t, call(t, cs, ctx, "read_conversation", args))
		messages := env["data"].(map[string]any)["messages"].([]any)
		if len(messages) != 1 {
			t.Fatalf("page lost its single row: %d", len(messages))
		}
		message := messages[0].(map[string]any)
		assertFurtherClipped(t, message)
		ids = append(ids, message["id"].(string))
		more := env["has_more"].(bool)
		cursor = env["next_cursor"].(string)
		if !more {
			if cursor != "" {
				t.Fatal("terminal page has cursor")
			}
			break
		}
		if cursor == "" {
			t.Fatal("page lost continuation")
		}
	}
	if !reflect.DeepEqual(ids, []string{"3", "2", "1"}) {
		t.Fatalf("cursor skipped/repeated rows: %v", ids)
	}
}

func TestNullAndArrayToolArgumentsFailSafely(t *testing.T) {
	cs, ctx := session(t, fixture(t))
	for _, raw := range []json.RawMessage{json.RawMessage(`null`), json.RawMessage(`[]`), json.RawMessage(`["SYNTHETIC_SECRET"]`)} {
		r := call(t, cs, ctx, "archive_status", raw)
		b := encoded(t, r)
		if !r.IsError || !strings.Contains(b, "INVALID_ARGUMENT") || strings.Contains(b, "SYNTHETIC_SECRET") {
			t.Fatal("invalid argument object was accepted or disclosed")
		}
	}
}
