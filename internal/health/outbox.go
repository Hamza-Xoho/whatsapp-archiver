package health

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"whatsapp-archiver/internal/logsafe"
)

func (m *Monitor) runAlertOutbox(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	heartbeat := time.NewTicker(5 * time.Minute)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.wakeAlert:
			_ = m.sendDueAlerts(ctx)
		case <-ticker.C:
			_ = m.sendDueAlerts(ctx)
		case <-heartbeat.C:
			if err := m.pingHeartbeat(ctx); err != nil {
				m.log.Error("heartbeat ping failed", "err", logsafe.RedactURLs(err.Error()))
			}
		}
	}
}

func (m *Monitor) pingHeartbeat(ctx context.Context) error {
	url := m.cfg.Alerts.HeartbeatURL
	if m.heartbeatURL != nil {
		url = m.heartbeatURL(ctx)
	}
	if url == "" || m.heartbeat == nil {
		return nil
	}
	m.mu.Lock()
	state, lastEvent := m.snapshot.State, m.snapshot.LastEvent
	m.mu.Unlock()
	limits := m.cfg.Alerts
	if m.limits != nil {
		limits = m.limits()
	}
	if state != "connected" || lastEvent.IsZero() || limits.NoEventsAfter <= 0 || m.clock.Now().Sub(lastEvent) > limits.NoEventsAfter {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := m.heartbeat.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("heartbeat status %s", resp.Status)
	}
	return nil
}

// sendDueAlerts drains queued rows in id order. A failed oldest row stops the
// pass, so its recovery can never overtake it.
func (m *Monitor) sendDueAlerts(ctx context.Context) error {
	for {
		a, ok, err := m.outbox.OldestQueuedAlert(ctx)
		if err != nil || !ok {
			return err
		}
		now := m.clock.Now()
		if a.NextAttemptAt > now.Unix() {
			return nil
		}
		if m.n == nil {
			if err = m.outbox.AlertSkipped(ctx, a.ID); err != nil {
				return err
			}
			continue
		}
		body := a.Body
		if now.Unix()-a.RaisedAt >= 60 {
			body += fmt.Sprintf("\nraised %s, delivered late", time.Unix(a.RaisedAt, 0).UTC().Format("15:04"))
		}
		if err = m.n.Notify(ctx, a.Title, body); err != nil {
			safe := logsafe.RedactNtfy(logsafe.RedactURLs(err.Error()), m.ntfyURL())
			next := now.Add(alertRetryBackoff(a.Attempts)).Unix()
			if storeErr := m.outbox.AlertFailed(context.WithoutCancel(ctx), a.ID, safe, next); storeErr != nil {
				return storeErr
			}
			m.log.Error("alert not sent", "title", a.Title, "err", safe)
			return err
		}
		if err = m.outbox.AlertSent(context.WithoutCancel(ctx), a.ID, now.Unix()); err != nil {
			return err
		}
	}
}

func alertRetryBackoff(attempts int) time.Duration {
	backoff := time.Minute << min(attempts, 6)
	return min(backoff, time.Hour)
}
