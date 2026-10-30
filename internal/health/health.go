package health

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"whatsapp-archiver/internal/activity"
	"whatsapp-archiver/internal/backup"
	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/logsafe"
	"whatsapp-archiver/internal/store"
)

type Clock interface{ Now() time.Time }
type ConnectionLedger interface {
	OpenConnectionPeriod(context.Context, store.ConnectionPeriod) (int64, error)
	CloseConnectionPeriod(context.Context, int64) (bool, error)
	NoteConnectionError(context.Context, string) error
	NoteOfflineSync(context.Context, *int64, *int64) error
}
type AlertStore interface {
	EnqueueAlert(context.Context, store.Alert) (int64, error)
	OldestQueuedAlert(context.Context) (store.Alert, bool, error)
	ActiveAlertKeys(context.Context) ([]string, error)
	AlertSent(context.Context, int64, int64) error
	AlertSkipped(context.Context, int64) error
	AlertFailed(context.Context, int64, string, int64) error
}
type Snapshot struct {
	State           string    `json:"state"`
	Detail          string    `json:"detail"`
	LastEvent       time.Time `json:"last_event"`
	PhoneLastActive time.Time `json:"phone_last_active"`
	Panics          int       `json:"panics"`
	UpdatedAt       time.Time `json:"updated_at"`
	Version         string    `json:"version"`        // WhatsApp client version in use
	VersionReason   string    `json:"version_reason"` // why: latest, built-in or override
	// The last nightly snapshot attempt: when it finished, the snapshot file on success, the reason on failure.
	LastBackup     time.Time `json:"last_backup"`
	LastBackupErr  string    `json:"last_backup_err"`
	LastBackupPath string    `json:"last_backup_path"`
}
type Monitor struct {
	mu           sync.Mutex
	transition   sync.Mutex // serializes state changes with their durable ledger writes
	cfg          *config.Config
	limits       func() config.AlertConfig // live thresholds; nil: cfg.Alerts
	n            Notifier
	clock        Clock
	diskFree     func(string) (uint64, error)
	version      func() string
	reason       func() string
	snapshot     Snapshot
	gate         *alertGate
	started      time.Time // baseline for rules whose event has not been seen yet
	downSince    time.Time // when the connection was last lost; zero while connected
	lostAt       time.Time // when this process logged "connection lost"; zero until it has
	queue        chan alert
	log          *slog.Logger
	bus          *activity.Bus // nil: nothing is published
	ledger       ConnectionLedger
	outbox       AlertStore
	wakeAlert    chan struct{}
	heartbeat    *http.Client
	heartbeatURL func(context.Context) string
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
	m := &Monitor{cfg: cfg, n: n, clock: c, diskFree: disk, version: version, started: now, downSince: now, queue: make(chan alert, 16), wakeAlert: make(chan struct{}, 1), log: slog.Default()}
	m.gate = &alertGate{now: c.Now, send: func(_, _, title, body string) bool { return m.notify(title, body) }, fired: map[string]time.Time{}}
	m.learnActiveAlerts()
	return m
}

// learnActiveAlerts marks the alerts that were active when the previous process stopped as fired, so that
// the connection coming back sends their "recovered" message. Without this, a restart clears the gate and the
// recovery is never sent.
func (m *Monitor) learnActiveAlerts() {
	data, err := os.ReadFile(filepath.Join(m.cfg.DataDir, "health.json"))
	if err != nil {
		return
	}
	var prev Snapshot
	if json.Unmarshal(data, &prev) != nil {
		return
	}
	// The last backup outlives the process, so the first write after a start does not blank it.
	m.snapshot.LastBackup, m.snapshot.LastBackupErr, m.snapshot.LastBackupPath = prev.LastBackup, prev.LastBackupErr, prev.LastBackupPath
	if _, ok := stateTitles[prev.State]; ok {
		m.gate.fired[prev.State] = prev.UpdatedAt
	}
}

// WithThresholds makes each check read its thresholds from f (the UI's alerts.thresholds setting). Ntfy always comes
// from the config file. Call it before Run.
func (m *Monitor) WithThresholds(f func() config.AlertConfig) *Monitor {
	m.limits = f
	return m
}

// WithVersionReason sets where the reason for the version in use comes from; health.json records it with the version.
func (m *Monitor) WithVersionReason(f func() string) *Monitor { m.reason = f; return m }

// WithLogger sets where dropped alerts are reported (default slog.Default()).
func (m *Monitor) WithLogger(l *slog.Logger) *Monitor { m.log = l; return m }

// WithActivity makes the monitor publish each connection state change to the live feed. Publish never blocks, so it
// cannot hold up the event handler that reports the state.
func (m *Monitor) WithActivity(b *activity.Bus) *Monitor { m.bus = b; return m }

