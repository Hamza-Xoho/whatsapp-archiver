package web

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"whatsapp-archiver/internal/config"
)

// The settings screen edits the three settings kept in the archive (media.rules, alerts.thresholds,
// backup.schedule). Capture, the monitor and the backup scheduler read them at each use, so a save applies on their
// next use without a restart. The values that stay in config.yaml are shown read-only. Page views only read; the
// ntfy URL and token are never rendered, only whether they are set.

type mediaOption struct {
	Name           string
	Download, Skip bool
}

type mediaSection struct {
	Options                        []mediaOption
	VideoMaxMB, Workers, RetryPerM string
	Err                            string
}

type alertsSection struct {
	NoEventsAfter, DisconnectedAfter, PhoneQuietAfter, DiskFreeMinGB string
	Err                                                              string
}

type backupSection struct {
	Time, Keep string
	Err        string
}

type settingsData struct {
	page
	Media  mediaSection
	Alerts alertsSection
	Backup backupSection
	Saved  string // the section just saved: media, alerts or backup
	// InUse says what applies now, whatever a rejected form shows: one line per section.
	MediaInUse, AlertsInUse, BackupInUse string
	// Read-only, from config.yaml.
	DataDir, TimeZone, BackupDestination, Listen, VersionOverride string
	CheckLatestVersion, NtfyConfigured                            bool
}

// settings is GET /settings.
func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	d, err := s.settingsData(r.Context())
	if err != nil {
		http.Error(w, "the settings could not be read", http.StatusInternalServerError)
		return
	}
	switch saved := r.URL.Query().Get("saved"); saved {
	case "media", "alerts", "backup":
		d.Saved = saved
	}
	s.render(w, s.pages["settings"], "layout", d)
}

// settingsData is the page with every section as it applies now: the archive's setting, else config.yaml's value,
// the same fallback the live readers use.
func (s *Server) settingsData(ctx context.Context) (settingsData, error) {
	cfg := s.config()
	m, a, b := cfg.Media, cfg.Alerts, cfg.Backup
	if s.d.Store != nil {
		if r, ok, err := s.d.Store.MediaRules(ctx); err != nil {
			return settingsData{}, err
		} else if ok {
			m = r
		}
		if t, ok, err := s.d.Store.AlertThresholds(ctx); err != nil {
			return settingsData{}, err
		} else if ok {
			a = t
		}
		if sc, ok, err := s.d.Store.BackupSchedule(ctx); err != nil {
			return settingsData{}, err
		} else if ok {
			b.Time, b.Keep = sc.Time, sc.Keep
		}
	}
	cap := strconv.Itoa(m.VideoMaxMB) + " MB"
	if m.VideoMaxMB == 0 {
		cap = "no cap"
	}
	return settingsData{
		page:        page{Title: "Settings"},
		MediaInUse:  fmt.Sprintf("In use now: largest video %s, %d download workers, %d phone retry requests a minute.", cap, m.Workers, m.RetryRequestsPerMinute),
		AlertsInUse: fmt.Sprintf("In use now: no events after %s, disconnected after %s, phone quiet after %s, disk free minimum %d GB.", formatDuration(a.NoEventsAfter), formatDuration(a.DisconnectedAfter), formatDuration(a.PhoneQuietAfter), a.DiskFreeMinGB),
		BackupInUse: fmt.Sprintf("In use now: every day at %s, keeping %d snapshots.", b.Time, b.Keep),
		Media:       mediaSectionOf(m), Alerts: alertsSectionOf(a), Backup: backupSection{Time: b.Time, Keep: strconv.Itoa(b.Keep)},
		DataDir: cfg.DataDir, TimeZone: cfg.TimeZone, BackupDestination: cfg.Backup.Destination, Listen: cfg.Web.Listen,
		CheckLatestVersion: cfg.WA.CheckLatestVersion, VersionOverride: cfg.WA.VersionOverride,
		NtfyConfigured: cfg.Alerts.Ntfy != nil && cfg.Alerts.Ntfy.URL != "",
	}, nil
}

func mediaSectionOf(m config.MediaRules) mediaSection {
	f := mediaSection{VideoMaxMB: strconv.Itoa(m.VideoMaxMB), Workers: strconv.Itoa(m.Workers), RetryPerM: strconv.Itoa(m.RetryRequestsPerMinute)}
	for _, t := range config.MediaTypes {
		f.Options = append(f.Options, mediaOption{Name: t, Download: slices.Contains(m.Download, t), Skip: slices.Contains(m.Skip, t)})
	}
	return f
}

func alertsSectionOf(a config.AlertConfig) alertsSection {
	return alertsSection{NoEventsAfter: formatDuration(a.NoEventsAfter), DisconnectedAfter: formatDuration(a.DisconnectedAfter),
		PhoneQuietAfter: formatDuration(a.PhoneQuietAfter), DiskFreeMinGB: strconv.Itoa(a.DiskFreeMinGB)}
}

// formatDuration writes 6h0m0s as 6h and 15m0s as 15m; both forms parse back.
func formatDuration(d time.Duration) string {
	switch {
	case d > 0 && d%time.Hour == 0:
		return fmt.Sprintf("%dh", d/time.Hour)
	case d > 0 && d%time.Minute == 0:
		return fmt.Sprintf("%dm", d/time.Minute)
	}
	return d.String()
}

