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

type Report struct {
	Snapshot string
	Bytes    int64
	Duration time.Duration
}
type Clock interface {
	Now() time.Time
	After(time.Duration) <-chan time.Time
}

func Run(ctx context.Context, cfg *config.Config) (Report, error) {
	start := time.Now()
	var report Report
	if cfg.Backup.Destination == "" {
		return report, fmt.Errorf("backup destination is required")
	}
	if err := os.MkdirAll(cfg.Backup.Destination, 0o700); err != nil {
		return report, err
	}
	folder := filepath.Join(cfg.Backup.Destination, start.UTC().Format("20060102T150405.000000000Z"))
	if err := os.Mkdir(folder, 0o700); err != nil {
		return report, err
	}
	target := filepath.Join(folder, "archive.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(cfg.DataDir, "archive.db"))+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return report, err
	}
	defer func() { _ = db.Close() }()
	if _, err = db.ExecContext(ctx, "VACUUM INTO ?", target); err != nil {
		return report, fmt.Errorf("snapshot archive: %w", err)
	}
	if err = Verify(target); err != nil {
		return report, err
	}
	info, err := os.Stat(target)
	if err != nil {
		return report, err
	}
	report = Report{Snapshot: target, Bytes: info.Size(), Duration: time.Since(start)}
	if err = retain(cfg.Backup.Destination, cfg.Backup.Keep); err != nil {
		return report, err
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
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	sort.Strings(dirs)
	for len(dirs) > keep {
		if err = os.RemoveAll(filepath.Join(dest, dirs[0])); err != nil {
			return err
		}
		dirs = dirs[1:]
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
