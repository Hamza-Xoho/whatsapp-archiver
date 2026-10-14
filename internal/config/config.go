package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
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
}

type HistoryRules struct {
	DaysLimit   int `yaml:"days_limit"`
	SizeMBLimit int `yaml:"size_mb_limit"`
}
type MediaRules struct {
	Download               []string `yaml:"download"`
	VideoMaxMB             int      `yaml:"video_max_mb"`
	Skip                   []string `yaml:"skip"`
	Workers                int      `yaml:"workers"`
	RetryRequestsPerMinute int      `yaml:"retry_requests_per_minute"`
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

func defaults() Config {
	return Config{
		DataDir: "./data", TimeZone: "Local",
		History: HistoryRules{DaysLimit: 3650, SizeMBLimit: 4096},
		Media:   MediaRules{Download: []string{"document", "image", "audio", "video"}, VideoMaxMB: 100, Skip: []string{"sticker", "gif"}, Workers: 2, RetryRequestsPerMinute: 10},
		Backup:  BackupConfig{Time: "03:30", Keep: 7},
		Alerts:  AlertConfig{NoEventsAfter: 6 * time.Hour, DisconnectedAfter: 15 * time.Minute, PhoneQuietAfter: 240 * time.Hour, DiskFreeMinGB: 5},
		WA:      WAConfig{CheckLatestVersion: true},
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
	return &cfg, nil
}
