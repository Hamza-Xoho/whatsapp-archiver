package main

import (
	"context"
	"log/slog"

	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/store"
)

// liveSettings reads the settings the UI changes at runtime from the archive at each use, so a change applies to the
// next media item, the next health check and the next backup decision. A setting that is missing or cannot be read
// falls back to config.yaml's value, so capture never stops on a settings read.
type liveSettings struct {
	st      *store.Store
	cfg     *config.Config
	log     *slog.Logger
	rules   *ingest.RuleSet
	aliases func(string) ([]string, error)
}

// media is the global media rules (setting media.rules).
func (l liveSettings) media() config.MediaRules {
	r, ok, err := l.st.MediaRules(context.Background())
	if err != nil {
		l.log.Warn("media rules setting not read, using config.yaml", "err", err)
	}
	if err != nil || !ok {
		return l.cfg.Media
	}
	return r
}

// chatMedia is a chat's own media rules, set on this JID or on one of its aliases (CON-7), or nil for the global ones.
func (l liveSettings) chatMedia(jid string) *config.MediaRules {
	if r := l.rules.Media(jid); r != nil {
		return r
	}
	aliases, err := l.aliases(jid)
	if err != nil {
		l.log.Warn("aliases not read for the chat media rules, using the global rules", "err", err)
		return nil
	}
	for _, alias := range aliases {
		if r := l.rules.Media(alias); r != nil {
			return r
		}
	}
	return nil
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
