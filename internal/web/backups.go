package web

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"whatsapp-archiver/internal/view"
)

// The backups screen is read-only except for the test alert. Restoring is shown as commands to run, never as a button.

// testAlertTitle is what the phone shows for the test alert.
const testAlertTitle = "Test alert from WhatsApp Archiver"

// testAlertBound caps one test send (the notifier retries a failing server for a while).
const testAlertBound = 30 * time.Second

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
		ctx, cancel := context.WithTimeout(r.Context(), testAlertBound)
		defer cancel()
		if err := s.d.Notifier.Notify(ctx, testAlertTitle, "This is a test. Nothing is wrong."); err != nil {
			res.Text = "The test alert failed: " + err.Error()
		} else {
			res.Sent, res.Text = true, "Sent. It should appear on your phone in a moment."
		}
	}
	s.renderBackups(w, r, res)
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
