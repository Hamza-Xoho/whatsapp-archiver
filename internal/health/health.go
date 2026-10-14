package health

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
	"whatsapp-archiver/internal/backup"
	"whatsapp-archiver/internal/config"
)

type Clock interface{ Now() time.Time }
type Snapshot struct {
	State           string    `json:"state"`
	Detail          string    `json:"detail"`
	LastEvent       time.Time `json:"last_event"`
	PhoneLastActive time.Time `json:"phone_last_active"`
	Panics          int       `json:"panics"`
	UpdatedAt       time.Time `json:"updated_at"`
}
type Monitor struct {
	mu       sync.Mutex
	cfg      *config.Config
	n        Notifier
	clock    Clock
	diskFree func(string) (uint64, error)
	version  func() string
	snapshot Snapshot
	alerts   map[string]time.Time
}

func NewMonitor(cfg *config.Config, n Notifier, c Clock, disk func(string) (uint64, error), version func() string) *Monitor {
	return &Monitor{cfg: cfg, n: n, clock: c, diskFree: disk, version: version, alerts: map[string]time.Time{}}
}
func (m *Monitor) Seen(t time.Time, fromMe bool) {
	m.mu.Lock()
	m.snapshot.LastEvent = t
	if fromMe {
		m.snapshot.PhoneLastActive = t
	}
	m.writeLocked()
	m.mu.Unlock()
}
func (m *Monitor) SetState(state, detail string) {
	m.mu.Lock()
	m.snapshot.State = state
	m.snapshot.Detail = detail
	m.writeLocked()
	m.mu.Unlock()
	switch state {
	case "logged_out", "temp_banned", "stream_replaced":
		m.alert(state, "WhatsApp Archiver", state+": "+detail)
	case "client_outdated":
		m.alert(state, "WhatsApp client outdated", "Version "+m.version()+" was rejected; update the archiver")
	}
}
func (m *Monitor) WriteFailed(err error) {
	m.alert("write_failed", "Archive write failed", err.Error())
}
func (m *Monitor) Panic(where string) {
	m.mu.Lock()
	m.snapshot.Panics++
	m.writeLocked()
	m.mu.Unlock()
	m.alert("panic:"+where, "Recovered panic", where)
}
func (m *Monitor) BackupResult(_ backup.Report, err error) {
	if err != nil {
		m.alert("backup", "Backup failed", err.Error())
	}
}
func (m *Monitor) Status() Snapshot { m.mu.Lock(); defer m.mu.Unlock(); return m.snapshot }
func (m *Monitor) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.check()
		}
	}
}
func (m *Monitor) check() {
	m.mu.Lock()
	now := m.clock.Now()
	s := m.snapshot
	m.snapshot.UpdatedAt = now
	m.writeLocked()
	m.mu.Unlock()
	if m.cfg.Alerts.NoEventsAfter > 0 && !s.LastEvent.IsZero() && now.Sub(s.LastEvent) > m.cfg.Alerts.NoEventsAfter {
		m.alert("no_events", "No WhatsApp events", "Capture may be stalled")
	}
	if m.diskFree != nil && m.cfg.Alerts.DiskFreeMinGB > 0 {
		free, err := m.diskFree(m.cfg.DataDir)
		if err == nil && free < uint64(m.cfg.Alerts.DiskFreeMinGB)<<30 {
			m.alert("disk", "Disk space low", fmt.Sprintf("%d bytes free", free))
		}
	}
}
func (m *Monitor) alert(key, title, body string) {
	m.mu.Lock()
	now := m.clock.Now()
	last := m.alerts[key]
	if !last.IsZero() && now.Sub(last) < 6*time.Hour {
		m.mu.Unlock()
		return
	}
	m.alerts[key] = now
	m.mu.Unlock()
	if m.n != nil {
		_ = m.n.Notify(context.Background(), title, body)
	}
}
func (m *Monitor) writeLocked() {
	m.snapshot.UpdatedAt = m.clock.Now()
	data, err := json.MarshalIndent(m.snapshot, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(m.cfg.DataDir, 0o700)
	tmp := filepath.Join(m.cfg.DataDir, "health.json.tmp")
	if os.WriteFile(tmp, data, 0o600) == nil {
		_ = os.Rename(tmp, filepath.Join(m.cfg.DataDir, "health.json"))
	}
}
