package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"whatsapp-archiver/internal/config"
)

// Settings keys. Each value is JSON. Everything else (data_dir, time_zone, history, wa, alerts.ntfy, which holds a
// token while archive.db is backed up, backup.destination, which needs systemd write access, and web) stays in
// config.yaml.
const (
	KeyMediaRules      = "media.rules"
	KeyAlertThresholds = "alerts.thresholds"
	KeyBackupSchedule  = "backup.schedule"
)

// ChatRule is the per-chat setting. Logging false means the chat is not stored. Media nil means the global media
// rules apply. A rule is keyed on the JID as given (CON-7); its aliases are resolved by the reader.
type ChatRule struct {
	JID       string
	Logging   bool
	Media     *config.MediaRules
	UpdatedAt int64
}

// ChatSeen is a chat's first sighting. A chat is new until it is acknowledged.
type ChatSeen struct {
	JID          string
	FirstSeen    int64
	Acknowledged bool
}

// alertThresholds is the JSON form of the alert thresholds; durations are Go duration strings ("6h0m0s").
type alertThresholds struct {
	NoEventsAfter     string `json:"no_events_after"`
	DisconnectedAfter string `json:"disconnected_after"`
	PhoneQuietAfter   string `json:"phone_quiet_after"`
	DiskFreeMinGB     int    `json:"disk_free_min_gb"`
}
type backupSchedule struct {
	Time string `json:"time"`
	Keep int    `json:"keep"`
}

func (s *Store) ChatRules(ctx context.Context) ([]ChatRule, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT jid,logging,media,updated_at FROM chat_rules ORDER BY jid`)
	if err != nil {
		return nil, fmt.Errorf("query chat rules: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []ChatRule
	for rows.Next() {
		var r ChatRule
		var media string
		if err = rows.Scan(&r.JID, &r.Logging, &media, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan chat rule: %w", err)
		}
		if media != "" {
			r.Media = &config.MediaRules{}
			if err = json.Unmarshal([]byte(media), r.Media); err != nil {
				return nil, fmt.Errorf("decode media rules of %s: %w", r.JID, err)
			}
		}
		out = append(out, r)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("read chat rules: %w", err)
	}
	return out, nil
}

// SetChatRules writes every rule in one transaction: a bulk change applies to all its chats or to none.
func (s *Store) SetChatRules(ctx context.Context, rules []ChatRule) error {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin chat rules: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().Unix()
	for _, r := range rules {
		if r.JID == "" {
			return errors.New("chat rule without a chat")
		}
		media := ""
		if r.Media != nil {
			b, err := json.Marshal(r.Media)
			if err != nil {
				return fmt.Errorf("encode media rules of %s: %w", r.JID, err)
			}
			media = string(b)
		}
		at := r.UpdatedAt
		if at == 0 {
			at = now
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO chat_rules(jid,logging,media,updated_at) VALUES(?,?,?,?) ON CONFLICT(jid) DO UPDATE SET logging=excluded.logging,media=excluded.media,updated_at=excluded.updated_at`, r.JID, r.Logging, media, at); err != nil {
			return fmt.Errorf("write chat rule %s: %w", r.JID, classify(err))
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit chat rules: %w", err)
	}
	return nil
}

func (s *Store) Setting(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := s.read.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read setting %s: %w", key, err)
	}
	return v, true, nil
}

