package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// ChatControl is the directory and explicit-rule metadata used by the paged Chats UI. Parent is canonical.
type ChatControl struct {
	Type, Parent string
	Rule         *ChatRule
}

// ChatEstimate is the archived activity used for the FR-25 weekly size estimate.
type ChatEstimate struct {
	Messages, TextBytes int64
	MediaBytes          int64
	Oldest, Newest      int64
}

// ChatControls reads the small mutable directory/control tables once, without touching messages or StatusReport.
func (s *Store) ChatControls(ctx context.Context) (map[string]ChatControl, error) {
	aliases := map[string]string{}
	rows, err := s.read.QueryContext(ctx, `SELECT alias,canonical FROM chat_canonical`)
	if err != nil {
		return nil, fmt.Errorf("chat controls aliases: %w", err)
	}
	for rows.Next() {
		var alias, canonical string
		if err = rows.Scan(&alias, &canonical); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("chat controls alias: %w", err)
		}
		aliases[alias] = canonical
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("chat controls aliases: %w", err)
	}
	_ = rows.Close()

	out := make(map[string]ChatControl, len(aliases))
	rows, err = s.read.QueryContext(ctx, `SELECT jid,type,parent_jid FROM chats ORDER BY jid`)
	if err != nil {
		return nil, fmt.Errorf("chat controls directory: %w", err)
	}
	for rows.Next() {
		var jid, typ, parent string
		if err = rows.Scan(&jid, &typ, &parent); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("chat controls directory: %w", err)
		}
		canonical := aliases[jid]
		if canonical == "" {
			canonical = jid
		}
		c := out[canonical]
		if c.Type == "" || typ == "community" {
			c.Type = typ
		}
		if parent != "" {
			c.Parent = aliases[parent]
			if c.Parent == "" {
				c.Parent = parent
			}
		}
		out[canonical] = c
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("chat controls directory: %w", err)
	}
	_ = rows.Close()

	rules, err := s.ChatRules(ctx)
	if err != nil {
		return nil, err
	}
	for i := range rules {
		canonical := aliases[rules[i].JID]
		if canonical == "" {
			canonical = rules[i].JID
		}
		c := out[canonical]
		rule := rules[i]
		c.Rule = &rule
		out[canonical] = c
	}
	return out, nil
}

// DeleteChatRules restores inherited/default behavior for the named canonical chats and all their aliases.
func (s *Store) DeleteChatRules(ctx context.Context, canonical []string) error {
	if len(canonical) == 0 {
		return nil
	}
	marks := make([]string, len(canonical))
	args := make([]any, len(canonical))
	for i, jid := range canonical {
		marks[i], args[i] = "?", jid
	}
	_, err := s.write.ExecContext(ctx, `DELETE FROM chat_rules WHERE jid IN (
SELECT alias FROM chat_canonical WHERE canonical IN (`+strings.Join(marks, ",")+`))`, args...)
	if err != nil {
		return fmt.Errorf("delete chat rules: %w", err)
	}
	return nil
}

// EstimateChats reads only the selected chats' indexed messages/media rows. It is used only before a rule change.
func (s *Store) EstimateChats(ctx context.Context, canonical []string) (ChatEstimate, error) {
	var out ChatEstimate
	if len(canonical) == 0 {
		return out, nil
	}
	marks := make([]string, len(canonical))
	args := make([]any, len(canonical))
	for i, jid := range canonical {
		marks[i], args[i] = "?", jid
	}
	in := strings.Join(marks, ",")
	q := `SELECT COUNT(*),COALESCE(SUM(length(m.text)+length(m.caption)+300),0),COALESCE(MIN(m.ts),0),COALESCE(MAX(m.ts),0)
FROM messages m JOIN chat_canonical a ON a.alias=m.chat_jid WHERE a.canonical IN (` + in + `)`
	if err := s.read.QueryRowContext(ctx, q, args...).Scan(&out.Messages, &out.TextBytes, &out.Oldest, &out.Newest); err != nil {
		return out, fmt.Errorf("estimate chat text: %w", err)
	}
	q = `SELECT COALESCE(SUM(m.declared_size),0) FROM media m JOIN chat_canonical a ON a.alias=m.chat_jid WHERE a.canonical IN (` + in + `)`
	if err := s.read.QueryRowContext(ctx, q, args...).Scan(&out.MediaBytes); err != nil && err != sql.ErrNoRows {
		return out, fmt.Errorf("estimate chat media: %w", err)
	}
	return out, nil
}
