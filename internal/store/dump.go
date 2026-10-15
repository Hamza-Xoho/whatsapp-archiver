package store

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
)

// rawTables are the tables ingest writes, in dump order; media belongs to the media queue and is not dumped.
var rawTables = []string{"chats", "participants", "id_mappings", "messages", "message_events", "chat_events", "history_sync_chunks"}

// receiveTimes are columns set from the local clock when a row is written (id_mappings.first_seen is one too).
var receiveTimes = map[string]bool{"received_at": true, "first_seen": true}

// DumpRaw returns every row of the raw tables as one INSERT line, sorted by every column, without receive times,
// so two archives built from the same events dump identically.
func (s *Store) DumpRaw(ctx context.Context) (string, error) {
	var b strings.Builder
	for _, table := range rawTables {
		cols, err := s.dumpColumns(ctx, table)
		if err != nil {
			return "", err
		}
		list := strings.Join(cols, ",")
		rows, err := s.read.QueryContext(ctx, "SELECT "+list+" FROM "+table+" ORDER BY "+list)
		if err != nil {
			return "", fmt.Errorf("dump %s: %w", table, err)
		}
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		for rows.Next() {
			if err = rows.Scan(ptrs...); err != nil {
				_ = rows.Close()
				return "", err
			}
			literals := make([]string, len(vals))
			for i, v := range vals {
				literals[i] = sqlLiteral(v)
			}
			fmt.Fprintf(&b, "INSERT INTO %s(%s) VALUES(%s);\n", table, list, strings.Join(literals, ","))
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return "", err
		}
	}
	return b.String(), nil
}

func (s *Store) dumpColumns(ctx context.Context, table string) ([]string, error) {
	rows, err := s.read.QueryContext(ctx, "SELECT name FROM pragma_table_info(?) ORDER BY cid", table)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var cols []string
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			return nil, err
		}
		if !receiveTimes[name] {
			cols = append(cols, name)
		}
	}
	return cols, rows.Err()
}

func sqlLiteral(v any) string {
	switch x := v.(type) {
	case nil:
		return "NULL"
	case []byte:
		return "X'" + strings.ToUpper(hex.EncodeToString(x)) + "'"
	case string:
		return "'" + strings.ReplaceAll(x, "'", "''") + "'"
	}
	return fmt.Sprint(v)
}
