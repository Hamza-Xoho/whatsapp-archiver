package archiveread_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

func TestReaderReviewStoppedWALNeverCreatesSiblings(t *testing.T) {
	for _, family := range []string{"legacy", "v1"} {
		t.Run(family, func(t *testing.T) {
			f := newFixture(t, family)
			f.msg("a@lid", "one", "synthetic", 1)
			if err := f.db.Close(); err != nil {
				t.Fatal(err)
			}
			for _, suffix := range []string{"-wal", "-shm"} {
				if _, err := os.Stat(f.path + suffix); !os.IsNotExist(err) {
					t.Fatalf("fixture sidecar still present: %s %v", suffix, err)
				}
			}
			_, err := f.call("archive_status", map[string]any{})
			checkCode(t, err, "ARCHIVE_UNAVAILABLE")
			for _, suffix := range []string{"-wal", "-shm"} {
				if _, err = os.Stat(f.path + suffix); !os.IsNotExist(err) {
					t.Fatalf("reader created sibling %s", suffix)
				}
			}
			f.db, err = sql.Open("sqlite", f.path)
			if err != nil {
				t.Fatal(err)
			}
			f.msg("a@lid", "two", "writer resumed", 2)
			r, err := f.call("read_conversation", map[string]any{"chat_id": "a@lid"})
			r = must(t, r, err)
			if len(messages(t, r)) != 2 {
				t.Fatal("reader failed after real WAL recreation")
			}
			if err = f.db.Close(); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(f.path+"-wal", nil, 0600); err != nil {
				t.Fatal(err)
			}
			_, err = f.call("archive_status", map[string]any{})
			checkCode(t, err, "ARCHIVE_UNAVAILABLE")
			if _, err = os.Stat(f.path + "-shm"); !os.IsNotExist(err) {
				t.Fatal("created missing SHM")
			}
		})
	}
}

func TestReaderReviewSenderScopeDoesNotDisclosePrivateMapping(t *testing.T) {
	f := newFixture(t, "v1")
	f.exec("INSERT INTO id_mappings(lid,pn) VALUES('private-hidden@lid','guessed@s.whatsapp.net')")
	f.msg("group@g.us", "one", "needle", 1)
	f.policy("selected", []string{"group@g.us"}, []string{"private-hidden@lid"})
	r, err := f.call("search_messages", map[string]any{"query": "needle", "sender_id": "guessed@s.whatsapp.net", "all_time": true})
	r = must(t, r, err)
	if r.Scope["sender_id"] != "guessed@s.whatsapp.net" {
		t.Fatalf("scope disclosed mapping: %#v", r.Scope)
	}
	raw, _ := json.Marshal(r)
	if strings.Contains(string(raw), "private-hidden@lid") {
		t.Fatal("empty result disclosed unrelated private mapping")
	}
	f.exec("UPDATE messages SET sender_jid='private-hidden@lid' WHERE id='one'")
	r, err = f.call("search_messages", map[string]any{"query": "needle", "sender_id": "guessed@s.whatsapp.net", "all_time": true})
	r = must(t, r, err)
	if len(messages(t, r)) != 1 {
		t.Fatal("sender-private-chat denial incorrectly hides permitted group message")
	}
}

func TestReaderReviewSearchPrecedesDisplayClipping(t *testing.T) {
	f := newFixture(t, "v1")
	f.msg("a@lid", "document", "body", 1)
	name := strings.Repeat("x", 512) + " needle-name"
	filename := strings.Repeat("y", 512) + "needle-file.pdf"
	f.exec("INSERT INTO chats(jid,name,type) VALUES('a@lid',?,?)", name, strings.Repeat("private", 30))
	f.exec("INSERT INTO media(chat_jid,message_id,source_rank,type,mime,filename,state) VALUES('a@lid','document',1,'document','application/pdf',?,'done')", filename)
	r, err := f.call("search_messages", map[string]any{"query": "needle-file", "all_time": true})
	r = must(t, r, err)
	if len(messages(t, r)) != 1 {
		t.Fatal("search missed filename beyond display clip")
	}
	raw, _ := json.Marshal(r)
	if !strings.Contains(string(raw), "attachment.filename") {
		t.Fatal("filename clip not flagged")
	}
	r, err = f.call("list_chats", map[string]any{"query": "needle-name"})
	r = must(t, r, err)
	chats := data(t, r)["chats"].([]any)
	if len(chats) != 1 {
		t.Fatal("chat filter missed name beyond display clip")
	}
	raw, _ = json.Marshal(r)
	if !strings.Contains(string(raw), `"clipped"`) || !strings.Contains(string(raw), `"name"`) {
		t.Fatal("chat name clip not flagged")
	}
}

