package archiveread_test

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
	"whatsapp-archiver/internal/archiveread"
)

type fixture struct {
	t                    *testing.T
	db                   *sql.DB
	path, config, family string
	e                    *archiveread.Engine
}

func newFixture(t *testing.T, family string) *fixture {
	t.Helper()
	d := t.TempDir()
	f := &fixture{t: t, path: filepath.Join(d, "archive.db"), config: filepath.Join(d, "reader.yaml"), family: family}
	var err error
	f.db, err = sql.Open("sqlite", f.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.db.Close() })
	f.exec("PRAGMA journal_mode=WAL")
	schema := "../../internal/store/testdata/pre_v1/0001_raw.sql"
	if family == "v1" {
		schema = "../../internal/store/migrations/0001_v1.sql"
	}
	b, err := os.ReadFile(schema)
	if err != nil {
		t.Fatal(err)
	}
	f.exec(string(b))
	f.policy("all", nil, nil)
	f.e, err = archiveread.New(f.config)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *fixture) exec(q string, args ...any) {
	f.t.Helper()
	if _, err := f.db.Exec(q, args...); err != nil {
		f.t.Fatal(err)
	}
}
func (f *fixture) policy(mode string, allow, deny []string) {
	f.t.Helper()
	p := fmt.Sprintf("archive: %q\nruntime_dir: %q\npolicy:\n  mode: %s\n  allow_chats: [%s]\n  deny_chats: [%s]\n", f.path, filepath.Dir(f.path), mode, strings.Join(allow, ", "), strings.Join(deny, ", "))
	if err := os.WriteFile(f.config, []byte(p), 0600); err != nil {
		f.t.Fatal(err)
	}
}
func (f *fixture) msg(chat, id, text string, ts int64) {
	f.t.Helper()
	if f.family == "v1" {
		f.exec("INSERT INTO messages(chat_jid,id,sender_jid,kind,text,source,ts,received_at,raw_proto) VALUES(?,?,?,'text',?,'live',?,?,X'')", chat, id, "sender@lid", text, ts, ts)
	} else {
		f.exec("INSERT INTO messages(chat_jid,id,sender_jid,text,source,ts,received_at,raw_proto) VALUES(?,?,?,?,'live',?,?,X'')", chat, id, "sender@lid", text, ts, ts)
	}
}
func (f *fixture) event(chat, id, kind, event, text string, ts int64) {
	f.exec("INSERT INTO message_events(chat_jid,target_id,kind,event_id,new_text,ts,received_at) VALUES(?,?,?,?,?,?,?)", chat, id, kind, event, text, ts, ts)
}
func (f *fixture) call(tool string, args any) (archiveread.Result, error) {
	b, _ := json.Marshal(args)
	return f.e.Call(context.Background(), tool, b)
}
func data(t *testing.T, r archiveread.Result) map[string]any {
	t.Helper()
	b, e := json.Marshal(r.Data)
	if e != nil {
		t.Fatal(e)
	}
	var d map[string]any
	if e = json.Unmarshal(b, &d); e != nil {
		t.Fatalf("data not object: %s", b)
	}
	return d
}
func messages(t *testing.T, r archiveread.Result) []any {
	t.Helper()
	v, ok := data(t, r)["messages"].([]any)
	if !ok {
		t.Fatalf("messages missing: %#v", r.Data)
	}
	return v
}
func must(t *testing.T, r archiveread.Result, err error) archiveread.Result {
	t.Helper()
	if err != nil {
		t.Fatalf("call failed: %s", err)
	}
	return r
}
func checkCode(t *testing.T, err error, want string) {
	t.Helper()
	if archiveread.CodeOf(err) != want {
		t.Fatalf("code=%q err=%v want=%q", archiveread.CodeOf(err), err, want)
	}
}
func ref(chat, id string) string {
	b, _ := json.Marshal([]string{"1", chat, id})
	return base64.RawURLEncoding.EncodeToString(b)
}