func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.write.ExecContext(ctx, `INSERT INTO settings(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, key, value, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("write setting %s: %w", key, classify(err))
	}
	return nil
}

// MarkChatSeen records a chat's first sighting and reports whether this call was it.
func (s *Store) MarkChatSeen(ctx context.Context, jid string, at int64) (bool, error) {
	return s.insert(ctx, `INSERT OR IGNORE INTO chat_seen(jid,first_seen) VALUES(?,?)`, jid, at)
}

func (s *Store) SeenChats(ctx context.Context) ([]ChatSeen, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT jid,first_seen,acknowledged FROM chat_seen ORDER BY jid`)
	if err != nil {
		return nil, fmt.Errorf("query seen chats: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []ChatSeen
	for rows.Next() {
		var c ChatSeen
		if err = rows.Scan(&c.JID, &c.FirstSeen, &c.Acknowledged); err != nil {
			return nil, fmt.Errorf("scan seen chat: %w", err)
		}
		out = append(out, c)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("read seen chats: %w", err)
	}
	return out, nil
}

// AcknowledgeChats clears the "new" flag of these chats, in one transaction.
func (s *Store) AcknowledgeChats(ctx context.Context, jids []string) error {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin acknowledge: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, jid := range jids {
		if _, err = tx.ExecContext(ctx, `UPDATE chat_seen SET acknowledged=1 WHERE jid=?`, jid); err != nil {
			return fmt.Errorf("acknowledge %s: %w", jid, err)
		}
	}
	return tx.Commit()
}

// ImportConfigOnce copies config.yaml's runtime settings into the archive on the first start of a build that has the
// settings tables: each exclude entry becomes a rule with logging off, and the media rules, alert thresholds and backup
// time and keep become settings. Chats already in the archive are marked seen and acknowledged, so the upgrade does not
// flag every existing chat as new. It runs in one transaction and sets config.ImportMarker; once the marker is set it
// does nothing, so a later UI change is never undone by config.yaml.
func (s *Store) ImportConfigOnce(ctx context.Context, cfg *config.Config) (bool, error) {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin config import: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var done int
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM settings WHERE key=?)`, config.ImportMarker).Scan(&done); err != nil {
		return false, fmt.Errorf("check config import: %w", err)
	}
	if done != 0 {
		return false, nil
	}
	now := time.Now().Unix()
	for _, jid := range cfg.Exclude {
		if _, err = tx.ExecContext(ctx, `INSERT INTO chat_rules(jid,logging,media,updated_at) VALUES(?,0,'',?) ON CONFLICT(jid) DO NOTHING`, jid, now); err != nil {
			return false, fmt.Errorf("import exclusion %s: %w", jid, err)
		}
	}
	values := map[string]any{
		KeyMediaRules:       cfg.Media,
		KeyAlertThresholds:  encodeThresholds(cfg.Alerts),
		KeyBackupSchedule:   backupSchedule{Time: cfg.Backup.Time, Keep: cfg.Backup.Keep},
		config.ImportMarker: now,
	}
	for key, v := range values {
		b, err := json.Marshal(v)
		if err != nil {
			return false, fmt.Errorf("encode %s: %w", key, err)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO settings(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO NOTHING`, key, string(b), now); err != nil {
			return false, fmt.Errorf("import %s: %w", key, err)
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO chat_seen(jid,first_seen,acknowledged) SELECT jid,?,1 FROM chats`, now); err != nil {
		return false, fmt.Errorf("mark existing chats seen: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return false, fmt.Errorf("commit config import: %w", err)
	}
	return true, nil
}

// settingJSON decodes setting key into v and reports whether it is set.
func (s *Store) settingJSON(ctx context.Context, key string, v any) (bool, error) {
	raw, ok, err := s.Setting(ctx, key)
	if err != nil || !ok {
		return false, err
	}
	if err = json.Unmarshal([]byte(raw), v); err != nil {
		return false, fmt.Errorf("decode setting %s: %w", key, err)
	}
	return true, nil
}

func (s *Store) setSettingJSON(ctx context.Context, key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode setting %s: %w", key, err)
	}
	return s.SetSetting(ctx, key, string(b))
}

func (s *Store) MediaRules(ctx context.Context) (config.MediaRules, bool, error) {
	var r config.MediaRules
	ok, err := s.settingJSON(ctx, KeyMediaRules, &r)
	return r, ok, err
}
func (s *Store) SetMediaRules(ctx context.Context, r config.MediaRules) error {
	return s.setSettingJSON(ctx, KeyMediaRules, r)
}

// AlertThresholds returns the alert thresholds setting; Ntfy is always nil (it stays in config.yaml).
func (s *Store) AlertThresholds(ctx context.Context) (config.AlertConfig, bool, error) {
	var t alertThresholds
	ok, err := s.settingJSON(ctx, KeyAlertThresholds, &t)
	if err != nil || !ok {
		return config.AlertConfig{}, false, err
	}
	a := config.AlertConfig{DiskFreeMinGB: t.DiskFreeMinGB}
	for _, f := range []struct {
		dst *time.Duration
		src string
	}{{&a.NoEventsAfter, t.NoEventsAfter}, {&a.DisconnectedAfter, t.DisconnectedAfter}, {&a.PhoneQuietAfter, t.PhoneQuietAfter}} {
		if f.src == "" {
			continue
		}
		if *f.dst, err = time.ParseDuration(f.src); err != nil {
			return config.AlertConfig{}, false, fmt.Errorf("decode setting %s: %w", KeyAlertThresholds, err)
		}
	}
	return a, true, nil
}

// SetAlertThresholds writes the thresholds of a; a.Ntfy is never written.
func (s *Store) SetAlertThresholds(ctx context.Context, a config.AlertConfig) error {
	return s.setSettingJSON(ctx, KeyAlertThresholds, encodeThresholds(a))
}

func encodeThresholds(a config.AlertConfig) alertThresholds {
	return alertThresholds{NoEventsAfter: a.NoEventsAfter.String(), DisconnectedAfter: a.DisconnectedAfter.String(),
		PhoneQuietAfter: a.PhoneQuietAfter.String(), DiskFreeMinGB: a.DiskFreeMinGB}
}

// BackupSchedule returns the backup time and keep setting; Destination is always empty (it stays in config.yaml).
func (s *Store) BackupSchedule(ctx context.Context) (config.BackupConfig, bool, error) {
	var b backupSchedule
	ok, err := s.settingJSON(ctx, KeyBackupSchedule, &b)
	return config.BackupConfig{Time: b.Time, Keep: b.Keep}, ok, err
}

// SetBackupSchedule writes the time and keep of b; b.Destination is never written.
func (s *Store) SetBackupSchedule(ctx context.Context, b config.BackupConfig) error {
	return s.setSettingJSON(ctx, KeyBackupSchedule, backupSchedule{Time: b.Time, Keep: b.Keep})
}
