package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	DataDir  string       `yaml:"data_dir"`
	TimeZone string       `yaml:"time_zone"`
	Exclude  []string     `yaml:"exclude"`
	History  HistoryRules `yaml:"history"`
	Media    MediaRules   `yaml:"media"`
	Backup   BackupConfig `yaml:"backup"`
	Alerts   AlertConfig  `yaml:"alerts"`
	WA       WAConfig     `yaml:"wa"`
	Web      Web          `yaml:"web"`
}

// ImportMarker is the settings key that records that config.yaml's runtime settings were imported into the archive.
// The import runs once: after it, the UI owns exclusions, media rules, alert thresholds and the backup schedule.
const ImportMarker = "import.config.v1"

// Web is the control UI. It has no login in phase 2, so it listens on loopback only.
type Web struct {
	Listen string `yaml:"listen"`
}

type HistoryRules struct {
	DaysLimit   int `yaml:"days_limit"`
	SizeMBLimit int `yaml:"size_mb_limit"`
}
type MediaRules struct {
	Download               []string `yaml:"download" json:"download"`
	VideoMaxMB             int      `yaml:"video_max_mb" json:"video_max_mb"`
	Skip                   []string `yaml:"skip" json:"skip"`
	Workers                int      `yaml:"workers" json:"workers"`
	RetryRequestsPerMinute int      `yaml:"retry_requests_per_minute" json:"retry_requests_per_minute"`
}
type BackupConfig struct {
	Destination string `yaml:"destination"`
	Time        string `yaml:"time"`
	Keep        int    `yaml:"keep"`
}
type AlertConfig struct {
	Ntfy              *NtfyConfig   `yaml:"ntfy"`
	NoEventsAfter     time.Duration `yaml:"no_events_after"`
	DisconnectedAfter time.Duration `yaml:"disconnected_after"`
	PhoneQuietAfter   time.Duration `yaml:"phone_quiet_after"`
	DiskFreeMinGB     int           `yaml:"disk_free_min_gb"`
}
type NtfyConfig struct {
	URL   string `yaml:"url"`
	Token string `yaml:"token"`
}
type WAConfig struct {
	CheckLatestVersion bool   `yaml:"check_latest_version"`
	VersionOverride    string `yaml:"version_override"`
}

var versionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// chatAddress is the form of a chat's JID as WhatsApp sends it: a phone number or LID for a direct chat, a group ID.
// Anything else, such as a "+" number or a missing server, would silently exclude nothing.
var chatAddress = regexp.MustCompile(`^(\d+@s\.whatsapp\.net|\d+@lid|\d+(-\d+)?@g\.us)$`)

func defaults() Config {
	return Config{
		DataDir: "./data", TimeZone: "Local",
		History: HistoryRules{DaysLimit: 3650, SizeMBLimit: 4096},
		Media:   MediaRules{Download: []string{"document", "image", "audio", "video"}, VideoMaxMB: 100, Skip: []string{"sticker", "gif"}, Workers: 2, RetryRequestsPerMinute: 10},
		Backup:  BackupConfig{Time: "03:30", Keep: 7},
		Alerts:  AlertConfig{NoEventsAfter: 6 * time.Hour, DisconnectedAfter: 15 * time.Minute, PhoneQuietAfter: 240 * time.Hour, DiskFreeMinGB: 5},
		WA:      WAConfig{CheckLatestVersion: true},
		Web:     Web{Listen: "127.0.0.1:8080"},
	}
}

func Load(path string) (*Config, error) {
	cfg := defaults()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &cfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open config: %w", err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	if cfg.WA.VersionOverride != "" && !versionPattern.MatchString(cfg.WA.VersionOverride) {
		return nil, fmt.Errorf("version_override must have three numeric components")
	}
	for _, entry := range cfg.Exclude {
		if !chatAddress.MatchString(entry) {
			return nil, fmt.Errorf("exclude entry %q is not a chat address; write <digits>@s.whatsapp.net (phone number, digits only), <digits>@lid or <id>@g.us (group, from `archiver chats`)", entry)
		}
	}
	if err := checkLoopback(cfg.Web.Listen); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// checkLoopback accepts a listen address only on 127.0.0.1, ::1 or localhost, with a port.
func checkLoopback(listen string) error {
	host, port, err := net.SplitHostPort(listen)
	if err != nil || port == "" {
		return fmt.Errorf("web.listen %q must be host:port on 127.0.0.1, [::1] or localhost", listen)
	}
	switch host {
	case "127.0.0.1", "::1", "localhost":
		return nil
	}
	return fmt.Errorf("web.listen %q is not loopback: the UI has no login, so it listens on 127.0.0.1, [::1] or localhost only; reach it over ssh -L", listen)
}
