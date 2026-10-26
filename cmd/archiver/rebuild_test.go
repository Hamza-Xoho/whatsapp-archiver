package main

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"whatsapp-archiver/internal/store"
)

func statsDump(t *testing.T, dir string) string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(dir, "archive.db"))+"?mode=ro")
	mustDo(t, err)
	defer func() { _ = db.Close() }()
	rows, err := db.Query(`SELECT canonical, name, messages, last_ts, oldest_ts, media_failed, media_pending, gaps_open FROM chat_stats ORDER BY canonical`)
	mustDo(t, err)
	defer func() { _ = rows.Close() }()
	var b strings.Builder
	for rows.Next() {
		var c, n string
		var m, l, o, f, p, g int64
		mustDo(t, rows.Scan(&c, &n, &m, &l, &o, &f, &p, &g))
		fmt.Fprintf(&b, "%s|%s|%d|%d|%d|%d|%d|%d\n", c, n, m, l, o, f, p, g)
	}
	mustDo(t, rows.Err())
	return b.String()
}

// `archiver rebuild-derived --data <dir>` recreates chat_canonical, chat_stats and the search index from the raw tables.
func TestRebuildDerivedCommandRestoresDamagedTables(t *testing.T) {
	dir := seedFolder(t)
	want := statsDump(t, dir)
	if !strings.Contains(want, familyJID) {
		t.Fatalf("seed has no stats for the family chat:\n%s", want)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(dir, "archive.db")))
	mustDo(t, err)
	_, err = db.Exec(`DELETE FROM chat_stats; DELETE FROM search_fts; DELETE FROM search_tri`)
	mustDo(t, err)
	mustDo(t, db.Close())
	if got := statsDump(t, dir); got != "" {
		t.Fatalf("damage did not apply: %q", got)
	}
	code, out, errOut := runCmd(t, "rebuild-derived", "--data", dir)
	if code != 0 || !strings.Contains(out, "rebuilt") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
	}
	if got := statsDump(t, dir); got != want {
		t.Fatalf("stats after the rebuild:\n%s\nwant:\n%s", got, want)
	}
}

// One writer: with the archive open (the service running) the command refuses and changes nothing.
func TestRebuildDerivedCommandRefusesWhileTheServiceHasTheArchiveOpen(t *testing.T) {
	dir := seedFolder(t)
	service, err := store.Open(filepath.Join(dir, "archive.db"))
	mustDo(t, err)
	defer func() { _ = service.Close() }()
	code, out, errOut := runCmd(t, "rebuild-derived", "--data", dir)
	if code != 1 || out != "" || !strings.Contains(errOut, "stop the service") {
		t.Fatalf("code=%d out=%q err=%q; want 1, nothing printed and 'stop the service'", code, out, errOut)
	}
}

func TestRebuildDerivedCommandWithoutAnArchive(t *testing.T) {
	code, _, errOut := runCmd(t, "rebuild-derived", "--data", t.TempDir())
	if code != 1 || !strings.Contains(errOut, "no archive") {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
}