func TestReaderReviewOversizedFieldsRetrieveBoundedSearchIsHonest(t *testing.T) {
	f := newFixture(t, "v1")
	f.msg("a@lid", "large", strings.Repeat("x", 1000001)+"needle-at-tail", 1)
	r, err := f.call("get_message", map[string]any{"message_ref": ref("a@lid", "large")})
	r = must(t, r, err)
	m := data(t, r)["message"].(map[string]any)
	if utf8.RuneCountInString(m["text"].(string)) > 8000 {
		t.Fatal("body not bounded")
	}
	raw, _ := json.Marshal(r)
	if !strings.Contains(string(raw), `"clipped":["text"`) {
		t.Fatal("SQL body clip not flagged")
	}
	f.exec("UPDATE messages SET text='short body',caption=? WHERE id='large'", strings.Repeat("界", 1100000)+"needle-at-tail")
	f.exec("INSERT INTO media(chat_jid,message_id,source_rank,type,mime,filename,state) VALUES('a@lid','large',1,'document',?,?,'done')", strings.Repeat("q", 1100000), strings.Repeat("f", 1100000)+"needle-at-tail")
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	start := time.Now()
	r, err = f.call("get_message", map[string]any{"message_ref": ref("a@lid", "large")})
	r = must(t, r, err)
	runtime.ReadMemStats(&after)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("bounded retrieval too slow: %s", elapsed)
	}
	if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 24*1024*1024 {
		t.Fatalf("oversized retrieval allocated %d bytes; presentation projection must bound fields", alloc)
	}
	raw, _ = json.Marshal(r)
	if len(raw) > 28*1024 || !strings.Contains(string(raw), "caption") || !strings.Contains(string(raw), "attachment.filename") {
		t.Fatal("oversized retrieval lacks bounded/clipped metadata")
	}
	_, err = f.call("search_messages", map[string]any{"query": "needle-at-tail", "all_time": true})
	checkCode(t, err, "QUERY_TOO_BROAD")
}

func TestReaderReviewViewerRouteAndMappingCursorInvalidation(t *testing.T) {
	f := newFixture(t, "legacy")
	f.msg("a@lid", "special/?#", "needle", 1)
	f.msg("a@lid", "two", "needle", 2)
	b, err := os.ReadFile(f.config)
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, []byte("viewer_base_url: https://viewer.example/base\n")...)
	if err = os.WriteFile(f.config, b, 0600); err != nil {
		t.Fatal(err)
	}
	r, err := f.call("get_message", map[string]any{"message_ref": ref("a@lid", "special/?#")})
	r = must(t, r, err)
	m := data(t, r)["message"].(map[string]any)
	if m["viewer_url"] != "https://viewer.example/base/chats/a@lid/m/special%2F%3F%23" {
		t.Fatalf("bad viewer route: %v", m["viewer_url"])
	}
	r, err = f.call("search_messages", map[string]any{"query": "needle", "all_time": true, "limit": 1})
	r = must(t, r, err)
	f.exec("INSERT INTO id_mappings(lid,pn) VALUES('a@lid','p@s.whatsapp.net')")
	_, err = f.call("search_messages", map[string]any{"query": "needle", "all_time": true, "limit": 1, "cursor": r.NextCursor})
	checkCode(t, err, "INVALID_CURSOR")
}

