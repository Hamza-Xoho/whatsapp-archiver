package main

import (
	"context"
	"fmt"
	"log/slog"

	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/health"
	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/store"
)

// liveSettings reads the settings the UI changes at runtime from the archive at each use, so a change applies to the
// next media item, the next health check and the next backup decision. A missing setting (an archive not yet
// imported) falls back to config.yaml's value. A failed read of a media rule is an error, so the pipeline retries and
// alerts instead of guessing; the alert thresholds and the backup schedule fall back to config.yaml.
type liveSettings struct {
	st      *store.Store
	cfg     *config.Config
	log     *slog.Logger
	rules   *ingest.RuleSet
	aliases func(string) ([]string, error)
}

// media is the global media rules (setting media.rules). A failed read is marked as a read, so its alert is titled
// as one.
func (l liveSettings) media() (config.MediaRules, error) {
	r, ok, err := l.st.MediaRules(context.Background())
	if err != nil {
		return config.MediaRules{}, fmt.Errorf("%w: %w", health.ErrRead, err)
	}
	if !ok {
		return l.cfg.Media, nil
	}
	return r, nil
}

// chatMedia is a chat's own media rules, set on this JID or on one of its aliases (CON-7), or nil for the global ones.
// A failed alias lookup is an error, never "no rule".
func (l liveSettings) chatMedia(jid string) (*config.MediaRules, error) {
	if r := l.rules.Media(jid); r != nil {
		return r, nil
	}
	aliases, err := l.aliases(jid)
	if err != nil {
		return nil, err
	}
	for _, alias := range aliases {
		if r := l.rules.Media(alias); r != nil {
			return r, nil
		}
	}
	return nil, nil
}

// alerts is the alert thresholds (setting alerts.thresholds); the monitor keeps ntfy from config.yaml.
func (l liveSettings) alerts() config.AlertConfig {
	a, ok, err := l.st.AlertThresholds(context.Background())
	if err != nil {
		l.log.Warn("alert thresholds setting not read, using config.yaml", "err", err)
	}
	if err != nil || !ok {
		return l.cfg.Alerts
	}
	return a
}

// backup is the backup time and keep (setting backup.schedule); the destination stays config.yaml's.
func (l liveSettings) backup() config.BackupConfig {
	b, ok, err := l.st.BackupSchedule(context.Background())
	if err != nil {
		l.log.Warn("backup schedule setting not read, using config.yaml", "err", err)
	}
	if err != nil || !ok {
		return l.cfg.Backup
	}
	return b
}
