package archiveread

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type arguments struct {
	Query       string `json:"query,omitempty"`
	ChatID      string `json:"chat_id,omitempty"`
	SenderID    string `json:"sender_id,omitempty"`
	From        string `json:"from,omitempty"`
	To          string `json:"to,omitempty"`
	AllTime     bool   `json:"all_time,omitempty"`
	Limit       *int   `json:"limit,omitempty"`
	Cursor      string `json:"cursor,omitempty"`
	MessageRef  string `json:"message_ref,omitempty"`
	ArtifactRef string `json:"artifact_ref,omitempty"`
	Before      *int   `json:"before,omitempty"`
	After       *int   `json:"after,omitempty"`
}

func (a arguments) limit(def int) int {
	if a.Limit != nil {
		return *a.Limit
	}
	return def
}
func parseArgs(tool string, raw json.RawMessage) (arguments, error) {
	var a arguments
	allowed := map[string]string{"archive_status": "", "list_chats": "query limit cursor", "search_messages": "query chat_id sender_id from to all_time limit cursor", "read_conversation": "chat_id from to limit cursor", "get_message": "message_ref", "get_context": "message_ref before after", "list_artifacts": "message_ref", "get_artifact": "artifact_ref"}
	fields, ok := allowed[tool]
	if !ok || len(raw) > 16384 || !utf8.Valid(raw) {
		return a, failure("INVALID_ARGUMENT")
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return a, failure("INVALID_ARGUMENT")
	}
	for k, v := range m {
		if !strings.Contains(" "+fields+" ", " "+k+" ") || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return a, failure("INVALID_ARGUMENT")
		}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&a) != nil {
		return a, failure("INVALID_ARGUMENT")
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return a, failure("INVALID_ARGUMENT")
	}
	if utf8.RuneCountInString(a.Query) > 512 || len(a.ChatID) > 512 || len(a.SenderID) > 512 || len(a.Cursor) > 4096 {
		return a, failure("INVALID_ARGUMENT")
	}
	if a.Limit != nil && (*a.Limit < 1 || *a.Limit > 100) {
		return a, failure("INVALID_ARGUMENT")
	}
	for _, n := range []*int{a.Before, a.After} {
		if n != nil && (*n < 0 || *n > 10) {
			return a, failure("INVALID_ARGUMENT")
		}
	}
	if tool == "search_messages" && strings.TrimSpace(a.Query) == "" || tool == "read_conversation" && a.ChatID == "" || (tool == "get_message" || tool == "get_context" || tool == "list_artifacts") && a.MessageRef == "" || tool == "get_artifact" && a.ArtifactRef == "" {
		return a, failure("INVALID_ARGUMENT")
	}
	if a.AllTime && (a.From != "" || a.To != "") {
		return a, failure("INVALID_ARGUMENT")
	}
	var from, to time.Time
	var e error
	if a.From != "" {
		from, e = time.Parse(time.RFC3339Nano, a.From)
		if e != nil {
			return a, failure("INVALID_ARGUMENT")
		}
	}
	if a.To != "" {
		to, e = time.Parse(time.RFC3339Nano, a.To)
		if e != nil {
			return a, failure("INVALID_ARGUMENT")
		}
	}
	if !from.IsZero() && !to.IsZero() && !from.Before(to) {
		return a, failure("INVALID_ARGUMENT")
	}
	return a, nil
}
func clip(s string, n int) (string, bool) {
	count := 0
	for offset := range s {
		if count == n {
			return s[:offset], true
		}
		count++
	}
	return s, false
}

func ceilInstant(s string) int64 {
	t, _ := time.Parse(time.RFC3339Nano, s)
	n := t.Unix()
	if t.Nanosecond() > 0 {
		n++
	}
	return n
}

// bytePrefixSQL preserves embedded NULs; SQLite's TEXT substr/length do not.
// Callers choose a strict byte budget so Go never receives the whole huge value.
func bytePrefixSQL(expr string, maxBytes int) string {
	return "COALESCE(CAST(substr(CAST(" + expr + " AS BLOB),1," + strconv.Itoa(maxBytes) + ") AS TEXT),CASE WHEN " + expr + " IS NULL THEN NULL ELSE '' END)"
}
func boundedSQL(expr string, n int) string {
	bytes := utf8.UTFMax * n
	return bytePrefixSQL(expr, bytes) + ",COALESCE(length(substr(CAST(" + expr + " AS BLOB),1," + strconv.Itoa(bytes+1) + ")),0)>" + strconv.Itoa(bytes)
}

// finishPrefix trims only an incomplete UTF-8 sequence cut by the SQL byte
// boundary. Invalid complete source sequences fail explicitly rather than being
// replaced by JSON encoding. Rune clipping is separate and never removes NULs.
func finishPrefix(value string, byteClipped bool, n int) (string, bool, error) {
	if !utf8.ValidString(value) {
		complete := ""
		found := false
		if byteClipped {
			for remove := 1; remove < utf8.UTFMax && remove <= len(value); remove++ {
				prefix, suffix := value[:len(value)-remove], value[len(value)-remove:]
				if utf8.ValidString(prefix) && !utf8.FullRuneInString(suffix) {
					complete = prefix
					found = true
					break
				}
			}
		}
		if !found {
			return "", false, failure("QUERY_TOO_BROAD")
		}
		value = complete
	}
	value, runeClipped := clip(value, n)
	return value, byteClipped || runeClipped, nil
}