// saveSetting is POST /settings/{media|alerts|backup}. A valid form is written and answered with a redirect to the
// page; an invalid one re-renders the page with the error and the typed values, and writes nothing.
func (s *Server) saveSetting(w http.ResponseWriter, r *http.Request) {
	section := r.PathValue("section")
	if section != "media" && section != "alerts" && section != "backup" {
		http.NotFound(w, r)
		return
	}
	if s.d.Store == nil {
		http.Error(w, "the archive is not available", http.StatusServiceUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "the form could not be read", http.StatusBadRequest)
		return
	}
	d, err := s.settingsData(r.Context())
	if err != nil {
		http.Error(w, "the settings could not be read", http.StatusInternalServerError)
		return
	}
	var msg string
	switch section {
	case "media":
		d.Media, msg, err = s.saveMedia(r)
	case "alerts":
		d.Alerts, msg, err = s.saveAlerts(r)
	case "backup":
		d.Backup, msg, err = s.saveBackup(r)
	}
	if err != nil {
		http.Error(w, "the setting could not be saved", http.StatusInternalServerError)
		return
	}
	if msg == "" {
		http.Redirect(w, r, "/settings?saved="+section+"#"+section, http.StatusSeeOther)
		return
	}
	switch section {
	case "media":
		d.Media.Err = msg
	case "alerts":
		d.Alerts.Err = msg
	case "backup":
		d.Backup.Err = msg
	}
	w.WriteHeader(http.StatusUnprocessableEntity)
	s.render(w, s.pages["settings"], "layout", d)
}

// atoi parses a whole number field; the message names the field.
func atoi(label, v string) (int, string) {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return 0, label + " must be a whole number"
	}
	return n, ""
}

// saveMedia returns the form as typed and, when it is invalid, the message; otherwise it writes the rules.
func (s *Server) saveMedia(r *http.Request) (mediaSection, string, error) {
	f := mediaSection{VideoMaxMB: r.PostForm.Get("video_max_mb"), Workers: r.PostForm.Get("workers"), RetryPerM: r.PostForm.Get("retry_requests_per_minute")}
	rules := config.MediaRules{Download: []string{}, Skip: []string{}}
	for _, t := range config.MediaTypes {
		o := mediaOption{Name: t, Download: slices.Contains(r.PostForm["download"], t), Skip: slices.Contains(r.PostForm["skip"], t)}
		f.Options = append(f.Options, o)
		if o.Download {
			rules.Download = append(rules.Download, t)
		}
		if o.Skip {
			rules.Skip = append(rules.Skip, t)
		}
	}
	for _, t := range append(slices.Clone(r.PostForm["download"]), r.PostForm["skip"]...) {
		if !slices.Contains(config.MediaTypes, t) {
			return f, fmt.Sprintf("Unknown media type %q", t), nil
		}
	}
	var msg string
	if rules.VideoMaxMB, msg = atoi("Largest video (MB)", f.VideoMaxMB); msg != "" {
		return f, msg + ", 0 for no cap", nil
	}
	if rules.Workers, msg = atoi("Download workers", f.Workers); msg != "" {
		return f, msg, nil
	}
	if rules.RetryRequestsPerMinute, msg = atoi("Phone retry requests per minute", f.RetryPerM); msg != "" {
		return f, msg, nil
	}
	if err := config.ValidateMediaRules(rules); err != nil {
		return f, upperFirst(err.Error()), nil
	}
	return f, "", s.d.Store.SetMediaRules(r.Context(), rules)
}

func (s *Server) saveAlerts(r *http.Request) (alertsSection, string, error) {
	f := alertsSection{NoEventsAfter: r.PostForm.Get("no_events_after"), DisconnectedAfter: r.PostForm.Get("disconnected_after"),
		PhoneQuietAfter: r.PostForm.Get("phone_quiet_after"), DiskFreeMinGB: r.PostForm.Get("disk_free_min_gb")}
	var a config.AlertConfig
	for _, d := range []struct {
		label, in string
		out       *time.Duration
	}{{"No events after", f.NoEventsAfter, &a.NoEventsAfter}, {"Disconnected after", f.DisconnectedAfter, &a.DisconnectedAfter}, {"Phone quiet after", f.PhoneQuietAfter, &a.PhoneQuietAfter}} {
		v, err := time.ParseDuration(strings.TrimSpace(d.in))
		if err != nil {
			return f, d.label + " must be a duration such as 6h or 15m", nil
		}
		*d.out = v
	}
	var msg string
	if a.DiskFreeMinGB, msg = atoi("Disk free minimum (GB)", f.DiskFreeMinGB); msg != "" {
		return f, msg, nil
	}
	if err := config.ValidateAlertThresholds(a); err != nil {
		return f, upperFirst(err.Error()), nil
	}
	return f, "", s.d.Store.SetAlertThresholds(r.Context(), a)
}

func (s *Server) saveBackup(r *http.Request) (backupSection, string, error) {
	f := backupSection{Time: r.PostForm.Get("time"), Keep: r.PostForm.Get("keep")}
	keep, msg := atoi("Snapshots to keep", f.Keep)
	if msg != "" {
		return f, msg, nil
	}
	if err := config.ValidateBackupSchedule(f.Time, keep); err != nil {
		return f, upperFirst(err.Error()), nil
	}
	return f, "", s.d.Store.SetBackupSchedule(r.Context(), config.BackupConfig{Time: f.Time, Keep: keep})
}

func upperFirst(s string) string {
	for i, r := range s {
		return string(unicode.ToUpper(r)) + s[i+len(string(r)):]
	}
	return s
}
