package wa

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow/types/events"

	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/logsafe"
)

const (
	connectionPollInterval = 30 * time.Second
	reconnectingAfter      = 60 * time.Second
)

type connectivity interface {
	IsConnected() bool
}

// connectionMonitor observes both whatsmeow events and the socket. The socket
// poll is deliberately independent of Disconnected events: whatsmeow's
// keepalive timeout path can disconnect and auto-reconnect without emitting one.
type connectionMonitor struct {
	mu         sync.Mutex
	socket     connectivity
	health     ingest.ConnectionStateSink
	now        func() time.Time
	state      string
	lastOnline time.Time
}

func newConnectionMonitor(socket connectivity, health ingest.ConnectionStateSink, now func() time.Time) *connectionMonitor {
	return &connectionMonitor{socket: socket, health: health, now: now}
}

func (m *connectionMonitor) handle(evt any) {
	switch e := evt.(type) {
	case *events.Connected:
		m.set("connected", "")
	case *events.KeepAliveTimeout:
		m.set("degraded", fmt.Sprintf("keepalive timeout (failures=%d)", e.ErrorCount))
	case *events.KeepAliveRestored:
		m.set("connected", "")
	case *events.Disconnected:
		m.set("disconnected", "")
	}
}

func (m *connectionMonitor) reconnectFailed(err error, count int) {
	class := reconnectErrorClass(err)
	m.set("reconnecting", fmt.Sprintf("reconnect_failed{class=%s,count=%d}", class, count))
}

func (m *connectionMonitor) set(state, detail string) {
	m.mu.Lock()
	if state == "connected" {
		m.lastOnline = m.now()
	}
	changed := state != m.state || detail != ""
	m.state = state
	m.mu.Unlock()
	if changed {
		ingest.ReportConnectionState(m.health, state, detail)
	}
}

func (m *connectionMonitor) poll() {
	now := m.now()
	connected := m.socket.IsConnected()
	m.mu.Lock()
	if connected {
		m.lastOnline = now
		m.mu.Unlock()
		return
	}
	lastOnline := m.lastOnline
	state := m.state
	if lastOnline.IsZero() {
		m.lastOnline = now
	}
	m.mu.Unlock()
	if !lastOnline.IsZero() && now.Sub(lastOnline) >= reconnectingAfter && (state == "connected" || state == "degraded") {
		m.set("reconnecting", "socket disconnected without an event")
	}
}

func (m *connectionMonitor) run(ctx context.Context) {
	ticker := time.NewTicker(connectionPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.poll()
		}
	}
}

func reconnectErrorClass(err error) string {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "dns"
	}
	lower := strings.ToLower(logsafe.RedactURLs(err.Error()))
	switch {
	case strings.Contains(lower, "no such host"), strings.Contains(lower, "dns"), strings.Contains(lower, "lookup "):
		return "dns"
	case strings.Contains(lower, "tls"), strings.Contains(lower, "x509"), strings.Contains(lower, "certificate"):
		return "tls"
	case strings.Contains(lower, "websocket"), strings.Contains(lower, "bad handshake"), strings.Contains(lower, "upgrade"):
		return "ws"
	case strings.Contains(lower, "tcp"), strings.Contains(lower, "connection refused"), strings.Contains(lower, "connection reset"), strings.Contains(lower, "i/o timeout"):
		return "tcp"
	default:
		return "other"
	}
}
