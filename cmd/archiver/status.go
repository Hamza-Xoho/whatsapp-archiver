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
	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/health"
	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/view"
)

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

func printStatus(cfg *config.Config, out io.Writer) error {
	loc, err := view.Location(cfg)
	if err != nil {
		return err
	}
	ctx := context.Background()
	st, err := store.OpenReadOnly(filepath.Join(cfg.DataDir, "archive.db"))
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	report, err := st.StatusReport(ctx)
	if err != nil {
		return fmt.Errorf("read status: %w", err)
	}
	outages, err := st.OutageSummary(ctx, time.Now(), loc, 7)
	if err != nil {
		return fmt.Errorf("read outage statistics: %w", err)
	}
	coverage, stats := report.Coverage, report.Chats
	size, err := view.DirSize(cfg.DataDir)
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
	p("last event: %s", view.FormatTime(loc, snap.LastEvent))
	p("phone last active: %s", view.FormatTime(loc, snap.PhoneLastActive))
	p("%s", view.HistoryLine(loc, coverage))
	p("name\tcanonical JID\tmessages\toldest\tnewest\tmedia failed\tmedia expired\tmedia pending\topen gaps")
	var messages, failed, expired, pending, gaps int
	for _, s := range stats {
		p("%s\t%s\t%d\t%s\t%s\t%d\t%d\t%d\t%d", s.Name, s.CanonicalJID, s.MessageCount, view.FormatUnix(loc, s.Oldest), view.FormatUnix(loc, s.Newest), s.MediaFailed, s.MediaExpired, s.MediaPending, s.OpenGaps)
		messages += s.MessageCount
		failed += s.MediaFailed
		expired += s.MediaExpired
		pending += s.MediaPending
		gaps += s.OpenGaps
	}
	p("total\t%d chats\t%d\t-\t-\t%d\t%d\t%d\t%d", len(stats), messages, failed, expired, pending, gaps)
	if outages.HasOutages {
		for _, day := range outages.Days {
			if day.Minutes > 0 {
				p("outage minutes %s: %d", day.Date, day.Minutes)
			}
		}
		p("longest outage: %d minutes", roundedStatusMinutes(outages.Longest))
	}
	p("recovered panics: %d", snap.Panics)
	p("data folder: %s", view.HumanSize(size))
	return nil
}

func roundedStatusMinutes(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	return int((d + time.Minute - 1) / time.Minute)
}

func printChats(cfg *config.Config, out io.Writer) error {
	if _, err := view.Location(cfg); err != nil {
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
	// After the import the archive's chat rules are what capture applies; before it (an archive the new build has
	// not started on yet) config.yaml's exclude list still is.
	rules := ingest.ExclusionRules(cfg.Exclude)
	imported, err := st.ConfigImported(ctx)
	if err != nil {
		return fmt.Errorf("read chat rules: %w", err)
	}
	if imported {
		if rules, err = st.ChatRules(ctx); err != nil {
			return fmt.Errorf("read chat rules: %w", err)
		}
	}
	filter := ingest.NewFilter(ingest.NewRuleSet(rules), func(jid string) ([]string, error) { return st.Aliases(ctx, jid) })
	for _, s := range stats {
		hit, err := filter.Excluded(s.CanonicalJID)
		if err != nil {
			return fmt.Errorf("read aliases of %s: %w", s.CanonicalJID, err)
		}
		answer := "no"
		if hit {
			answer = "yes"
		}
		_, _ = fmt.Fprintf(out, "%s\t%s\t%d\texcluded=%s\n", s.CanonicalJID, s.Name, s.MessageCount, answer)
	}
	return nil
}
