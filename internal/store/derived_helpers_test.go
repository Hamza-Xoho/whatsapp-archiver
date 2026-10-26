package store_test

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"whatsapp-archiver/internal/store"
)

// Sender used by the helpers; it has a participant row but never a chat_stats row (it is not a chat).
const alice = "111@lid"

// mb is a message batch: the chat row is written only when chatName is given, the sender row always.
func mb(chat, id, kind, text string, ts int64, edit ...func(*store.Message)) store.Batch {
	m := store.Message{ChatJID: chat, ID: id, SenderJID: alice, Kind: kind, Text: text, Source: "live", Timestamp: ts, ReceivedAt: ts, RawProto: []byte(id)}
	for _, f := range edit {
		f(&m)
	}
	return store.Batch{Sender: &store.Participant{JID: alice, PushName: "Alice", UpdatedAt: ts}, Message: &m}
}

func withChat(b store.Batch, name, typ string) store.Batch {
	b.Chat = &store.Chat{JID: b.Message.ChatJID, Name: name, Type: typ}
	return b
}

func withMedia(b store.Batch, state string, size int64, filename string) store.Batch {
	m := b.Message
	b.Media = &store.Media{ChatJID: m.ChatJID, MessageID: m.ID, SenderJID: m.SenderJID, Source: m.Source, Type: m.Kind, State: state, DeclaredSize: size, Filename: filename, MessageTimestamp: m.Timestamp}
	return b
}

func ev(chat, target, kind, id string, ts int64, edit ...func(*store.MessageEvent)) store.Batch {
	e := store.MessageEvent{ChatJID: chat, TargetID: target, Kind: kind, EventID: id, SenderJID: alice, Source: "live", Timestamp: ts, ReceivedAt: ts}
	for _, f := range edit {
		f(&e)
	}
	return store.Batch{Event: &e}
}

// rawDB opens a second read-write connection (one connection, so temp tables stay) for the tests that create fts5vocab
// tables or run DDL.
func rawDB(t *testing.T, s *store.Store) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(s.Path())+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// table renders every row of query, one line per row, NULL as NULL.
func table(t *testing.T, db *sql.DB, query string, args ...any) string {
	t.Helper()
	rows, err := db.Query(query, args...)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	defer func() { _ = rows.Close() }()
	cols, _ := rows.Columns()
	var b strings.Builder
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err = rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		parts := make([]string, len(vals))
		for i, v := range vals {
			switch x := v.(type) {
			case nil:
				parts[i] = "NULL"
			case []byte:
				parts[i] = string(x)
			default:
				parts[i] = fmt.Sprint(x)
			}
		}
		fmt.Fprintln(&b, strings.Join(parts, "|"))
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// vocab lists term, doc (the rowid), column and offset of every token of an FTS table: what the index holds, whatever
// order it was filled in (spike 8: fts5vocab instance table).
func vocab(t *testing.T, db *sql.DB, fts string) string {
	t.Helper()
	if _, err := db.Exec(`CREATE VIRTUAL TABLE temp.v_` + fts + ` USING fts5vocab(main, '` + fts + `', 'instance')`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = db.Exec(`DROP TABLE temp.v_` + fts) }()
	return table(t, db, `SELECT term, doc, col, offset FROM temp.v_`+fts+` ORDER BY term, doc, col, offset`)
}

// docs lists the rowids that have at least one token in an FTS table.
func docs(t *testing.T, db *sql.DB, fts string) string {
	t.Helper()
	if _, err := db.Exec(`CREATE VIRTUAL TABLE temp.d_` + fts + ` USING fts5vocab(main, '` + fts + `', 'instance')`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = db.Exec(`DROP TABLE temp.d_` + fts) }()
	return strings.ReplaceAll(strings.TrimSpace(table(t, db, `SELECT DISTINCT doc FROM temp.d_`+fts+` ORDER BY doc`)), "\n", ",")
}

// derivedState is everything RebuildDerived is supposed to reproduce.
func derivedState(t *testing.T, s *store.Store) string {
	t.Helper()
	db := rawDB(t, s)
	defer func() { _ = db.Close() }()
	return "chat_canonical\n" + table(t, db, `SELECT alias, canonical FROM chat_canonical ORDER BY alias`) +
		"chat_stats\n" + table(t, db, `SELECT * FROM chat_stats ORDER BY canonical`) +
		"search_fts\n" + vocab(t, db, "search_fts") +
		"search_tri\n" + vocab(t, db, "search_tri")
}

// ids runs a MATCH query and returns the rowids in order.
func ids(t *testing.T, s *store.Store, fts, query string) string {
	t.Helper()
	db := readDB(t, s)
	rows, err := db.Query(`SELECT rowid FROM `+fts+` WHERE `+fts+` MATCH ? ORDER BY rowid`, `"`+strings.ReplaceAll(store.Fold(query), `"`, `""`)+`"`)
	if err != nil {
		t.Fatalf("%s %q: %v", fts, query, err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprint(id))
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(out, ",")
}
