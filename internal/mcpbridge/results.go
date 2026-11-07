package mcpbridge

import (
	"encoding/json"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"whatsapp-archiver/internal/archiveread"
)

// SDK 1.8.0 adds these fields after the tool handler for the current protocol.
// Budget their exact encoding even for older clients, where they are omitted.
func sdkResultOverhead(implementation *mcp.Implementation) int {
	fields, _ := json.Marshal(struct {
		Meta       mcp.Meta `json:"_meta"`
		ResultType string   `json:"resultType"`
	}{mcp.Meta{mcp.MetaKeyServerInfo: implementation}, "complete"})
	// Merge the fields into an existing JSON object: remove its braces, add comma.
	return len(fields) - 1
}

func encodedResult(result archiveread.Result) (*mcp.CallToolResult, int, error) {
	data, err := json.Marshal(result)
	if err != nil {
		return nil, 0, err
	}
	response := &mcp.CallToolResult{StructuredContent: json.RawMessage(data), Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}}
	complete, err := json.Marshal(response)
	return response, len(complete), err
}

// Clip presentation fields in place, retaining every row, locator, ordering key,
// scope and continuation token supplied by the reader. Re-encoding the one
// result afterward keeps structured content and its JSON text block equivalent.
func clipResultMessages(result *archiveread.Result) bool {
	data, ok := result.Data.(map[string]any)
	if !ok {
		return false
	}
	changed := false
	if message, ok := data["message"].(archiveread.Message); ok {
		changed = clipMessage(&message)
		data["message"] = message
	}
	if messages, ok := data["messages"].([]archiveread.Message); ok {
		for i := range messages {
			if clipMessage(&messages[i]) {
				changed = true
			}
		}
	}
	return changed
}

func clipMessage(message *archiveread.Message) bool {
	changed := false
	trim := func(value *string, field string) {
		clipped, cut := halfString(*value)
		if !cut {
			return
		}
		*value = clipped
		changed = true
		for _, existing := range message.Clipped {
			if existing == field {
				return
			}
		}
		message.Clipped = append(message.Clipped, field)
	}
	trim(&message.Text, "text")
	if message.Caption != nil {
		caption := *message.Caption
		trim(&caption, "caption")
		message.Caption = &caption
	}
	trim(&message.Snippet, "snippet")
	if message.Attachment != nil && message.Attachment.Filename != nil {
		// Preserve the reader-owned DTO even if a future adapter shares attachments.
		attachment := *message.Attachment
		filename := *attachment.Filename
		trim(&filename, "attachment.filename")
		attachment.Filename = &filename
		message.Attachment = &attachment
	}
	return changed
}

func halfString(value string) (string, bool) {
	count := utf8.RuneCountInString(value)
	if count == 0 {
		return value, false
	}
	remaining := count / 2
	for offset := range value {
		if remaining == 0 {
			return value[:offset], true
		}
		remaining--
	}
	return "", true
}
