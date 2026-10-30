package store

import (
	"context"
	"fmt"
	"strings"
)

// ChatControl is the directory and explicit-rule metadata used by the paged Chats UI. Parent is canonical.
type ChatControl struct {
	Name, Type, Parent string
	Rule               *ChatRule
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
	rows, err = s.read.QueryContext(ctx, `SELECT jid,name,type,parent_jid FROM chats ORDER BY jid`)
	if err != nil {
		return nil, fmt.Errorf("chat controls directory: %w", err)
	}
	for rows.Next() {
		var jid, name, typ, parent string
		if err = rows.Scan(&jid, &name, &typ, &parent); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("chat controls directory: %w", err)
		}
		canonical := aliases[jid]
		if canonical == "" {
			canonical = jid
		}
		c := out[canonical]
		if c.Name == "" && name != "" {
			c.Name = name
		}
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

	rules, err := s.ExplicitChatRules(ctx)
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

// EstimateChats uses the selected chats' derived aggregates. TextBytes is deliberately an estimate (300 bytes of
// archived text/metadata per message), so a bulk preview remains O(chats) instead of rescanning raw messages.
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
	q := `SELECT COALESCE(SUM(messages),0),COALESCE(SUM(messages)*300,0),COALESCE(SUM(media_bytes),0),COALESCE(MIN(oldest_ts),0),COALESCE(MAX(last_ts),0)
FROM chat_stats WHERE canonical IN (` + in + `)`
	if err := s.read.QueryRowContext(ctx, q, args...).Scan(&out.Messages, &out.TextBytes, &out.MediaBytes, &out.Oldest, &out.Newest); err != nil {
		return out, fmt.Errorf("estimate chat text: %w", err)
	}
	return out, nil
}
