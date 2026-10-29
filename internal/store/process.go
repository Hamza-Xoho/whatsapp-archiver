package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
)

const lastAliveSetting = "last_alive_at"

// RecordProcessStart closes the interval since the previous process heartbeat
// as not_running, then records this process as alive in the same transaction.
func (s *Store) RecordProcessStart(ctx context.Context, now int64) error {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin process start: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var raw string
	err = tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, lastAliveSetting).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read last alive: %w", err)
	}
	if err == nil {
		last, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil {
			return fmt.Errorf("parse last alive: %w", parseErr)
		}
		if last > 0 && last < now {
			if _, err = tx.ExecContext(ctx, `INSERT INTO connection_periods(state,started_at,ended_at,cause_class) VALUES('not_running',?,?,'not_running')`, last, now); err != nil {
				return fmt.Errorf("record process downtime: %w", err)
			}
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO settings(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, lastAliveSetting, strconv.FormatInt(now, 10), now); err != nil {
		return fmt.Errorf("record process start: %w", err)
	}
	return tx.Commit()
}

// RecordAlive is the watchdog's writer-progress probe.
func (s *Store) RecordAlive(ctx context.Context, now int64) error {
	return s.SetSetting(ctx, lastAliveSetting, strconv.FormatInt(now, 10))
}
