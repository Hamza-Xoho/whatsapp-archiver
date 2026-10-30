package web

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/view"
)

// The storage screen is read-only: sizes, weekly growth and the days until the disk is full. Walking <data>/media
// (about 3 GB in many files) is slow, so the whole computation is kept in memory for storageTTL.
const (
	storageTTL = 5 * time.Minute
	topChats   = 10
	warnDays   = 30
)

type storageRow struct{ Name, JID, Size string }

type storageData struct {
	page
	DB, Media, Backup, Free, Growth string
	BackupSet                       bool
	Projection, Warning             string
	ByType, ByChat                  []storageRow
	AsOf                            string
}

// storageFacts are the numbers behind the page. The warning is worked out from them at each view, because the alert
// level it compares with is a live setting.
type storageFacts struct {
	storageData
	free   uint64
	weekly int64
}

type storageCache struct {
	mu    sync.Mutex
	at    time.Time
	facts storageFacts
	ok    bool
}

// get returns the cached facts while they are younger than storageTTL, else computes them. Holding the lock while
// computing makes concurrent views share one walk. A failed computation is not cached.
func (c *storageCache) get(now time.Time, compute func() (storageFacts, error)) (storageFacts, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ok && now.Sub(c.at) < storageTTL {
		return c.facts, nil
	}
	f, err := compute()
	if err != nil {
		return storageFacts{}, err
	}
	c.at, c.facts, c.ok = now, f, true
	return f, nil
}

// storage is GET /storage.
func (s *Server) storage(w http.ResponseWriter, r *http.Request) {
	loc, err := view.Location(s.config())
	if err != nil {
		http.Error(w, "the page could not be rendered", http.StatusInternalServerError)
		return
	}
	now := s.d.Now()
	f, err := s.storageCache.get(now, func() (storageFacts, error) { return s.storageFacts(r.Context(), now, loc) })
	if err != nil {
		http.Error(w, "the storage figures could not be read", http.StatusInternalServerError)
		return
	}
	d := f.storageData
	d.Title = "Storage"
	d.Current = "Logging"
	if min := s.diskFreeMinGB(r.Context()); min > 0 {
		d.Warning = projectionWarning(f.free, f.weekly, min)
	}
	s.render(w, s.pages["storage"], "layout", d)
}

func (s *Server) storageFacts(ctx context.Context, now time.Time, loc *time.Location) (storageFacts, error) {
	cfg := s.config()
	d := storageData{AsOf: view.FormatTime(loc, now)}
	var usage store.Usage
	d.DB = "-"
	if s.d.Store != nil {
		if info, err := statSize(s.d.Store.Path()); err == nil {
			d.DB = view.HumanSize(info)
		}
		var err error
		if usage, err = s.d.Store.Usage(ctx, now); err != nil {
			return storageFacts{}, err
		}
	}
	d.Media = sizeOfDir(cfg.DataDir + "/media")
	d.BackupSet = cfg.Backup.Destination != ""
	if d.BackupSet {
		d.Backup = sizeOfDir(cfg.Backup.Destination)
	}
	free, err := s.d.DiskFree(cfg.DataDir)
	if err != nil {
		d.Free = "unavailable"
	} else {
		d.Free = view.HumanSize(int64(free))
	}
	d.Growth = view.HumanSize(usage.WeeklyGrowth) + " a week"
	if err == nil {
		d.Projection = projectionText(free, usage.WeeklyGrowth)
	} else {
		d.Projection = "unknown"
	}
	for _, typ := range sortedKeys(usage.ByMediaType) {
		d.ByType = append(d.ByType, storageRow{Name: typ, Size: view.HumanSize(usage.ByMediaType[typ])})
	}
	for i, c := range usage.ByChat {
		if i == topChats {
			break
		}
		row := storageRow{Name: c.ChatJID, Size: view.HumanSize(c.Bytes)}
		if name, err := s.d.Store.ChatName(ctx, c.ChatJID); err == nil && name != "" {
			row.Name, row.JID = name, c.ChatJID
		}
		d.ByChat = append(d.ByChat, row)
	}
	return storageFacts{storageData: d, free: free, weekly: usage.WeeklyGrowth}, nil
}

// diskFreeMinGB is the live alert level (the alerts.thresholds setting), falling back to config.yaml.
func (s *Server) diskFreeMinGB(ctx context.Context) int {
	if s.d.Store != nil {
		if a, ok, err := s.d.Store.AlertThresholds(ctx); err == nil && ok {
			return a.DiskFreeMinGB
		}
	}
	return s.config().Alerts.DiskFreeMinGB
}

// projectionText is the days until the disk is full at the current growth: free / (weekly / 7).
func projectionText(free uint64, weekly int64) string {
	if weekly <= 0 {
		return "no growth measured yet"
	}
	days := free * 7 / uint64(weekly)
	switch {
	case days > 365:
		return "more than a year"
	case days == 1:
		return "1 day"
	}
	return fmt.Sprintf("%d days", days)
}

// projectionWarn is true when free space is at or under the alert level, or will reach it in under warnDays days.
func projectionWarn(free uint64, weekly int64, min uint64) bool {
	if free <= min {
		return true
	}
	return weekly > 0 && (free-min)*7/uint64(weekly) < warnDays
}

func projectionWarning(free uint64, weekly int64, minGB int) string {
	min := uint64(minGB) << 30
	switch {
	case !projectionWarn(free, weekly, min):
		return ""
	case free <= min:
		return fmt.Sprintf("Warning: free space is already at or under the %d GB alert level.", minGB)
	}
	return fmt.Sprintf("Warning: free space is projected to reach the %d GB alert level in about %d days.", minGB, (free-min)*7/uint64(weekly))
}
