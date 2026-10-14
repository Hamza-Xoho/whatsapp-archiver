package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
	_ "time/tzdata" // zone names resolve on hosts without a zoneinfo database (Windows, minimal containers)
	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/health"
	"whatsapp-archiver/internal/store"
)

const timeLayout = "2006-01-02 15:04 MST"

// location is the configured time zone; empty means UTC.
func location(cfg *config.Config) (*time.Location, error) {
	if cfg.TimeZone == "" {
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(cfg.TimeZone)
	if err != nil {
		return nil, fmt.Errorf("time_zone %q: %w", cfg.TimeZone, err)
	}
	return loc, nil
}

func formatTime(loc *time.Location, t time.Time) string {
	if t.IsZero() || t.Unix() == 0 {
		return "-"
	}
	return t.In(loc).Format(timeLayout)
}
func formatUnix(loc *time.Location, sec int64) string { return formatTime(loc, time.Unix(sec, 0)) }

// humanSize formats n bytes with one decimal as B, KB, MB or GB (powers of 1024).
func humanSize(n int64) string {
	v, unit := float64(n), 0
	for ; v >= 1024 && unit < 3; unit++ {
		v /= 1024
	}
	return fmt.Sprintf("%.1f %s", v, []string{"B", "KB", "MB", "GB"}[unit])
}

// dirSize sums the sizes of the regular files under dir; a file that disappears while walking is skipped.
func dirSize(dir string) (int64, error) {
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

// readHealth loads health.json; the second result is the state line when the file is missing or unreadable.
func readHealth(dir string) (health.Snapshot, string) {
	var snap health.Snapshot
	data, err := os.ReadFile(filepath.Join(dir, "health.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return snap, "unknown (health.json not found)"
	}
	if err == nil {
		err = json.Unmarshal(data, &snap)
	}
	if err != nil {
		return snap, fmt.Sprintf("unknown (health.json unreadable: %v)", err)
	}
	state := snap.State
	if state == "" {
		state = "unknown"
	}
	if snap.Detail != "" {
		state += " (" + snap.Detail + ")"
	}
	return snap, state
}

func historyLine(loc *time.Location, c store.Coverage) string {
	if c.Chunks == 0 {
		return "history: none received yet"
	}
	chunks := fmt.Sprintf("chunks %d, last chunk %s", c.Chunks, formatUnix(loc, c.LastChunkTime))
	if !c.Complete {
		return fmt.Sprintf("history: in progress (%d%%), %s", c.LastProgress, chunks)
	}
	oldest := "-"
	first := true
	var min int64
	for _, ts := range c.OldestByChat {
		if first || ts < min {
			min, first = ts, false
		}
	}
	if !first && min != 0 {
		oldest = time.Unix(min, 0).In(loc).Format("2006-01-02")
	}
	return fmt.Sprintf("history: complete back to %s, %s", oldest, chunks)
}

func printStatus(cfg *config.Config, out io.Writer) error {
	loc, err := location(cfg)
	if err != nil {
		return err
	}
	ctx := context.Background()
	st, err := store.OpenReadOnly(filepath.Join(cfg.DataDir, "archive.db"))
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	coverage, err := st.HistoryCoverage(ctx)
	if err != nil {
		return fmt.Errorf("read history coverage: %w", err)
	}
	stats, err := st.ChatStats(ctx)
	if err != nil {
		return fmt.Errorf("read chat statistics: %w", err)
	}
	size, err := dirSize(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("measure data folder: %w", err)
	}
	snap, state := readHealth(cfg.DataDir)
	version := "-"
	if snap.Version != "" {
		version = snap.Version
		if snap.VersionReason != "" {
			version += " (" + snap.VersionReason + ")"
		}
	}
	p := func(format string, a ...any) { _, _ = fmt.Fprintf(out, format+"\n", a...) }
	p("state: %s", state)
	p("client version: %s", version)
	p("last event: %s", formatTime(loc, snap.LastEvent))
	p("phone last active: %s", formatTime(loc, snap.PhoneLastActive))
	p("%s", historyLine(loc, coverage))
	p("name\tcanonical JID\tmessages\toldest\tnewest\tmedia failed\tmedia expired\tmedia pending\topen gaps")
	var messages, failed, expired, pending, gaps int
	for _, s := range stats {
		p("%s\t%s\t%d\t%s\t%s\t%d\t%d\t%d\t%d", s.Name, s.CanonicalJID, s.MessageCount, formatUnix(loc, s.Oldest), formatUnix(loc, s.Newest), s.MediaFailed, s.MediaExpired, s.MediaPending, s.OpenGaps)
		messages += s.MessageCount
		failed += s.MediaFailed
		expired += s.MediaExpired
		pending += s.MediaPending
		gaps += s.OpenGaps
	}
	p("total\t%d chats\t%d\t-\t-\t%d\t%d\t%d\t%d", len(stats), messages, failed, expired, pending, gaps)
	p("recovered panics: %d", snap.Panics)
	p("data folder: %s", humanSize(size))
	return nil
}

func printChats(cfg *config.Config, out io.Writer) error {
	if _, err := location(cfg); err != nil {
		return err
	}
	ctx := context.Background()
	st, err := store.OpenReadOnly(filepath.Join(cfg.DataDir, "archive.db"))
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	stats, err := st.ChatStats(ctx)
	if err != nil {
		return fmt.Errorf("read chat statistics: %w", err)
	}
	excluded := map[string]bool{}
	for _, v := range cfg.Exclude {
		excluded[v] = true
	}
	for _, s := range stats {
		aliases, err := st.Aliases(ctx, s.CanonicalJID)
		if err != nil {
			return fmt.Errorf("read aliases of %s: %w", s.CanonicalJID, err)
		}
		hit := excluded[s.CanonicalJID]
		for _, a := range aliases {
			hit = hit || excluded[a]
		}
		answer := "no"
		if hit {
			answer = "yes"
		}
		_, _ = fmt.Fprintf(out, "%s\t%s\t%d\texcluded=%s\n", s.CanonicalJID, s.Name, s.MessageCount, answer)
	}
	return nil
}
