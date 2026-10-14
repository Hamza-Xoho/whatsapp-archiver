package health

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
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
	mu        sync.Mutex
	cfg       *config.Config
	n         Notifier
	clock     Clock
	diskFree  func(string) (uint64, error)
	version   func() string
	snapshot  Snapshot
	gate      *alertGate
	started   time.Time // baseline for rules whose event has not been seen yet
	downSince time.Time // when the connection was last lost; zero while connected
	queue     chan alert
	log       *slog.Logger
}
type alert struct{ title, body string }

// stateTitles are the alerts that clear when the connection is back.
var stateTitles = map[string]string{
	"disconnected":    "WhatsApp disconnected",
	"logged_out":      "WhatsApp logged out",
	"temp_banned":     "WhatsApp temporarily banned",
	"stream_replaced": "WhatsApp session replaced",
	"client_outdated": "WhatsApp client outdated",
}

func NewMonitor(cfg *config.Config, n Notifier, c Clock, disk func(string) (uint64, error), version func() string) *Monitor {
	now := c.Now()
	m := &Monitor{cfg: cfg, n: n, clock: c, diskFree: disk, version: version, started: now, downSince: now, queue: make(chan alert, 16), log: slog.Default()}
	m.gate = &alertGate{now: c.Now, send: m.notify, fired: map[string]time.Time{}}
	return m
}

// WithLogger sets where dropped alerts are reported (default slog.Default()).
func (m *Monitor) WithLogger(l *slog.Logger) *Monitor { m.log = l; return m }
func (m *Monitor) Seen(t time.Time, fromMe bool) {
	m.mu.Lock()
	m.snapshot.LastEvent = t
	if fromMe {
		m.snapshot.PhoneLastActive = t
	}
	m.writeLocked()
	m.mu.Unlock()
	m.gate.clear("no_events", "No WhatsApp events")
	if fromMe {
		m.gate.clear("phone_quiet", "Phone inactive")
	}
}
func (m *Monitor) SetState(state, detail string) {
	m.mu.Lock()
	m.snapshot.State = state
	m.snapshot.Detail = detail
	switch {
	case state == "connected":
		m.downSince = time.Time{}
	case state == "disconnected" && m.downSince.IsZero():
		m.downSince = m.clock.Now()
	}
	m.writeLocked()
	m.mu.Unlock()
	switch state {
	case "connected":
		for key, title := range stateTitles {
			m.gate.clear(key, title)
		}
	case "logged_out", "stream_replaced":
		m.gate.fire(state, stateTitles[state], state+": "+detail)
	case "temp_banned":
		m.gate.fire(state, stateTitles[state], m.banBody(detail))
	case "client_outdated":
		m.gate.fire(state, stateTitles[state], "Version "+m.version()+" was rejected; update the archiver")
	}
}

// banBody reads detail as the ban's duration (ingest passes TemporaryBan.Expire); anything else is quoted as is.
func (m *Monitor) banBody(detail string) string {
	d, err := time.ParseDuration(detail)
	if err != nil {
		return "Temporarily banned: " + detail + "; capture is paused"
	}
	until := m.clock.Now().Add(d).UTC().Format(time.RFC3339)
	return "Temporarily banned for " + d.String() + " (until " + until + "); capture is paused"
}
func (m *Monitor) WriteFailed(err error) {
	m.gate.fire("write_failed", "Archive write failed", err.Error())
}
func (m *Monitor) Panic(where string) {
	m.mu.Lock()
	m.snapshot.Panics++
	m.writeLocked()
	m.mu.Unlock()
	m.gate.fire("panic:"+where, "Recovered panic", where)
}
func (m *Monitor) BackupResult(_ backup.Report, err error) {
	if err != nil {
		m.gate.fire("backup", "Backup failed", err.Error())
		return
	}
	m.gate.clear("backup", "Backup failed")
}
func (m *Monitor) Status() Snapshot { m.mu.Lock(); defer m.mu.Unlock(); return m.snapshot }

