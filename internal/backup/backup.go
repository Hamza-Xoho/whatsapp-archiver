package backup

import (
	"context"
	"database/sql"
	"fmt"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
	"sort"
	"time"
	"whatsapp-archiver/internal/config"
)

// snapshotLayout names a snapshot folder; retention only touches folders that parse with it.
const snapshotLayout = "20060102T150405.000000000Z"

type Report struct {
	Snapshot string
	Bytes    int64
	Duration time.Duration
}
type Clock interface {
	Now() time.Time
	After(time.Duration) <-chan time.Time
}

func Run(ctx context.Context, cfg *config.Config) (report Report, err error) {
	start := time.Now()
	if cfg.Backup.Destination == "" {
		return report, fmt.Errorf("backup destination is required")
	}
	if err = os.MkdirAll(cfg.Backup.Destination, 0o700); err != nil {
		return report, fmt.Errorf("create backup destination: %w", err)
	}
	folder := filepath.Join(cfg.Backup.Destination, start.UTC().Format(snapshotLayout))
	if err = os.Mkdir(folder, 0o700); err != nil {
		return report, fmt.Errorf("create snapshot folder: %w", err)
	}
	defer func() {
		if err != nil && report.Snapshot == "" {
			_ = os.RemoveAll(folder)
		}
	}()
	target := filepath.Join(folder, "archive.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(cfg.DataDir, "archive.db"))+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return report, fmt.Errorf("open archive: %w", err)
	}
	defer func() { _ = db.Close() }()
	if _, err = db.ExecContext(ctx, "VACUUM INTO ?", target); err != nil {
		return report, fmt.Errorf("snapshot archive: %w", err)
	}
	if err = Verify(target); err != nil {
		return report, fmt.Errorf("verify snapshot: %w", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		return report, fmt.Errorf("stat snapshot: %w", err)
	}
	report = Report{Snapshot: target, Bytes: info.Size(), Duration: time.Since(start)}
	if err = retain(cfg.Backup.Destination, cfg.Backup.Keep); err != nil {
		return report, fmt.Errorf("apply retention: %w", err)
	}
	return report, nil
}
func Verify(path string) error {
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	var result string
	if err = db.QueryRow("PRAGMA integrity_check").Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("integrity check: %s", result)
	}
	return nil
}
func retain(dest string, keep int) error {
	if keep < 1 {
		return nil
	}
	entries, err := os.ReadDir(dest)
	if err != nil {
		return err
	}
	type snapshot struct {
		name string
		at   time.Time
	}
	var snaps []snapshot
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if at, err := time.Parse(snapshotLayout, e.Name()); err == nil {
			snaps = append(snaps, snapshot{e.Name(), at})
		}
	}
	sort.Slice(snaps, func(i, j int) bool { return snaps[i].at.Before(snaps[j].at) })
	for len(snaps) > keep {
		if err = os.RemoveAll(filepath.Join(dest, snaps[0].name)); err != nil {
			return err
		}
		snaps = snaps[1:]
	}
	return nil
}
func Schedule(ctx context.Context, cfg *config.Config, clock Clock, onResult func(Report, error)) {
	for {
		now := clock.Now()
		target, err := time.ParseInLocation("15:04", cfg.Backup.Time, now.Location())
		if err != nil {
			onResult(Report{}, err)
			return
		}
		next := time.Date(now.Year(), now.Month(), now.Day(), target.Hour(), target.Minute(), 0, 0, now.Location())
		if !next.After(now) {
			next = next.Add(24 * time.Hour)
		}
		select {
		case <-ctx.Done():
			return
		case <-clock.After(next.Sub(now)):
			report, runErr := Run(ctx, cfg)
			onResult(report, runErr)
		}
	}
}