func TestReaderLegacyV1CurrentProjectionAndPolicy(t *testing.T) {
	for _, family := range []string{"legacy", "v1"} {
		t.Run(family, func(t *testing.T) {
			f := newFixture(t, family)
			f.exec("INSERT INTO id_mappings(lid,pn) VALUES('b@lid','p@s.whatsapp.net'),('a@lid','p@s.whatsapp.net')")
			f.exec("INSERT INTO chats(jid,name,type) VALUES('p@s.whatsapp.net','Team','private'),('denied@lid','Secret','private')")
			f.msg("p@s.whatsapp.net", "one", "obsolete private phrase", 10)
			f.msg("a@lid", "one", "duplicate content", 11)
			f.msg("p@s.whatsapp.net", "two", "deleted private phrase", 12)
			f.msg("denied@lid", "three", "forbidden text", 13)
			f.event("b@lid", "one", "edit", "e1", "first replacement", 15)
			f.event("a@lid", "one", "edit", "e2", "Current أَهْلًا literal %_", 16)
			f.event("b@lid", "two", "revoke", "r1", "", 17)
			f.policy("selected", []string{"b@lid"}, nil)
			r, e := f.call("search_messages", map[string]any{"query": "اهلا", "all_time": true})
			r = must(t, r, e)
			ms := messages(t, r)
			if len(ms) != 1 {
				t.Fatalf("search results=%v", ms)
			}
			m := ms[0].(map[string]any)
			if m["chat_id"] != "a@lid" || m["stored_chat_id"] != "p@s.whatsapp.net" || m["text"] != "Current أَهْلًا literal %_" {
				t.Fatalf("bad projected identity/body: %#v", m)
			}
			for _, q := range []string{"obsolete", "duplicate", "deleted", "forbidden"} {
				r, e = f.call("search_messages", map[string]any{"query": q, "all_time": true})
				r = must(t, r, e)
				if len(messages(t, r)) != 0 {
					t.Fatalf("leaked old/forbidden search %q", q)
				}
			}
			r, e = f.call("get_message", map[string]any{"message_ref": ref("a@lid", "two")})
			r = must(t, r, e)
			raw, _ := json.Marshal(r)
			if strings.Contains(string(raw), "deleted private") || !strings.Contains(string(raw), `"revoked":true`) {
				t.Fatalf("revocation not protected: %s", raw)
			}
			r, e = f.call("list_chats", map[string]any{})
			r = must(t, r, e)
			ch := data(t, r)["chats"].([]any)
			if len(ch) != 1 || ch[0].(map[string]any)["id"] != "a@lid" {
				t.Fatalf("chats=%v", ch)
			}
			f.policy("selected", []string{"a@lid"}, []string{"p@s.whatsapp.net"})
			_, e = f.call("get_message", map[string]any{"message_ref": m["message_ref"]})
			checkCode(t, e, "NOT_FOUND_OR_NOT_ALLOWED")
			f.policy("selected", nil, nil)
			r, e = f.call("search_messages", map[string]any{"query": "Current", "all_time": true})
			r = must(t, r, e)
			if len(messages(t, r)) != 0 {
				t.Fatal("empty policy exposed content")
			}
			if e = os.Remove(f.config); e != nil {
				t.Fatal(e)
			}
			_, e = f.call("get_message", map[string]any{"message_ref": ref("a@lid", "one")})
			checkCode(t, e, "NOT_FOUND_OR_NOT_ALLOWED")
		})
	}
}

func TestReaderReadOnlySchemaAndMissingArchive(t *testing.T) {
	f := newFixture(t, "legacy")
	f.msg("a@lid", "one", "hello", 1)
	var before string
	if err := f.db.QueryRow("SELECT group_concat(sql) FROM sqlite_schema").Scan(&before); err != nil {
		t.Fatal(err)
	}
	r, e := f.call("archive_status", map[string]any{})
	must(t, r, e)
	var after string
	if err := f.db.QueryRow("SELECT group_concat(sql) FROM sqlite_schema").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("reader changed schema")
	}
	f.exec("CREATE TABLE archive_meta(key TEXT PRIMARY KEY,value TEXT)")
	f.exec("INSERT INTO archive_meta VALUES('schema','future')")
	_, e = f.call("archive_status", map[string]any{})
	checkCode(t, e, "UNSUPPORTED_SCHEMA")
	f2 := newFixture(t, "legacy")
	f2.exec("ALTER TABLE messages RENAME COLUMN text TO wrong")
	_, e = f2.call("archive_status", map[string]any{})
	checkCode(t, e, "UNSUPPORTED_SCHEMA")
	missing := filepath.Join(t.TempDir(), "archive.db")
	config := filepath.Join(t.TempDir(), "reader.yaml")
	if err := os.WriteFile(config, []byte(fmt.Sprintf("archive: %q\nruntime_dir: %q\npolicy:\n  mode: selected\n", missing, t.TempDir())), 0600); err != nil {
		t.Fatal(err)
	}
	engine, err := archiveread.New(config)
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.Call(context.Background(), "archive_status", json.RawMessage(`{}`))
	checkCode(t, err, "ARCHIVE_UNAVAILABLE")
	if _, err = os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("missing archive created: %v", err)
	}
	if err = os.WriteFile(config, []byte("archive: /tmp/session.db\npolicy:\n  mode: all\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = archiveread.LoadConfig(config); err == nil {
		t.Fatal("accepted session database")
	}
}

