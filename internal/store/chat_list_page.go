package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// ChatListFilter is the complete SQL-side predicate for the Chats screen.
type ChatListFilter struct {
	Query, Type, Logging string
	New, Gaps, Failed    bool
}

// ChatListAfter identifies the final row of the preceding page in the stable Chats ordering.
type ChatListAfter struct {
	JID      string
	Last     int64
	Messages int
}

// ChatListPage reads at most limit+1 derived rows so callers can expose a next-page link without loading the archive's
// complete chat list. Total is the number matching the same predicate.
func (s *Store) ChatListPage(ctx context.Context, f ChatListFilter, after ChatListAfter, limit int) (rows []ChatRow, total int, more bool, err error) {
	if limit < 1 || limit > 10000 {
		return nil, 0, false, fmt.Errorf("chat list page limit out of range")
	}
	where, args := chatListWhere(f)
	if err = s.read.QueryRowContext(ctx, `SELECT COUNT(*) FROM (`+chatListPageBase+where+`)`, args...).Scan(&total); err != nil {
		return nil, 0, false, fmt.Errorf("count chat list page: %w", err)
	}
	if after.JID != "" {
		where += ` AND (s.last_ts<? OR (s.last_ts=? AND (s.messages=0)>?) OR (s.last_ts=? AND (s.messages=0)=? AND s.canonical>?))`
		empty := 0
		if after.Messages == 0 {
			empty = 1
		}
		args = append(args, after.Last, after.Last, empty, after.Last, empty, after.JID)
	}
	base := chatListPageBase + where
	queryArgs := append(append([]any{}, args...), limit+1)
	err = s.scanRows(ctx, base+` ORDER BY s.last_ts DESC,s.messages=0,s.canonical LIMIT ?`, queryArgs, func(dbRows *sql.Rows) error {
		var row ChatRow
		c := &row.ChatStat
		if scanErr := dbRows.Scan(&c.CanonicalJID, &c.Name, &c.MessageCount, &c.Oldest, &c.Newest, &row.OldestHistory,
			&c.MediaFailed, &c.MediaExpired, &c.MediaPending, &c.OpenGaps, &row.Logging, &row.HasMediaRule, &row.New); scanErr != nil {
			return scanErr
		}
		row.DisplayName = c.Name
		if row.DisplayName == "" {
			row.DisplayName = c.CanonicalJID
		}
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		return nil, 0, false, fmt.Errorf("chat list page: %w", err)
	}
	if len(rows) > limit {
		rows, more = rows[:limit], true
	}
	return rows, total, more, nil
}

const chatListPageBase = `WITH
r AS (SELECT a.canonical AS c,MIN(x.logging) AS logging,MAX(x.media<>'') AS media FROM chat_rules x JOIN chat_canonical a ON a.alias=x.jid GROUP BY a.canonical),
f AS (SELECT a.canonical AS c,MAX(x.acknowledged) AS acked FROM chat_seen x JOIN chat_canonical a ON a.alias=x.jid GROUP BY a.canonical),
d AS (SELECT a.canonical AS c,MAX(x.type='community') AS community,MAX(x.type='group') AS group_chat FROM chats x JOIN chat_canonical a ON a.alias=x.jid GROUP BY a.canonical)
SELECT s.canonical,s.name,s.messages,s.oldest_ts,s.last_ts,COALESCE(s.oldest_history_ts,0),s.media_failed,s.media_expired,s.media_pending,s.gaps_open,
 COALESCE(r.logging,1),COALESCE(r.media,0),COALESCE(NOT f.acked,0)
FROM chat_stats s LEFT JOIN r ON r.c=s.canonical LEFT JOIN f ON f.c=s.canonical LEFT JOIN d ON d.c=s.canonical WHERE 1=1`

func chatListWhere(f ChatListFilter) (string, []any) {
	var where strings.Builder
	var args []any
	if f.Query != "" {
		where.WriteString(` AND (LOWER(CASE WHEN s.name='' THEN s.canonical ELSE s.name END) LIKE ? ESCAPE '\' OR LOWER(s.canonical) LIKE ? ESCAPE '\')`)
		needle := "%" + escapeLike(strings.ToLower(f.Query)) + "%"
		args = append(args, needle, needle)
	}
	if f.Type != "" {
		where.WriteString(` AND CASE WHEN COALESCE(d.community,0) THEN 'community' WHEN COALESCE(d.group_chat,0) OR s.canonical LIKE '%@g.us' THEN 'group' ELSE 'direct' END=?`)
		args = append(args, f.Type)
	}
	switch f.Logging {
	case "on":
		where.WriteString(` AND COALESCE(r.logging,1)=1`)
	case "off":
		where.WriteString(` AND COALESCE(r.logging,1)=0`)
	}
	if f.New {
		where.WriteString(` AND COALESCE(NOT f.acked,0)`)
	}
	if f.Gaps {
		where.WriteString(` AND s.gaps_open>0`)
	}
	if f.Failed {
		where.WriteString(` AND s.media_failed>0`)
	}
	return where.String(), args
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	return strings.ReplaceAll(s, `_`, `\_`)
}