// WithConnectionLedger records every non-connected period and its reconnect backlog evidence.
func (m *Monitor) WithConnectionLedger(ledger ConnectionLedger) *Monitor { m.ledger = ledger; return m }

// WithAlertStore switches the production monitor from its compatibility
// in-memory queue to the durable alerts table and restores the active gate.
func (m *Monitor) WithAlertStore(outbox AlertStore) *Monitor {
	m.outbox = outbox
	keys, err := outbox.ActiveAlertKeys(context.Background())
	if err != nil {
		m.log.Error("active alerts could not be restored", "err", err)
		keys = nil
	}
	m.gate.mu.Lock()
	m.gate.fired = make(map[string]time.Time, len(keys))
	for _, key := range keys {
		m.gate.fired[key] = m.clock.Now()
	}
	m.gate.durable = true
	m.gate.send = m.enqueueAlert
	m.gate.mu.Unlock()
	return m
}

// WithHeartbeatClient applies the resilient shared HTTP transport to the
// optional off-host heartbeat.
func (m *Monitor) WithHeartbeatClient(client *http.Client) *Monitor {
	clone := *client
	if clone.Timeout == 0 {
		clone.Timeout = 15 * time.Second
	}
	m.heartbeat = &clone
	return m
}

// WithHeartbeatURL makes each heartbeat read the current write-only setting.
// It does not mutate the shared config and must be called before Run.
func (m *Monitor) WithHeartbeatURL(url func() string) *Monitor {
	m.heartbeatURL = func(context.Context) string { return url() }
	return m
}

// WithHeartbeatURLContext makes the provider's archive read share Run's
// cancellation and ownership. It must be called before Run.
func (m *Monitor) WithHeartbeatURLContext(url func(context.Context) string) *Monitor {
	m.heartbeatURL = url
	return m
}
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
	m.transition.Lock()
	defer m.transition.Unlock()
	m.mu.Lock()
	prev := m.snapshot.State
	at := m.clock.Now()
	m.snapshot.State = state
	m.snapshot.Detail = detail
	m.logTransitionLocked(prev, state, detail)
	switch {
	case state == "connected":
		m.downSince = time.Time{}
	case connectionDown(state) && m.downSince.IsZero():
		m.downSince = m.clock.Now()
	}
	m.writeLocked()
	m.mu.Unlock()
	m.recordConnectionState(prev, state, detail, at)
	if prev != state {
		m.bus.Publish(activity.Event{At: m.clock.Now(), Kind: "state", Detail: state})
	}
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

func (m *Monitor) recordConnectionState(prev, state, detail string, at time.Time) {
	if m.ledger == nil {
		return
	}
	ctx := context.Background()
	var err error
	switch {
	case prev == state && connectionDown(state) && detail != "":
		err = m.ledger.NoteConnectionError(ctx, logsafe.RedactURLs(detail))
	case prev == state:
		return
	case state == "connected" && prev != "":
		_, err = m.ledger.CloseConnectionPeriod(ctx, at.Unix())
	case connectionDown(state) || state == "logged_out" || state == "temp_banned" || state == "stream_replaced" || state == "client_outdated":
		_, err = m.ledger.OpenConnectionPeriod(ctx, store.ConnectionPeriod{
			State: state, StartedAt: at.Unix(), CauseClass: connectionCause(state, detail), LastError: logsafe.RedactURLs(detail),
		})
	}
	if err != nil {
		m.log.Error("connection ledger write failed", "state", state, "err", err)
	}
}

func connectionCause(state, detail string) string {
	lower := strings.ToLower(detail)
	for _, class := range []string{"dns", "tcp", "tls", "ws"} {
		if strings.Contains(lower, "class="+class) || strings.Contains(lower, class) {
			return class
		}
	}
	switch state {
	case "degraded":
		return "keepalive"
	case "logged_out":
		return "logout"
	case "temp_banned", "stream_replaced", "client_outdated", "disconnected":
		return "server"
	default:
		return "other"
	}
}

func (m *Monitor) OfflineSyncPreview(messages int) {
	value := int64(messages)
	m.noteOfflineSync(&value, nil)
}

func (m *Monitor) OfflineSyncCompleted(count int) {
	value := int64(count)
	m.noteOfflineSync(nil, &value)
}

func (m *Monitor) noteOfflineSync(preview, completed *int64) {
	if m.ledger != nil {
		if err := m.ledger.NoteOfflineSync(context.Background(), preview, completed); err != nil {
			m.log.Error("offline sync ledger write failed", "err", err)
		}
	}
}

