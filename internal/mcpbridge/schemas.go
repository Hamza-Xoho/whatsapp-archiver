// Package mcpbridge exposes the read-only archive through the official MCP SDK.
package mcpbridge

import (
	"bytes"
	"encoding/json"
	"io"
)

const maxArguments = 16 * 1024
const maxResult = 64 * 1024

type toolSpec struct {
	name        string
	description string
	fields      map[string]string
	required    []string
}

var tools = []toolSpec{
	{"archive_status", "Report archive schema, query limits and coverage caveats; never connection health.", nil, nil},
	{"list_chats", "List only permitted chats.", map[string]string{"query": "string", "limit": "integer", "cursor": "string"}, nil},
	{"search_messages", "Search current stored text and captions literally; attachment contents are not searched.", map[string]string{"query": "string", "chat_id": "string", "sender_id": "string", "from": "string", "to": "string", "all_time": "boolean", "limit": "integer", "cursor": "string"}, []string{"query"}},
	{"read_conversation", "Read a chronological window in one permitted conversation.", map[string]string{"chat_id": "string", "from": "string", "to": "string", "limit": "integer", "cursor": "string"}, []string{"chat_id"}},
	{"get_message", "Resolve a stable message reference with current access checks.", map[string]string{"message_ref": "string"}, []string{"message_ref"}},
	{"get_context", "Read at most ten messages before and after a permitted anchor.", map[string]string{"message_ref": "string", "before": "integer", "after": "integer"}, []string{"message_ref"}},
	{"list_artifacts", "Reserved optional module artifacts; currently FEATURE_UNAVAILABLE.", map[string]string{"message_ref": "string"}, []string{"message_ref"}},
	{"get_artifact", "Reserved optional module artifact retrieval; currently FEATURE_UNAVAILABLE.", map[string]string{"artifact_ref": "string"}, []string{"artifact_ref"}},
}

func (s toolSpec) schema() map[string]any {
	properties := make(map[string]any, len(s.fields))
	for key, kind := range s.fields {
		field := map[string]any{"type": kind}
		switch key {
		case "query":
			field["maxLength"] = 512
		case "limit":
			field["minimum"] = 1
			field["maximum"] = 100
		case "before", "after":
			field["minimum"] = 0
			field["maximum"] = 10
		}
		properties[key] = field
	}
	schema := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(s.required) > 0 {
		schema["required"] = s.required
	}
	return schema
}

// Validate before invoking the reader: unknown keys, scalar/array/null arguments,
// trailing JSON, and large payloads cannot trigger any archive access.
func (s toolSpec) arguments(raw json.RawMessage) (map[string]any, bool) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if len(raw) > maxArguments {
		return nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var args map[string]any
	if err := dec.Decode(&args); err != nil || args == nil {
		return nil, false
	}
	var trailing any
	if dec.Decode(&trailing) != io.EOF {
		return nil, false
	}
	for key, value := range args {
		kind, ok := s.fields[key]
		if !ok {
			return nil, false
		}
		switch kind {
		case "string":
			if _, ok := value.(string); !ok {
				return nil, false
			}
		case "boolean":
			if _, ok := value.(bool); !ok {
				return nil, false
			}
		case "integer":
			n, ok := value.(json.Number)
			if !ok {
				return nil, false
			}
			if _, err := n.Int64(); err != nil {
				return nil, false
			}
		}
	}
	for _, key := range s.required {
		if _, ok := args[key]; !ok {
			return nil, false
		}
	}
	return args, true
}
