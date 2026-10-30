package web

import (
	"fmt"
	"net/http"

	"whatsapp-archiver/internal/view"
)

type loggingData struct {
	page
	Overview              dashboardData
	Storage               storageData
	Backups               backupsData
	StorageErr, BackupErr bool
}

// logging folds the former dashboard, storage, backups and settings screens into one operational area.
func (s *Server) logging(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	overview, err := s.dashboardData(ctx)
	if err != nil {
		http.Error(w, "the archive could not be read", http.StatusInternalServerError)
		return
	}
	loc, err := view.Location(s.config())
	if err != nil {
		http.Error(w, "the page could not be rendered", http.StatusInternalServerError)
		return
	}
	now := s.d.Now()
	facts, storageErr := s.storageCache.get(now, func() (storageFacts, error) { return s.loggingStorage(ctx, now, loc) })
	storage := facts.storageData
	if storageErr == nil {
		if min := s.diskFreeMinGB(ctx); min > 0 {
			storage.Warning = projectionWarning(facts.free, facts.weekly, min)
		}
	}
	backups, backupErr := s.loggingBackups(r)
	data := loggingData{
		page: page{Title: "Logging", Current: "Logging", Live: true}, Overview: overview,
		Storage: storage, Backups: backups, StorageErr: storageErr != nil, BackupErr: backupErr != nil,
	}
	s.render(w, s.pages["logging"], "layout", data)
}

func (s *Server) backupsData(r *http.Request) (backupsData, error) {
	loc, err := view.Location(s.config())
	if err != nil {
		return backupsData{}, err
	}
	cfg := s.config()
	b := cfg.Backup
	if s.d.Store != nil {
		sc, ok, err := s.d.Store.BackupSchedule(r.Context())
		if err != nil {
			return backupsData{}, err
		}
		if ok {
			b.Time, b.Keep = sc.Time, sc.Keep
		}
	}
	d := backupsData{
		page:           page{Title: "Backups & alerts", Current: "Logging"},
		Schedule:       fmt.Sprintf("every day at %s, keeping %d snapshots", b.Time, b.Keep),
		Destination:    b.Destination,
		NtfyConfigured: s.d.Notifier != nil,
		DataDir:        cfg.DataDir,
		Dest:           b.Destination,
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
	return d, nil
}