func TestReaderArgumentsDatesContextAndCursors(t *testing.T) {
	f := newFixture(t, "legacy")
	for i := int64(1); i <= 25; i++ {
		f.msg("a@lid", fmt.Sprintf("m%02d", i), "needle %_ OR literal", i)
	}
	invalid := []struct {
		tool string
		arg  any
	}{{"search_messages", map[string]any{"query": ""}}, {"search_messages", map[string]any{"query": strings.Repeat("界", 513)}}, {"search_messages", map[string]any{"query": "x", "from": "2026-01-01"}}, {"search_messages", map[string]any{"query": "x", "all_time": true, "from": "1970-01-01T00:00:01Z"}}, {"search_messages", map[string]any{"query": "x", "from": "1970-01-01T00:00:02Z", "to": "1970-01-01T00:00:01Z"}}, {"read_conversation", map[string]any{"chat_id": "a@lid", "limit": 101}}, {"list_chats", map[string]any{"extra": true}}, {"get_context", map[string]any{"message_ref": ref("a@lid", "m12"), "before": 11}}}
	for _, tc := range invalid {
		_, e := f.call(tc.tool, tc.arg)
		checkCode(t, e, "INVALID_ARGUMENT")
	}
	r, e := f.call("search_messages", map[string]any{"query": "%_ OR", "all_time": true, "limit": 2})
	r = must(t, r, e)
	if len(messages(t, r)) != 2 || !r.HasMore || r.NextCursor == "" {
		t.Fatalf("pagination=%#v", r)
	}
	first := messages(t, r)[0].(map[string]any)
	if first["id"] != "m25" {
		t.Fatalf("order=%v", first)
	}
	cursor := r.NextCursor
	r, e = f.call("search_messages", map[string]any{"query": "%_ OR", "all_time": true, "limit": 2, "cursor": cursor})
	r = must(t, r, e)
	if messages(t, r)[0].(map[string]any)["id"] != "m23" {
		t.Fatal("cursor skipped/wrong order")
	}
	_, e = f.call("search_messages", map[string]any{"query": "different", "all_time": true, "limit": 2, "cursor": cursor})
	checkCode(t, e, "INVALID_CURSOR")
	_, e = f.call("search_messages", map[string]any{"query": "%_ OR", "all_time": true, "limit": 2, "cursor": cursor + "x"})
	checkCode(t, e, "INVALID_CURSOR")
	f.policy("selected", []string{"a@lid"}, nil)
	_, e = f.call("search_messages", map[string]any{"query": "%_ OR", "all_time": true, "limit": 2, "cursor": cursor})
	checkCode(t, e, "INVALID_CURSOR")
	r, e = f.call("read_conversation", map[string]any{"chat_id": "a@lid", "limit": 3})
	r = must(t, r, e)
	ms := messages(t, r)
	if ms[0].(map[string]any)["id"] != "m23" || ms[2].(map[string]any)["id"] != "m25" {
		t.Fatalf("recent order=%v", ms)
	}
	r, e = f.call("read_conversation", map[string]any{"chat_id": "a@lid", "from": "1970-01-01T00:00:02Z", "to": "1970-01-01T00:00:04Z"})
	r = must(t, r, e)
	if len(messages(t, r)) != 2 {
		t.Fatal("half-open bounds")
	}
	r, e = f.call("get_context", map[string]any{"message_ref": ref("a@lid", "m12"), "before": 10, "after": 10})
	r = must(t, r, e)
	if len(messages(t, r)) != 21 {
		t.Fatalf("context count=%d", len(messages(t, r)))
	}
	r, e = f.call("search_messages", map[string]any{"query": "needle"})
	r = must(t, r, e)
	if len(messages(t, r)) != 0 {
		t.Fatal("search default time scope missing")
	}
}

