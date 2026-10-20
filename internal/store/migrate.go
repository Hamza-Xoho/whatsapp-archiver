package store

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

type migration struct {
	version int
	file    string
}

// migrate applies every embedded migration that schema_version does not list yet.
func migrate(db *sql.DB) error { return migrateFS(db, migrations, "migrations") }

// migrateFS applies the NNNN_*.sql files in dir in numeric order. Each file runs in its own transaction together
// with its schema_version row, so a failed file leaves nothing behind and an applied file never runs again.
func migrateFS(db *sql.DB, fsys fs.FS, dir string) error {
	list, err := listMigrations(fsys, dir)
	if err != nil {
		return err
	}
	if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("create schema_version: %w", err)
	}
	applied, err := appliedVersions(db)
	if err != nil {
		return err
	}
	for _, m := range list {
		if applied[m.version] {
			continue
		}
		body, err := fs.ReadFile(fsys, path.Join(dir, m.file))
		if err != nil {
			return err
		}
		if err = applyMigration(db, m, string(body)); err != nil {
			return fmt.Errorf("migrate archive (%s): %w", m.file, err)
		}
	}
	return nil
}

func listMigrations(fsys fs.FS, dir string) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, err
	}
	var list []migration
	seen := map[int]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}
		num, _, _ := strings.Cut(name, "_")
		v, err := strconv.Atoi(num)
		if err != nil {
			return nil, fmt.Errorf("migration %q: name must start with a number and an underscore", name)
		}
		if prev, dup := seen[v]; dup {
			return nil, fmt.Errorf("migrations %q and %q share version %d", prev, name, v)
		}
		seen[v] = name
		list = append(list, migration{version: v, file: name})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].version < list[j].version })
	return list, nil
}

func appliedVersions(db *sql.DB) (map[int]bool, error) {
	rows, err := db.Query("SELECT version FROM schema_version")
	if err != nil {
		return nil, fmt.Errorf("read schema_version: %w", err)
	}
	defer func() { _ = rows.Close() }()
	applied := map[int]bool{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		applied[v] = true
	}
	return applied, rows.Err()
}

func applyMigration(db *sql.DB, m migration, body string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	if _, err = tx.Exec(body); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err = tx.Exec("INSERT INTO schema_version(version,applied_at) VALUES(?,?)", m.version, time.Now().Unix()); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// SchemaVersions lists the migration versions applied to the archive, ascending.
func (s *Store) SchemaVersions(ctx context.Context) ([]int, error) {
	rows, err := s.read.QueryContext(ctx, "SELECT version FROM schema_version ORDER BY version")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
