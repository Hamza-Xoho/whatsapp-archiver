package web

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"whatsapp-archiver/internal/logsafe"
	"whatsapp-archiver/internal/view"
)

// The backups screen is read-only except for the test alert. Restoring is shown as commands to run, never as a button.

// testAlertTitle is what the phone shows for the test alert.
const testAlertTitle = "Test alert from WhatsApp Archiver"

// testAlertBound caps one test send (the notifier retries a failing server for a while).
const testAlertBound = 30 * time.Second

// testAlertEvery is the least time between two test alerts.
const testAlertEvery = 10 * time.Second

// ntfyStatus finds the HTTP status in the notifier's "ntfy status 403 Forbidden" error. It is the only part of a send
// error the page shows: any other error text can hold the ntfy URL, whose topic is the secret on ntfy.sh.
var ntfyStatus = regexp.MustCompile(`ntfy status (\d{3}[ A-Za-z]*)`)

type alertResult struct {
	Sent bool
	Text string
}

type backupsData struct {
	page
	Schedule, Destination           string
	HasResult, Succeeded            bool
	ResultAt, ResultPath, ResultErr string
	NtfyConfigured                  bool
	DataDir, Dest                   string
	Alert                           *alertResult
}

// backups is GET /backups.
func (s *Server) backups(w http.ResponseWriter, r *http.Request) {
	s.renderBackups(w, r, nil)
}

// testAlert is POST /backups/test-alert: one notification through the same notifier the monitor uses.
func (s *Server) testAlert(w http.ResponseWriter, r *http.Request) {
	res := &alertResult{Text: "Phone alerts (ntfy) are not configured, so nothing was sent. Set alerts.ntfy in config.yaml."}
	if s.d.Notifier != nil {
		res = s.sendTestAlert(r.Context())
	}
	s.renderBackups(w, r, res)
}

// sendTestAlert sends one test notification, at most one every testAlertEvery (an attempt counts, sent or not).
func (s *Server) sendTestAlert(parent context.Context) *alertResult {
	s.testAlertMu.Lock()
	now := s.d.Now()
	if !s.testAlertAt.IsZero() && now.Sub(s.testAlertAt) < testAlertEvery {
		s.testAlertMu.Unlock()
		return &alertResult{Text: "Wait a few seconds before sending another test alert."}
	}
	s.testAlertAt = now
	s.testAlertMu.Unlock()
	ctx, cancel := context.WithTimeout(parent, testAlertBound)
	defer cancel()
	err := s.d.Notifier.Notify(ctx, testAlertTitle, "This is a test. Nothing is wrong.")
	if err == nil {
		return &alertResult{Sent: true, Text: "Sent. It should appear on your phone in a moment."}
	}
	slog.Warn("test alert not sent", "err", logsafe.RedactNtfy(logsafe.RedactURLs(err.Error()), s.ntfyURL()))
	if m := ntfyStatus.FindStringSubmatch(err.Error()); m != nil {
		return &alertResult{Text: "The test alert failed: ntfy answered " + m[1] + "."}
	}
	return &alertResult{Text: "Could not send the test alert; see the journal for details."}
}

func (s *Server) renderBackups(w http.ResponseWriter, r *http.Request, alert *alertResult) {
	loc, err := view.Location(s.config())
	if err != nil {
		http.Error(w, "the page could not be rendered", http.StatusInternalServerError)
		return
	}
	cfg := s.config()
	b := cfg.Backup
	if s.d.Store != nil {
		sc, ok, err := s.d.Store.BackupSchedule(r.Context())
		if err != nil {
			http.Error(w, "the settings could not be read", http.StatusInternalServerError)
			return
		}
		if ok {
			b.Time, b.Keep = sc.Time, sc.Keep
		}
	}
	d := backupsData{
		page:           page{Title: "Backups & alerts"},
		Schedule:       fmt.Sprintf("every day at %s, keeping %d snapshots", b.Time, b.Keep),
		Destination:    b.Destination,
		NtfyConfigured: s.d.Notifier != nil,
		DataDir:        cfg.DataDir,
		Dest:           b.Destination,
		Alert:          alert,
	}
	if d.Dest == "" {
		d.Dest = "<backup folder>"
	}
	if s.d.Health != nil {
		if snap := s.d.Health(); !snap.LastBackup.IsZero() {
			d.HasResult, d.ResultAt = true, view.FormatTime(loc, snap.LastBackup)
			d.Succeeded, d.ResultPath, d.ResultErr = snap.LastBackupErr == "", snap.LastBackupPath, snap.LastBackupErr
		}
	}
	s.render(w, s.pages["backups"], "layout", d)
}

// ntfyURL is the configured ntfy URL, whose path is the secret topic; "" when ntfy is not configured.
func (s *Server) ntfyURL() string {
	if n := s.config().Alerts.Ntfy; n != nil {
		return n.URL
	}
	return ""
}
