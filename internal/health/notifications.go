package health

import (
	"context"

	"whatsapp-archiver/internal/logsafe"
)

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
		m.log.Error("alert not sent", "title", a.title, "err", logsafe.RedactNtfy(err.Error(), m.ntfyURL()))
	}
}