func TestReaderReviewPositiveScopedScaleAndConcurrentWriter(t *testing.T) {
	if testing.Short() {
		t.Skip("300k positive scoped performance and concurrent WAL benchmark")
	}
	f := newFixture(t, "v1")
	f.exec("WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<300000) INSERT INTO messages(chat_jid,id,sender_jid,kind,text,source,ts,received_at,raw_proto) SELECT 'a@lid',printf('m%06d',x),'sender@lid','text','needle','live',x,x,X'' FROM n")
	var progress atomic.Int64
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		for j := 0; ctx.Err() == nil; j++ {
			_, err := f.db.ExecContext(ctx, "INSERT INTO messages(chat_jid,id,sender_jid,kind,text,source,ts,received_at,raw_proto) VALUES('writer@lid',?,'sender@lid','text','writer','live',?,?,X'')", fmt.Sprintf("w%d", j), 400000+j, 400000+j)
			if err != nil {
				if ctx.Err() == nil {
					done <- err
					return
				}
				break
			}
			progress.Add(1)
		}
		done <- nil
	}()
	defer func() {
		stop()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	for _, tool := range []string{"read_conversation", "search_messages"} {
		a := map[string]any{"chat_id": "a@lid", "limit": 2}
		if tool == "search_messages" {
			a["query"] = "needle"
			a["all_time"] = true
		}
		start := time.Now()
		r, err := f.call(tool, a)
		r = must(t, r, err)
		if len(messages(t, r)) != 2 || !r.HasMore {
			t.Fatalf("positive first page missing: %#v", r)
		}
		if elapsed := time.Since(start); elapsed > 1500*time.Millisecond {
			t.Fatalf("%s first page scaled with whole chat: %s", tool, elapsed)
		}
		t.Logf("%s first page: %s concurrent writer commits: %d", tool, time.Since(start), progress.Load())
	}
	if progress.Load() == 0 {
		t.Fatal("writer did not progress while reader queries ran")
	}
	// An exhaustive miss stays cancellable while the independently owned writer continues.
	before := progress.Load()
	scanCtx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	raw := json.RawMessage(`{"query":"absent","chat_id":"a@lid","all_time":true}`)
	start := time.Now()
	_, err := f.e.Call(scanCtx, "search_messages", raw)
	if err == nil || time.Since(start) > time.Second {
		t.Fatalf("loaded scan cancellation failed: %v", err)
	}
	if progress.Load() <= before {
		t.Fatal("writer stalled during cancellable scan")
	}
	var walBytes int64
	if stat, statErr := os.Stat(f.path + "-wal"); statErr == nil {
		walBytes = stat.Size()
	}
	t.Logf("cancelled scan: %s; concurrent commits delta=%d WAL bytes=%d", time.Since(start), progress.Load()-before, walBytes)
}

func TestReaderReviewNameMatchingPrecedesClipping(t *testing.T) {
	f := newFixture(t, "v1")
	f.exec("INSERT INTO chats(jid,name,type) VALUES('a@lid',?,?)", strings.Repeat("x", 512)+"needle-name", strings.Repeat("group", 50))
	r, err := f.call("list_chats", map[string]any{"query": "needle-name"})
	r = must(t, r, err)
	chats := data(t, r)["chats"].([]any)
	if len(chats) != 1 {
		t.Fatal("name clipping changed exhaustive filtering")
	}
	raw, _ := json.Marshal(r)
	if !strings.Contains(string(raw), `"clipped"`) {
		t.Fatal("chat clipping not flagged")
	}
}

func TestReaderReviewCaptionMetadataRetrievalBounded(t *testing.T) {
	f := newFixture(t, "v1")
	f.msg("a@lid", "large", "short", 1)
	f.exec("UPDATE messages SET caption=? WHERE id='large'", strings.Repeat("界", 1100000)+"needle-tail")
	f.exec("INSERT INTO media(chat_jid,message_id,source_rank,type,mime,filename,state) VALUES('a@lid','large',1,'document',?,?,'done')", strings.Repeat("m", 1100000), strings.Repeat("f", 1100000)+"needle-tail")
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	r, err := f.call("get_message", map[string]any{"message_ref": ref("a@lid", "large")})
	r = must(t, r, err)
	runtime.ReadMemStats(&after)
	if bytes := after.TotalAlloc - before.TotalAlloc; bytes > 24*1024*1024 {
		t.Fatalf("retrieval allocated %d bytes", bytes)
	}
	raw, _ := json.Marshal(r)
	if len(raw) > 28*1024 || !strings.Contains(string(raw), "attachment.mime") || !strings.Contains(string(raw), "caption") {
		t.Fatal("metadata/body presentation not bounded and flagged")
	}
	_, err = f.call("search_messages", map[string]any{"query": "needle-tail", "all_time": true})
	checkCode(t, err, "QUERY_TOO_BROAD")
}
