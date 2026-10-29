package store

import (
	"context"
	"database/sql"
	"fmt"
)

// ConnectionPeriod is a row of connection_periods: a stretch of time in a non-ok connection state. EndedAt 0 means the
// period is still open; at most one period is open (a partial unique index). OfflinePreviewMsgs and
// OfflineCompletedCount are nil until known.
type ConnectionPeriod struct {
	ID                                        int64
	State, CauseClass, LastError              string
	StartedAt, EndedAt                        int64
	ErrorCount                                int
	OfflinePreviewMsgs, OfflineCompletedCount *int64
}

func (p ConnectionPeriod) validate() error {
	if !ValidConnectionState(p.State) {
		return invalid("unknown connection state %q", p.State)
	}
	if !ValidConnectionCause(p.CauseClass) {
		return invalid("unknown connection cause class %q", p.CauseClass)
	}
	return nil
}

// OpenConnectionPeriod starts a period. A period that is still open ends at the new one's start, in the same
// transaction, so the periods never overlap. It returns the new period's id.
func (s *Store) OpenConnectionPeriod(ctx context.Context, p ConnectionPeriod) (int64, error) {
	if err := p.validate(); err != nil {
		return 0, err
	}
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin connection period: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `UPDATE connection_periods SET ended_at=? WHERE ended_at IS NULL`, p.StartedAt); err != nil {
		return 0, fmt.Errorf("end the open connection period: %w", err)
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO connection_periods(state,started_at,cause_class,last_error,offline_preview_msgs,offline_completed_count) VALUES(?,?,?,?,?,?)`,
		p.State, p.StartedAt, p.CauseClass, p.LastError, p.OfflinePreviewMsgs, p.OfflineCompletedCount)
	if err != nil {
		return 0, fmt.Errorf("open connection period: %w", classify(err))
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// CloseConnectionPeriod ends the open period at the given time and reports whether there was one.
func (s *Store) CloseConnectionPeriod(ctx context.Context, at int64) (bool, error) {
	result, err := s.write.ExecContext(ctx, `UPDATE connection_periods SET ended_at=? WHERE ended_at IS NULL`, at)
	if err != nil {
		return false, fmt.Errorf("close connection period: %w", err)
	}
	n, err := result.RowsAffected()
	return n > 0, err
}

// NoteConnectionError counts an error in the open period and keeps the latest message (already redacted by the caller).
// With no open period it does nothing.
func (s *Store) NoteConnectionError(ctx context.Context, lastError string) error {
	_, err := s.write.ExecContext(ctx, `UPDATE connection_periods SET error_count=error_count+1,last_error=? WHERE ended_at IS NULL`, lastError)
	if err != nil {
		return fmt.Errorf("note connection error: %w", err)
	}
	return nil
}

// NoteOfflineSync attaches reconnect backlog evidence to the most recently
// closed outage. A nil value leaves that count unchanged.
func (s *Store) NoteOfflineSync(ctx context.Context, preview, completed *int64) error {
	_, err := s.write.ExecContext(ctx, `UPDATE connection_periods
 SET offline_preview_msgs=COALESCE(?,offline_preview_msgs),
     offline_completed_count=COALESCE(?,offline_completed_count)
 WHERE id=(SELECT id FROM connection_periods WHERE ended_at IS NOT NULL ORDER BY ended_at DESC,id DESC LIMIT 1)`, preview, completed)
	if err != nil {
		return fmt.Errorf("note offline sync: %w", err)
	}
	return nil
}

// InsertClosedConnectionPeriod stores a period that is already over (R17: the time the process was not running).
func (s *Store) InsertClosedConnectionPeriod(ctx context.Context, p ConnectionPeriod) error {
	if err := p.validate(); err != nil {
		return err
	}
	_, err := s.write.ExecContext(ctx, `INSERT INTO connection_periods(state,started_at,ended_at,cause_class,error_count,last_error,offline_preview_msgs,offline_completed_count) VALUES(?,?,?,?,?,?,?,?)`,
		p.State, p.StartedAt, p.EndedAt, p.CauseClass, p.ErrorCount, p.LastError, p.OfflinePreviewMsgs, p.OfflineCompletedCount)
	if err != nil {
		return fmt.Errorf("insert connection period: %w", classify(err))
	}
	return nil
}

// ConnectionPeriods lists the periods that end at or after the given unix time (or are still open), oldest first.
func (s *Store) ConnectionPeriods(ctx context.Context, since int64) ([]ConnectionPeriod, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT id,state,started_at,COALESCE(ended_at,0),cause_class,error_count,last_error,offline_preview_msgs,offline_completed_count
 FROM connection_periods WHERE ended_at IS NULL OR ended_at>=? ORDER BY started_at,id`, since)
	if err != nil {
		return nil, fmt.Errorf("read connection periods: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []ConnectionPeriod
	for rows.Next() {
		var p ConnectionPeriod
		var preview, completed sql.NullInt64
		if err = rows.Scan(&p.ID, &p.State, &p.StartedAt, &p.EndedAt, &p.CauseClass, &p.ErrorCount, &p.LastError, &preview, &completed); err != nil {
			return nil, fmt.Errorf("scan connection period: %w", err)
		}
		if preview.Valid {
			p.OfflinePreviewMsgs = &preview.Int64
		}
		if completed.Valid {
			p.OfflineCompletedCount = &completed.Int64
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Alert is a row of the alert outbox. Kind is raised, recovered or test; State is queued, sent or skipped (no sink is
// configured). An alert is stored before it counts as fired.
type Alert struct {
	ID                      int64
	Key, Kind, Title, Body  string
	State, LastError        string
	RaisedAt, NextAttemptAt int64
	SentAt                  int64
	Attempts                int
}

// EnqueueAlert stores an alert as queued and returns its id.
func (s *Store) EnqueueAlert(ctx context.Context, a Alert) (int64, error) {
	result, err := s.write.ExecContext(ctx, `INSERT INTO alerts(key,kind,title,body,raised_at,state,next_attempt_at) VALUES(?,?,?,?,?,'queued',?)`,
		a.Key, a.Kind, a.Title, a.Body, a.RaisedAt, a.NextAttemptAt)
	if err != nil {
		return 0, fmt.Errorf("enqueue alert %s: %w", a.Key, classify(err))
	}
	return result.LastInsertId()
}

// DueAlerts lists up to limit queued alerts whose next attempt is due at the given time, oldest first.
func (s *Store) DueAlerts(ctx context.Context, now int64, limit int) ([]Alert, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT id,key,kind,title,body,raised_at,state,attempts,next_attempt_at,COALESCE(sent_at,0),last_error
 FROM alerts WHERE state='queued' AND next_attempt_at<=? ORDER BY id LIMIT ?`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("read due alerts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Alert
	for rows.Next() {
		var a Alert
		if err = rows.Scan(&a.ID, &a.Key, &a.Kind, &a.Title, &a.Body, &a.RaisedAt, &a.State, &a.Attempts, &a.NextAttemptAt, &a.SentAt, &a.LastError); err != nil {
			return nil, fmt.Errorf("scan alert: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// AlertSent marks an alert sent at the given time.
func (s *Store) AlertSent(ctx context.Context, id, at int64) error {
	return s.setAlert(ctx, `UPDATE alerts SET state='sent',sent_at=?,attempts=attempts+1,last_error='' WHERE id=?`, at, id)
}

// AlertSkipped marks an alert skipped: no sink is configured, so it will never be sent.
func (s *Store) AlertSkipped(ctx context.Context, id int64) error {
	return s.setAlert(ctx, `UPDATE alerts SET state='skipped' WHERE id=?`, id)
}

// AlertFailed counts a failed send, keeps the error and schedules the next attempt; the alert stays queued.
func (s *Store) AlertFailed(ctx context.Context, id int64, errText string, nextAttemptAt int64) error {
	return s.setAlert(ctx, `UPDATE alerts SET attempts=attempts+1,last_error=?,next_attempt_at=? WHERE id=?`, errText, nextAttemptAt, id)
}

func (s *Store) setAlert(ctx context.Context, query string, args ...any) error {
	if _, err := s.write.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("update alert: %w", err)
	}
	return nil
}
