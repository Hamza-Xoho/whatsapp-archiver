package store

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// ErrInUse is returned by OpenExclusive when another connection has the archive open, normally the running service.
var ErrInUse = errors.New("archive is in use by another connection")

// OpenExclusive opens an existing archive for a maintenance command (rebuild-derived): it takes SQLite's exclusive
// lock at once and keeps it until Close, so it fails with ErrInUse next to a running service (one writer, AGENTS.md)
// and nothing else can open the archive meanwhile. It does not create the archive or its folder. Reads and writes use
// the one connection.
func OpenExclusive(path string) (*Store, error) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("no archive at %s: run archiver first", path)
		}
		return nil, fmt.Errorf("stat archive: %w", err)
	}
	dsn := "file:" + filepath.ToSlash(path) + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(0)&_pragma=locking_mode(EXCLUSIVE)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open archive: %w", err)
	}
	db.SetMaxOpenConns(1)
	// The first read takes the lock; in WAL mode it fails while any other connection has the archive open.
	if err = checkSchema(db); err != nil {
		_ = db.Close()
		var se *sqlite.Error
		if errors.As(err, &se) && (se.Code()&0xff == sqlite3.SQLITE_BUSY || se.Code()&0xff == sqlite3.SQLITE_LOCKED) {
			return nil, ErrInUse
		}
		return nil, err
	}
	if err = migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{write: db, read: db, path: path}, nil
}
