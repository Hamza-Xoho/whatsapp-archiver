package store

import (
	"context"
	"fmt"
	"strings"
)

// searchDenseChatPosition materialises a globally dense exact FTS result once, intersects it with one chat, and
// returns only the selected and adjacent IDs. SearchChatPosition has already ruled out duplicate messages and matching
// edits, so neither grouping nor a second result source is needed here.
func (s *Store) searchDenseChatPosition(ctx context.Context, chat, table, match string, filter ViewerQuery, index int) (ChatSearchPosition, error) {
	position := ChatSearchPosition{Index: index}
	args := []any{chat, match}
	where := []string{"m.chat_jid IN (" + chatForms + ")"}
	add := func(clause string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}
	if filter.Sender != "" {
		add("m.sender_jid=?%d", filter.Sender)
	}
	if filter.MediaKind != "" {
		add("m.kind=?%d", filter.MediaKind)
	}
	if filter.FromTS > 0 {
		add("m.ts>=?%d", filter.FromTS)
	}
	if filter.ToTS > 0 {
		add("m.ts<=?%d", filter.ToTS)
	}
	start := max(index-1, 0)
	args = append(args, start)
	hitsSQL := `WITH hits(rowid) AS MATERIALIZED (SELECT rowid FROM ` + table + ` WHERE ` + table + ` MATCH ?2)`
	sourceSQL := ` FROM hits h JOIN messages m ON m.seq=h.rowid WHERE ` + strings.Join(where, " AND ")
	rows, err := s.read.QueryContext(ctx, hitsSQL+`
	 SELECT m.id,COUNT(*) OVER ()`+sourceSQL+`
	 ORDER BY m.ts DESC,m.id DESC LIMIT 3 OFFSET ?`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return position, fmt.Errorf("search dense chat position: %w", err)
	}
	for rowIndex := start; rows.Next(); rowIndex++ {
		var id string
		if err = rows.Scan(&id, &position.Count); err != nil {
			_ = rows.Close()
			return position, err
		}
		switch rowIndex {
		case index - 1:
			position.PreviousID = id
		case index:
			position.CurrentID = id
		case index + 1:
			position.NextID = id
		}
	}
	err = rows.Err()
	if closeErr := rows.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return position, fmt.Errorf("search dense chat position: %w", err)
	}
	// A far-out offset returns no window row, so ask only for the count needed to distinguish it from no matches.
	if position.Count == 0 && index > 0 {
		countArgs := args[:len(args)-1]
		if err = s.read.QueryRowContext(ctx, hitsSQL+` SELECT COUNT(*)`+sourceSQL, countArgs...).Scan(&position.Count); err != nil {
			return position, fmt.Errorf("count dense chat position: %w", err)
		}
	}
	if position.Count > 0 && position.CurrentID == "" {
		return position, fmt.Errorf("search chat position: index is out of range")
	}
	return position, nil
}
