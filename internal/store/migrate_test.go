package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// ADR-35: the V1 schema is one squashed migration; a fresh archive records version 1 and the archive_meta marker.
func TestFreshArchiveHasVersion1AndArchiveMeta(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "archive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if got := versions(t, s); !slices.Equal(got, []int{1}) {
		t.Fatalf("versions = %v, want [1]", got)
	}
	var v string
	if err = s.read.QueryRow("SELECT value FROM archive_meta WHERE key='schema'").Scan(&v); err != nil || v != "v1" {
		t.Fatalf("archive_meta schema = %q, %v; want v1", v, err)
	}
}

func objectNames(t *testing.T, s *Store, kind string) []string {
	t.Helper()
	rows, err := s.read.Query("SELECT name FROM sqlite_master WHERE type=? ORDER BY name", kind)
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

// schema.md section 3: every table the V1 design lists exists on a fresh archive; the two dropped views do not.
func TestFreshArchiveHasEveryV1Table(t *testing.T) {
	s := openInternal(t)
	have := tableNames(t, s)
	for _, want := range []string{"schema_version", "archive_meta", "chats", "participants", "id_mappings", "messages", "message_events",
		"chat_events", "history_sync_chunks", "media", "chat_canonical", "chat_stats", "search_fts", "search_tri",
		"connection_periods", "alerts", "history_spool", "settings", "chat_rules", "chat_seen"} {
		if !slices.Contains(have, want) {
			t.Errorf("table %s missing; have %v", want, have)
		}
	}
	views := objectNames(t, s, "view")
	if !slices.Equal(views, []string{"open_gaps"}) {
		t.Fatalf("views = %v, want only open_gaps (chat_aliases and messages_resolved are dropped)", views)
	}
}

// R1: messages and message_events have an explicit stable row id; the FTS rowid depends on it.
func TestMessagesAndEventsHaveExplicitSeq(t *testing.T) {
	s := openInternal(t)
	for _, table := range []string{"messages", "message_events"} {
		var pk int
		if err := s.read.QueryRow("SELECT pk FROM pragma_table_info(?) WHERE name='seq'", table).Scan(&pk); err != nil || pk != 1 {
			t.Fatalf("%s.seq pk=%d err=%v; want an INTEGER PRIMARY KEY", table, pk, err)
		}
	}
}

// R5: the big blobs are the last column of their table, so scans never touch overflow pages for the other columns.
func TestBlobsAreTheLastColumn(t *testing.T) {
	s := openInternal(t)
	for table, blob := range map[string]string{"messages": "raw_proto", "message_events": "raw_proto", "chat_events": "raw_proto", "media": "thumb", "history_spool": "blob"} {
		var last string
		if err := s.read.QueryRow("SELECT name FROM pragma_table_info(?) ORDER BY cid DESC LIMIT 1", table).Scan(&last); err != nil || last != blob {
			t.Fatalf("%s last column = %q, %v; want %s", table, last, err, blob)
		}
	}
}

// The settings tables keep the columns and defaults the UI relies on (unchanged from 0002_settings.sql).
func TestSettingsTablesKeepTheirDefaults(t *testing.T) {
	s := openInternal(t)
	if _, err := s.write.Exec("INSERT INTO chat_rules(jid,updated_at) VALUES('x@s.whatsapp.net',5)"); err != nil {
		t.Fatal(err)
	}
	var logging int
	var media string
	if err := s.read.QueryRow("SELECT logging, media FROM chat_rules WHERE jid='x@s.whatsapp.net'").Scan(&logging, &media); err != nil || logging != 1 || media != "" {
		t.Fatalf("chat_rules defaults = %d %q, %v", logging, media, err)
	}
	if _, err := s.write.Exec("INSERT INTO chat_seen(jid,first_seen) VALUES('x@s.whatsapp.net',5)"); err != nil {
		t.Fatal(err)
	}
	var ack int
	if err := s.read.QueryRow("SELECT acknowledged FROM chat_seen").Scan(&ack); err != nil || ack != 0 {
		t.Fatalf("acknowledged default = %d, %v", ack, err)
	}
	if _, err := s.write.Exec("INSERT INTO settings(key,value,updated_at) VALUES('k','v',1)"); err != nil {
		t.Fatal(err)
	}
}

// R18: both FTS tables accept rows under an explicit rowid and answer word and substring queries (contentless, with
// delete). The tables stay empty in task (b); this pins that the migration creates them usable for task (d).
func TestSearchTablesAcceptRowsAndDeletes(t *testing.T) {
	s := openInternal(t)
	if _, err := s.write.Exec(`INSERT INTO search_fts(rowid, body) VALUES (7, 'hello world'); INSERT INTO search_tri(rowid, body) VALUES (7, 'hello world')`); err != nil {
		t.Fatal(err)
	}
	var id int
	if err := s.read.QueryRow("SELECT rowid FROM search_fts WHERE search_fts MATCH 'world'").Scan(&id); err != nil || id != 7 {
		t.Fatalf("word search = %d, %v", id, err)
	}
	if err := s.read.QueryRow("SELECT rowid FROM search_tri WHERE search_tri MATCH 'ello'").Scan(&id); err != nil || id != 7 {
		t.Fatalf("substring search = %d, %v", id, err)
	}
	if _, err := s.write.Exec(`DELETE FROM search_fts WHERE rowid = 7; DELETE FROM search_tri WHERE rowid = 7`); err != nil {
		t.Fatalf("contentless delete: %v", err)
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
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_version").Scan(&n); err != nil || n != 1 {
		t.Fatalf("schema_version rows = %d, %v; want 1", n, err)
	}
}

// preV1Archive creates a file with the schema the archive had before V1: 0001_raw.sql and 0002_settings.sql, both
// recorded in schema_version, plus a message. A copy of the old SQL is kept as a fixture (testdata/pre_v1).
func preV1Archive(t *testing.T, versions ...int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "archive.db")
	db := openSQL(t, path)
	for _, v := range versions {
		name := map[int]string{1: "0001_raw.sql", 2: "0002_settings.sql"}[v]
		body, err := os.ReadFile(filepath.Join("testdata", "pre_v1", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(string(body)); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec("INSERT INTO schema_version(version,applied_at) VALUES(?,1)", v); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO messages(chat_jid,id,ts,received_at,source,raw_proto) VALUES('g@g.us','A',1,1,'live',x'00')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

const oldArchiveMessage = "archive predates the V1 schema (ADR-35): use a fresh data folder"

// R16: an old archive already lists version 1 in schema_version, so the new 0001 would be skipped silently. Open
// refuses it with the message from the schema design, and changes nothing in the file.
func TestOpenRefusesAnArchiveFromBeforeV1(t *testing.T) {
	for name, vs := range map[string][]int{"version 1 and 2 (ryzen)": {1, 2}, "version 1 only": {1}} {
		t.Run(name, func(t *testing.T) {
			path := preV1Archive(t, vs...)
			s, err := Open(path)
			if err == nil {
				_ = s.Close()
				t.Fatal("Open accepted an archive from before V1")
			}
			if !strings.Contains(err.Error(), oldArchiveMessage) {
				t.Fatalf("err = %v, want it to say %q", err, oldArchiveMessage)
			}
			db := openSQL(t, path)
			var n int
			if err = db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name IN ('archive_meta','chat_canonical')").Scan(&n); err != nil || n != 0 {
				t.Fatalf("the refused archive was changed: %d new tables, %v", n, err)
			}
			if err = db.QueryRow("SELECT COUNT(*) FROM schema_version").Scan(&n); err != nil || n != len(vs) {
				t.Fatalf("schema_version rows = %d, %v; want %d", n, err, len(vs))
			}
		})
	}
}

// A read-only open (archiver status on the old archive) gets the same message instead of "no such table".
func TestOpenReadOnlyRefusesAnArchiveFromBeforeV1(t *testing.T) {
	path := preV1Archive(t, 1, 2)
	s, err := OpenReadOnly(path)
	if err == nil {
		_ = s.Close()
		t.Fatal("OpenReadOnly accepted an archive from before V1")
	}
	if !strings.Contains(err.Error(), oldArchiveMessage) {
		t.Fatalf("err = %v, want it to say %q", err, oldArchiveMessage)
	}
}

// An archive that is not V1 in any other way (archive_meta names another schema) is refused too, not migrated over.
func TestOpenRefusesAnotherSchemaMarker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archive.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.write.Exec("UPDATE archive_meta SET value='v2' WHERE key='schema'"); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	if s, err = Open(path); err == nil {
		_ = s.Close()
		t.Fatal("Open accepted an archive whose schema marker is v2")
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
