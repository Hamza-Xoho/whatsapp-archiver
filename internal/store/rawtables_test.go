package store

import (
	"go/scanner"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// CON-2: the raw tables are insert-only.
const rawTableNames = `(?:messages|message_events|chat_events|id_mappings|history_sync_chunks)`

// rawWrites matches a statement that overwrites or removes a row of a raw table: UPDATE, DELETE, REPLACE INTO
// (so INSERT OR REPLACE too), and an INSERT ... ON CONFLICT ... DO UPDATE upsert.
var rawWrites = []*regexp.Regexp{
	regexp.MustCompile(`(?is)\bUPDATE\s+(?:OR\s+\w+\s+)?["'` + "`" + `\[]?` + rawTableNames + `\b`),
	regexp.MustCompile(`(?is)\bDELETE\s+FROM\s+["'` + "`" + `\[]?` + rawTableNames + `\b`),
	regexp.MustCompile(`(?is)\bREPLACE\s+INTO\s+["'` + "`" + `\[]?` + rawTableNames + `\b`),
	regexp.MustCompile(`(?is)\bINSERT\s+(?:OR\s+\w+\s+)?INTO\s+["'` + "`" + `\[]?` + rawTableNames + `\b[^;` + "`" + `]*?\bDO\s+UPDATE\b`),
}

func rawWriteIn(sql string) string {
	for _, re := range rawWrites {
		if m := re.FindString(sql); m != "" {
			return strings.Join(strings.Fields(m), " ")
		}
	}
	return ""
}

// stringLiterals returns the value of every string literal in Go source; comments are not SQL.
func stringLiterals(t *testing.T, path string, src []byte) []string {
	t.Helper()
	fset := token.NewFileSet()
	file := fset.AddFile(path, fset.Base(), len(src))
	var s scanner.Scanner
	s.Init(file, src, func(pos token.Position, msg string) { t.Fatalf("scan %s: %s: %s", path, pos, msg) }, 0)
	var out []string
	for {
		_, tok, lit := s.Scan()
		if tok == token.EOF {
			return out
		}
		if tok == token.STRING {
			value, err := strconv.Unquote(lit)
			if err != nil {
				t.Fatalf("%s: unquote %s: %v", path, lit, err)
			}
			out = append(out, value)
		}
	}
}

func TestRawWriteDetectorRecognisesTheForbiddenForms(t *testing.T) {
	bad := []string{
		"UPDATE messages SET text='x'",
		"update  message_events set value=1",
		"DELETE FROM chat_events WHERE 1",
		"INSERT OR REPLACE INTO id_mappings(lid,pn) VALUES(?,?)",
		"REPLACE INTO history_sync_chunks(id) VALUES(?)",
		"INSERT INTO messages(chat_jid,id) VALUES(?,?) ON CONFLICT(chat_jid,id) DO UPDATE SET text=excluded.text",
		"UPDATE OR IGNORE messages SET text=''",
	}
	for _, sql := range bad {
		if rawWriteIn(sql) == "" {
			t.Errorf("not detected: %s", sql)
		}
	}
	good := []string{
		"INSERT INTO messages(chat_jid,id) VALUES(?,?) ON CONFLICT DO NOTHING",
		"UPDATE media SET state='done'",
		"INSERT INTO chats(jid) VALUES(?) ON CONFLICT(jid) DO UPDATE SET name=excluded.name",
		"SELECT * FROM messages_resolved",
		"CREATE VIEW v AS SELECT m.* FROM messages m",
		"DELETE FROM media WHERE 1",
	}
	for _, sql := range good {
		if got := rawWriteIn(sql); got != "" {
			t.Errorf("false positive %q on: %s", got, sql)
		}
	}
}

// Review section 4 / CON-2: no non-test code under internal/ or cmd/ updates, deletes or replaces a raw row.
func TestNoUpdateOrDeleteOnRawTables(t *testing.T) {
	scanned, sawStore, sawMigration := 0, false, false
	for _, root := range []string{"..", filepath.Join("..", "..", "cmd")} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && d.Name() == "testdata" {
				return filepath.SkipDir
			}
			name := d.Name()
			if d.IsDir() || strings.HasSuffix(name, "_test.go") || (!strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, ".sql")) {
				return nil
			}
			src, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			scanned++
			sawStore = sawStore || filepath.Base(path) == "store.go"
			sawMigration = sawMigration || strings.HasSuffix(name, ".sql")
			chunks := []string{string(src)}
			if strings.HasSuffix(name, ".go") {
				chunks = stringLiterals(t, path, src)
			}
			for _, sql := range chunks {
				if hit := rawWriteIn(sql); hit != "" {
					t.Errorf("%s: raw table is not insert-only: %q", path, hit)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if scanned < 20 || !sawStore || !sawMigration {
		t.Fatalf("scanned %d files (store.go %v, migration %v): the walk missed the code it guards", scanned, sawStore, sawMigration)
	}
}

// Hard constraint: exactly one connection writes archive.db. The write pool is capped at one connection and the
// read pool cannot write.
func TestOnlyOneWriteConnection(t *testing.T) {
	s := openInternal(t)
	if n := s.write.Stats().MaxOpenConnections; n != 1 {
		t.Fatalf("write pool MaxOpenConnections = %d, want 1", n)
	}
	if _, err := s.read.Exec(`INSERT INTO chats(jid) VALUES('x@g.us')`); err == nil {
		t.Fatal("the read pool accepted a write")
	}
}
