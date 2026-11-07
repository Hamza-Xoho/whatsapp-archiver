package archiveread_test

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestReaderNULSearchCompleteFields(t *testing.T) {
	for _, family := range []string{"legacy", "v1"} {
		t.Run(family, func(t *testing.T) {
			fields := []string{"text", "filename", "name"}
			if family == "v1" {
				fields = append(fields, "caption")
			} else {
				fields = []string{"text", "name"}
			}
			for _, field := range fields {
				t.Run(field, func(t *testing.T) {
					f := newFixture(t, family)
					f.msg("a@lid", "one", "ordinary", 1)
					value := "prefix\x00needle-after-nul"
					switch field {
					case "text", "caption":
						f.exec("UPDATE messages SET "+field+"=? WHERE id='one'", value)
					case "filename":
						f.exec("INSERT INTO media(chat_jid,message_id,source_rank,type,mime,filename,state) VALUES('a@lid','one',1,'document','application/pdf',?,'done')", value)
					case "name":
						f.exec("INSERT INTO chats(jid,name,type) VALUES('a@lid',?,'private')", value)
					}
					if field == "name" {
						r, err := f.call("list_chats", map[string]any{"query": "needle-after-nul"})
						r = must(t, r, err)
						chats := data(t, r)["chats"].([]any)
						if len(chats) != 1 || chats[0].(map[string]any)["name"] != value {
							t.Fatalf("NUL name suffix disappeared: %#v", r.Data)
						}
						return
					}
					r, err := f.call("search_messages", map[string]any{"query": "needle-after-nul", "all_time": true})
					r = must(t, r, err)
					if len(messages(t, r)) != 1 {
						t.Fatalf("NUL %s suffix falsely absent", field)
					}
					r, err = f.call("get_message", map[string]any{"message_ref": ref("a@lid", "one")})
					r = must(t, r, err)
					m := data(t, r)["message"].(map[string]any)
					var got any
					if field == "filename" {
						got = m["attachment"].(map[string]any)["filename"]
					} else {
						got = m[field]
					}
					if got != value {
						t.Fatalf("NUL %s preview corrupted: got %q want %q", field, got, value)
					}
				})
			}
		})
	}
}

func TestReaderNULBoundedPreviewAndMultibyteBoundary(t *testing.T) {
	f := newFixture(t, "v1")
	value := "prefix\x00" + strings.Repeat("界🙂", 6000)
	f.msg("a@lid", "long", value, 1)
	f.exec("UPDATE messages SET caption=? WHERE id='long'", value)
	r, err := f.call("get_message", map[string]any{"message_ref": ref("a@lid", "long")})
	r = must(t, r, err)
	m := data(t, r)["message"].(map[string]any)
	for _, field := range []string{"text", "caption"} {
		got := m[field].(string)
		if !utf8.ValidString(got) || strings.ContainsRune(got, utf8.RuneError) {
			t.Fatalf("%s split UTF8 rune: %q", field, got)
		}
		if !strings.HasPrefix(got, "prefix\x00") || !strings.HasPrefix(value, got) {
			t.Fatalf("%s loses embedded NUL", field)
		}
		if utf8.RuneCountInString(got) > 8000 {
			t.Fatalf("%s preview exceeds character limit", field)
		}
	}
	raw, _ := json.Marshal(r)
	if len(raw) > 28*1024 || !strings.Contains(string(raw), `"clipped"`) {
		t.Fatal("bounded preview missing clipping metadata")
	}
}

func TestReaderMultibytePrefixBoundaryAndExhaustiveSearchCap(t *testing.T) {
	f := newFixture(t, "v1")
	prefix := strings.Repeat("a", 31999)
	value := prefix + "🙂tail"
	f.msg("a@lid", "one", value, 1)
	r, err := f.call("get_message", map[string]any{"message_ref": ref("a@lid", "one")})
	r = must(t, r, err)
	m := data(t, r)["message"].(map[string]any)
	if !utf8.ValidString(m["text"].(string)) || strings.ContainsRune(m["text"].(string), utf8.RuneError) {
		t.Fatal("byte-bounded projection emitted replacement rune")
	}
	r, err = f.call("search_messages", map[string]any{"query": "🙂tail", "all_time": true})
	r = must(t, r, err)
	if len(messages(t, r)) != 1 {
		t.Fatal("valid multibyte suffix falsely absent below full search limit")
	}
	f.exec("UPDATE messages SET text=? WHERE id='one'", "prefix\x00"+strings.Repeat("🙂", 65537)+"needle-after-cap")
	_, err = f.call("search_messages", map[string]any{"query": "needle-after-cap", "all_time": true})
	checkCode(t, err, "QUERY_TOO_BROAD")
}

func TestReaderNULIdentifiersKeepStableReferences(t *testing.T) {
	f := newFixture(t, "v1")
	chat, id := "group\x00suffix@g.us", "message\x00suffix"
	f.msg(chat, id, "needle", 1)
	r, err := f.call("get_message", map[string]any{"message_ref": ref(chat, id)})
	r = must(t, r, err)
	m := data(t, r)["message"].(map[string]any)
	if m["stored_chat_id"] != chat || m["id"] != id || m["message_ref"] != ref(chat, id) {
		t.Fatalf("embedded NUL corrupted stable locator: %#v", m)
	}
}
