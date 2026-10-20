package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"testing/fstest"

	_ "modernc.org/sqlite"
)

func openSQL(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func versions(t *testing.T, s *Store) []int {
	t.Helper()
	v, err := s.SchemaVersions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func tableNames(t *testing.T, s *Store) []string {
	t.Helper()
	rows, err := s.read.Query("SELECT name FROM sqlite_master WHERE type='table'")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	return out
}

func TestFreshArchiveHasVersions1And2(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "archive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if got := versions(t, s); !slices.Equal(got, []int{1, 2}) {
		t.Fatalf("versions = %v, want [1 2]", got)
	}
}

func TestUpgradeFromVersion1KeepsRowsAndAddsSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archive.db")
	v1, err := os.ReadFile("migrations/0001_raw.sql")
	if err != nil {
		t.Fatal(err)
	}
	db := openSQL(t, path)
	if _, err = db.Exec(string(v1)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO schema_version(version,applied_at) VALUES(1,1)"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"A", "B", "C"} {
		if _, err = db.Exec(`INSERT INTO messages(chat_jid,id,ts,received_at,source,raw_proto) VALUES('g@g.us',?,1,1,'live',x'00')`, id); err != nil {
			t.Fatal(err)
		}
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if got := versions(t, s); !slices.Equal(got, []int{1, 2}) {
		t.Fatalf("versions = %v, want [1 2]", got)
	}
	var n int
	if err = s.read.QueryRow("SELECT COUNT(*) FROM messages").Scan(&n); err != nil || n != 3 {
		t.Fatalf("messages = %d, %v; want 3", n, err)
	}
	have := tableNames(t, s)
	for _, want := range []string{"settings", "chat_rules", "chat_seen"} {
		if !slices.Contains(have, want) {
			t.Errorf("table %s missing; have %v", want, have)
		}
	}
	// the columns and defaults the later tasks rely on
	if _, err = s.write.Exec("INSERT INTO chat_rules(jid,updated_at) VALUES('x@s.whatsapp.net',5)"); err != nil {
		t.Fatal(err)
	}
	var logging int
	var media string
	if err = s.read.QueryRow("SELECT logging, media FROM chat_rules WHERE jid='x@s.whatsapp.net'").Scan(&logging, &media); err != nil || logging != 1 || media != "" {
		t.Fatalf("chat_rules defaults = %d %q, %v", logging, media, err)
	}
	if _, err = s.write.Exec("INSERT INTO chat_seen(jid,first_seen) VALUES('x@s.whatsapp.net',5)"); err != nil {
		t.Fatal(err)
	}
	var ack int
	if err = s.read.QueryRow("SELECT acknowledged FROM chat_seen").Scan(&ack); err != nil || ack != 0 {
		t.Fatalf("acknowledged default = %d, %v", ack, err)
	}
	if _, err = s.write.Exec("INSERT INTO settings(key,value,updated_at) VALUES('k','v',1)"); err != nil {
		t.Fatal(err)
	}
}

func TestReopenAppliesNothingTwice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archive.db")
	for range 2 {
		s, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.Close(); err != nil {
			t.Fatal(err)
		}
	}
	db := openSQL(t, path)
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_version").Scan(&n); err != nil || n != 2 {
		t.Fatalf("schema_version rows = %d, %v; want 2", n, err)
	}
}

func TestMigrationsApplyInFileOrder(t *testing.T) {
	fsys := fstest.MapFS{
		"m/10_c.sql":  {Data: []byte("INSERT INTO trail(n) VALUES(10);")},
		"m/2_b.sql":   {Data: []byte("INSERT INTO trail(n) VALUES(2);")},
		"m/1_a.sql":   {Data: []byte("CREATE TABLE trail(n INTEGER); INSERT INTO trail(n) VALUES(1);")},
		"m/README.md": {Data: []byte("not a migration")},
	}
	db := openSQL(t, filepath.Join(t.TempDir(), "m.db"))
	for range 2 { // the second run must apply nothing (it would fail on CREATE TABLE trail)
		if err := migrateFS(db, fsys, "m"); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := db.Query("SELECT n FROM trail ORDER BY rowid")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var got []int
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		got = append(got, n)
	}
	if !slices.Equal(got, []int{1, 2, 10}) {
		t.Fatalf("applied order = %v, want [1 2 10]", got)
	}
}

func TestFailedMigrationRollsBackAndRecordsNothing(t *testing.T) {
	fsys := fstest.MapFS{
		"m/1_a.sql": {Data: []byte("CREATE TABLE a(n INTEGER);")},
		"m/2_b.sql": {Data: []byte("CREATE TABLE b(n INTEGER); NOT SQL;")},
	}
	db := openSQL(t, filepath.Join(t.TempDir(), "m.db"))
	if err := migrateFS(db, fsys, "m"); err == nil {
		t.Fatal("broken migration succeeded")
	}
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name='b'").Scan(&n); err != nil || n != 0 {
		t.Fatalf("table b exists after a failed migration: %d, %v", n, err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_version WHERE version=2").Scan(&n); err != nil || n != 0 {
		t.Fatalf("version 2 recorded after failure: %d, %v", n, err)
	}
}