// logTransitionLocked writes the journal line for a connection change: "lost" when a connected link goes to any other
// state, "restored" when it is back after this process logged a loss. The first connect after start logs nothing, and
// neither does a repeat of the same state.
func (m *Monitor) logTransitionLocked(prev, state, detail string) {
	switch {
	case prev == state:
	case prev == "connected":
		m.lostAt = m.clock.Now()
		m.log.Info("connection lost", "state", state, "detail", detail)
	case state == "connected" && !m.lostAt.IsZero():
		m.log.Info("connection restored", "down_for", m.clock.Now().Sub(m.lostAt).String())
		m.lostAt = time.Time{}
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

// ErrRead marks a failed archive read, such as the exclusion filter's alias lookup.
var ErrRead = errors.New("archive read failed")

// failingBody is the whole alert body: the error can name a chat (even the alias of an excluded one) and ntfy may be
// a public server, so the error stays in the journal.
const failingBody = "A write or read is failing; see the journal"

// WriteFailed alerts that a write, or a read wrapped in ErrRead, keeps failing.
func (m *Monitor) WriteFailed(err error) {
	if errors.Is(err, ErrRead) {
		m.gate.fire("read_failed", "Archive read failed", failingBody)
		return
	}
	m.gate.fire("write_failed", "Archive write failed", failingBody)
}
func (m *Monitor) Panic(where string) {
	m.mu.Lock()
	m.snapshot.Panics++
	m.writeLocked()
	m.mu.Unlock()
	m.gate.fire("panic:"+where, "Recovered panic", where)
}
func (m *Monitor) BackupResult(r backup.Report, err error) {
	m.mu.Lock()
	m.snapshot.LastBackup = m.clock.Now()
	m.snapshot.LastBackupErr, m.snapshot.LastBackupPath = "", r.Snapshot
	if err != nil {
		m.snapshot.LastBackupErr, m.snapshot.LastBackupPath = err.Error(), ""
	}
	m.writeLocked()
	m.mu.Unlock()
	if err != nil {
		m.gate.fire("backup", "Backup failed", err.Error())
		return
	}
	m.gate.clear("backup", "Backup failed")
}
func (m *Monitor) Status() Snapshot { m.mu.Lock(); defer m.mu.Unlock(); return m.snapshot }

// Run checks the rules every minute and owns the goroutine that sends alerts; it returns after that goroutine has.
func (m *Monitor) Run(ctx context.Context) {
	if m.outbox != nil {
		m.runAlertOutbox(ctx)
		return
	}
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

func (m *Monitor) enqueueAlert(key, kind, title, body string) bool {
	now := m.clock.Now().Unix()
	_, err := m.outbox.EnqueueAlert(context.Background(), store.Alert{
		Key: key, Kind: kind, Title: title, Body: body, RaisedAt: now, NextAttemptAt: now,
	})
	if err != nil {
		m.log.Error("alert not stored", "title", title, "err", err)
		return false
	}
	select {
	case m.wakeAlert <- struct{}{}:
	default:
	}
	return true
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
	if m.limits != nil {
		a = m.limits()
		a.Ntfy = m.cfg.Alerts.Ntfy
	}
	if d := now.Sub(down); a.DisconnectedAfter > 0 && (connectionDown(s.State) || s.State == "") && d > a.DisconnectedAfter { // "" = never connected yet
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

func connectionDown(state string) bool {
	return state == "disconnected" || state == "degraded" || state == "reconnecting"
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

const alertEvery = 6 * time.Hour

// alertGate sends each alert at most once per alertEvery while it is active and one "recovered" message when it clears.
type alertGate struct {
	mu      sync.Mutex
	now     func() time.Time
	send    func(key, kind, title, body string) bool // false when the alert was not stored/queued
	fired   map[string]time.Time
	durable bool
}

func (g *alertGate) fire(key, title, body string) {
	g.mu.Lock()
	now := g.now()
	last, ok := g.fired[key]
	if ok && (g.durable || now.Sub(last) < alertEvery) {
		g.mu.Unlock()
		return
	}
	if g.send(key, "raised", title, body) { // only a stored/queued alert counts as fired
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
		g.send(key, "recovered", title+": recovered", "Recovered: "+title)
	}
}
func (m *Monitor) writeLocked() {
	m.snapshot.UpdatedAt = m.clock.Now()
	m.snapshot.Version = m.version()
	if m.reason != nil {
		m.snapshot.VersionReason = m.reason()
	}
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

// ntfyURL is the configured ntfy URL, whose path is the secret topic; "" when ntfy is not configured.
func (m *Monitor) ntfyURL() string {
	if m.cfg != nil && m.cfg.Alerts.Ntfy != nil {
		return m.cfg.Alerts.Ntfy.URL
	}
	return ""
}
