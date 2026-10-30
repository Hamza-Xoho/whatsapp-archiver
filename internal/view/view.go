// Package view holds the formatting helpers shared by the CLI and the web UI.
package view

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"time"
	_ "time/tzdata" // zone names resolve on hosts without a zoneinfo database (Windows, minimal containers)
	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/store"
)

const timeLayout = "2006-01-02 15:04 MST"

// Location is the configured time zone; empty means UTC.
func Location(cfg *config.Config) (*time.Location, error) {
	if cfg.TimeZone == "" {
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(cfg.TimeZone)
	if err != nil {
		return nil, fmt.Errorf("time_zone %q: %w", cfg.TimeZone, err)
	}
	return loc, nil
}

// FormatTime renders t in loc; a zero or Unix-epoch time shows as "-".
func FormatTime(loc *time.Location, t time.Time) string {
	if t.IsZero() || t.Unix() == 0 {
		return "-"
	}
	return t.In(loc).Format(timeLayout)
}

// FormatUnix renders a Unix-seconds timestamp in loc; see FormatTime.
func FormatUnix(loc *time.Location, sec int64) string { return FormatTime(loc, time.Unix(sec, 0)) }

// HumanSize formats n bytes with one decimal as B, KB, MB or GB (powers of 1024).
func HumanSize(n int64) string {
	v, unit := float64(n), 0
	for ; v >= 1024 && unit < 3; unit++ {
		v /= 1024
	}
	return fmt.Sprintf("%.1f %s", v, []string{"B", "KB", "MB", "GB"}[unit])
}

// DirSize sums the sizes of the regular files under dir; a file that disappears while walking is skipped.
func DirSize(dir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	return total, err
}

// HistoryLinesAt describes each durable history sync type. A stalled sync is incomplete and has received no chunk for
// 48 hours; it is deliberately not described as in progress.
func HistoryLinesAt(loc *time.Location, c store.Coverage, now time.Time) []string {
	if len(c.Syncs) == 0 {
		return []string{"history: none received yet"}
	}
	out := make([]string, 0, len(c.Syncs))
	for _, sync := range c.Syncs {
		state := "complete"
		if !sync.Complete {
			state = fmt.Sprintf("in progress (%d%%)", sync.LastProgress)
			if sync.LastChunkTime > 0 && !now.Before(time.Unix(sync.LastChunkTime, 0).Add(48*time.Hour)) {
				state = fmt.Sprintf("stalled at %d%% (no chunk for 48 h)", sync.LastProgress)
			}
		}
		out = append(out, fmt.Sprintf("history %s: %s, received %d, imported %d, last chunk %s", sync.SyncType, state, sync.ReceivedChunks, sync.ImportedChunks, FormatUnix(loc, sync.LastChunkTime)))
	}
	return out
}

// HistoryLine is the plain-text form used by callers that need one string.
func HistoryLine(loc *time.Location, c store.Coverage) string {
	return strings.Join(HistoryLinesAt(loc, c, time.Now()), "\n")
}