func TestReaderSafeMetadataClippingAndUnavailableArtifacts(t *testing.T) {
	f := newFixture(t, "v1")
	f.msg("a@lid", "long", strings.Repeat("界", 12000), 1)
	f.exec("UPDATE messages SET kind='document',caption='caption',details='{\"filename\":\"report.pdf\",\"secret\":\"DO-NOT-RETURN\"}' WHERE id='long'")
	f.exec("INSERT INTO media(chat_jid,message_id,source_rank,type,mime,filename,state,path,retry_direct_path) VALUES('a@lid','long',1,'document','application/pdf','report.pdf','done','DO-NOT-RETURN','DO-NOT-RETURN')")
	r, e := f.call("get_message", map[string]any{"message_ref": ref("a@lid", "long")})
	r = must(t, r, e)
	raw, _ := json.Marshal(r)
	if len(raw) > 32000 || strings.Contains(string(raw), "DO-NOT-RETURN") || !strings.Contains(string(raw), "clipped") {
		t.Fatalf("unsafe/unbounded response %d bytes", len(raw))
	}
	for _, tool := range []string{"list_artifacts", "get_artifact"} {
		key := "message_ref"
		if tool == "get_artifact" {
			key = "artifact_ref"
		}
		_, e = f.call(tool, map[string]any{key: "opaque"})
		checkCode(t, e, "FEATURE_UNAVAILABLE")
	}
	r, e = f.call("archive_status", map[string]any{})
	r = must(t, r, e)
	raw, _ = json.Marshal(r)
	if !strings.Contains(string(raw), `"artifacts":false`) {
		t.Fatal("artifact capability missing")
	}
}

func TestReaderWALFreshRequestsCancellationAndReplacement(t *testing.T) {
	f := newFixture(t, "legacy")
	f.msg("a@lid", "one", "needle", 1)
	r, e := f.call("search_messages", map[string]any{"query": "needle", "all_time": true, "limit": 1})
	must(t, r, e)
	f.msg("a@lid", "two", "needle", 2)
	r, e = f.call("search_messages", map[string]any{"query": "needle", "all_time": true, "limit": 1})
	r = must(t, r, e)
	if messages(t, r)[0].(map[string]any)["id"] != "two" {
		t.Fatal("WAL commit not visible")
	}
	cursor := r.NextCursor
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	_, e = f.e.Call(ctx, "list_chats", json.RawMessage(`{}`))
	if e == nil || time.Since(start) > time.Second {
		t.Fatalf("cancellation failed: %v", e)
	}
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(f.path, f.path+".old"); err != nil {
		t.Fatal(err)
	}
	var err error
	f.db, err = sql.Open("sqlite", f.path)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := os.ReadFile("../store/testdata/pre_v1/0001_raw.sql")
	if err != nil {
		t.Fatal(err)
	}
	f.exec(string(schema))
	f.msg("a@lid", "new", "needle", 3)
	_, e = f.call("search_messages", map[string]any{"query": "needle", "all_time": true, "limit": 1, "cursor": cursor})
	checkCode(t, e, "INVALID_CURSOR")
	r, e = f.call("search_messages", map[string]any{"query": "needle", "all_time": true})
	r = must(t, r, e)
	if len(messages(t, r)) != 1 || messages(t, r)[0].(map[string]any)["id"] != "new" {
		t.Fatal("replacement not reopened")
	}
}

func TestReaderAtScale(t *testing.T) {
	if testing.Short() {
		t.Skip("synthetic scale benchmark")
	}
	for _, family := range []string{"legacy", "v1"} {
		t.Run(family, func(t *testing.T) {
			f := newFixture(t, family)
			f.exec("WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<300000) INSERT INTO messages(chat_jid,id,sender_jid," + map[bool]string{true: "kind,", false: ""}[family == "v1"] + "text,source,ts,received_at,raw_proto) SELECT 'a@lid',printf('m%06d',x),'sender@lid'," + map[bool]string{true: "'text',", false: ""}[family == "v1"] + "'synthetic haystack','live',x,x,X'' FROM n")
			start := time.Now()
			r, e := f.call("search_messages", map[string]any{"query": "absent needle", "all_time": true})
			elapsed := time.Since(start)
			if e != nil {
				checkCode(t, e, "QUERY_TOO_BROAD")
			} else if len(messages(t, r)) != 0 {
				t.Fatal("wrong scale results")
			}
			if elapsed > 3*time.Second {
				t.Fatalf("deadline exceeded: %s", elapsed)
			}
			f.msg("a@lid", "writer-progress", "after read", 400000)
			t.Logf("%s 300k search: %s result=%s; writer progressed", family, elapsed, archiveread.CodeOf(e))
		})
	}
}