// Run checks the rules every minute and owns the goroutine that sends alerts; it returns after that goroutine has.
func (m *Monitor) Run(ctx context.Context) {
	sent := make(chan struct{})
	go func() { defer close(sent); m.sendAlerts(ctx) }()
	defer func() { <-sent }()
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
	down := m.downSince
	m.snapshot.UpdatedAt = now
	m.writeLocked()
	m.mu.Unlock()
	a := m.cfg.Alerts
	if d := now.Sub(down); a.DisconnectedAfter > 0 && s.State == "disconnected" && d > a.DisconnectedAfter {
		m.gate.fire("disconnected", stateTitles["disconnected"], fmt.Sprintf("Disconnected for %s %s", d.Round(time.Minute), s.Detail))
	}
	if d := now.Sub(orStart(s.LastEvent, m.started)); a.NoEventsAfter > 0 && s.State == "connected" {
		m.condition(d > a.NoEventsAfter, "no_events", "No WhatsApp events", fmt.Sprintf("No events for %s; capture may be stalled", d.Round(time.Minute)))
	}
	if d := now.Sub(orStart(s.PhoneLastActive, m.started)); a.PhoneQuietAfter > 0 {
		m.condition(d > a.PhoneQuietAfter, "phone_quiet", "Phone inactive",
			fmt.Sprintf("No activity from your phone for %s: open WhatsApp on your phone to avoid the 14-day unlink", d.Round(time.Hour)))
	}
	if m.diskFree != nil && a.DiskFreeMinGB > 0 {
		free, err := m.diskFree(m.cfg.DataDir)
		if err == nil {
			m.condition(free < uint64(a.DiskFreeMinGB)<<30, "disk", "Disk space low", fmt.Sprintf("%d bytes free", free))
		}
	}
}

// condition fires key while holds is true and clears it once it is not.
func (m *Monitor) condition(holds bool, key, title, body string) {
	if holds {
		m.gate.fire(key, title, body)
	} else {
		m.gate.clear(key, title)
	}
}
func orStart(t, start time.Time) time.Time {
	if t.IsZero() {
		return start
	}
	return t
}

// notify queues an alert for Run's sender without blocking and reports whether it was queued;
// when the queue is full the alert is dropped.
func (m *Monitor) notify(title, body string) bool {
	if m.n == nil {
		return true
	}
	select {
	case m.queue <- alert{title, body}:
		return true
	default:
		m.log.Error("alert dropped: queue full", "title", title, "body", body)
		return false
	}
}

// sendAlerts hands queued alerts to the notifier with Run's context. Once ctx ends it passes on what is
// still queued with the ended context (a real send then fails at once) and returns.
func (m *Monitor) sendAlerts(ctx context.Context) {
	for {
		select {
		case a := <-m.queue:
			m.send(ctx, a)
		case <-ctx.Done():
			for {
				select {
				case a := <-m.queue:
					m.send(ctx, a)
				default:
					return
				}
			}
		}
	}
}
func (m *Monitor) send(ctx context.Context, a alert) {
	if err := m.n.Notify(ctx, a.title, a.body); err != nil {
		m.log.Error("alert not sent", "title", a.title, "err", err)
	}
}

const alertEvery = 6 * time.Hour

// alertGate sends each alert at most once per alertEvery while it is active and one "recovered" message when it clears.
type alertGate struct {
	mu    sync.Mutex
	now   func() time.Time
	send  func(title, body string) bool // false when the alert was dropped
	fired map[string]time.Time
}

func (g *alertGate) fire(key, title, body string) {
	g.mu.Lock()
	now := g.now()
	last, ok := g.fired[key]
	if ok && now.Sub(last) < alertEvery {
		g.mu.Unlock()
		return
	}
	if g.send(title, body) { // only a queued alert counts as fired, so a dropped one fires again
		g.fired[key] = now
	}
	g.mu.Unlock()
}
func (g *alertGate) clear(key, title string) {
	g.mu.Lock()
	_, ok := g.fired[key]
	delete(g.fired, key)
	g.mu.Unlock()
	if ok {
		g.send(title+": recovered", "Recovered: "+title)
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
