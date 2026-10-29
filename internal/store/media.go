package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// The media queue's reads and state changes. Insertion is Apply (the row comes with its message, R2). The thumbnail is
// the last column (R5) and no query here selects it; the raw proto of a message is read from messages by a
// primary-key lookup of the one row that is returned (R4).

// mediaColumns are the columns of media except thumb, in the order Media.fields scans them.
const mediaColumns = `chat_jid,message_id,sender_jid,from_me,source,source_rank,type,mime,declared_size,filename,duration_s,width,height,pages,message_ts,enqueued_at,state,reason,error_class,http_status,attempts,next_attempt_at,retry_direct_path,path,sha256,updated_at,done_at`

// withRawProto adds the message's raw proto to mediaColumns.
const withRawProto = mediaColumns + `,COALESCE((SELECT raw_proto FROM messages WHERE chat_jid=media.chat_jid AND id=media.message_id),X'')`

func (m *Media) fields() []any {
	return []any{&m.ChatJID, &m.MessageID, &m.SenderJID, &m.FromMe, &m.Source, &m.SourceRank, &m.Type, &m.MIME, &m.DeclaredSize, &m.Filename, &m.DurationS, &m.Width, &m.Height, &m.Pages, &m.MessageTimestamp, &m.EnqueuedAt, &m.State, &m.Reason, &m.ErrorClass, &m.HTTPStatus, &m.Attempts, &m.NextAttemptAt, &m.RetryDirectPath, &m.Path, &m.SHA256, &m.UpdatedAt, &m.DoneAt}
}
func (m *Media) fieldsWithRawProto() []any { return append(m.fields(), &m.RawProto) }

// The queue order (R: asked for first, live before history and replay, newest first) is the order of the media_pick
// index. Without statistics SQLite plans media_due plus a temp b-tree sort of every due row, so these queries name the
// index; a query-plan test pins that there is no sort.
const (
	claimMediaQuery  = `SELECT ` + withRawProto + ` FROM media INDEXED BY media_pick WHERE state='pending' AND next_attempt_at<=?1 ORDER BY source_rank,message_ts DESC LIMIT 1`
	nextExpiredQuery = `SELECT ` + withRawProto + ` FROM media INDEXED BY media_pick WHERE state='expired' ORDER BY source_rank,message_ts DESC LIMIT 1`
	mediaByIDQuery   = `SELECT ` + withRawProto + ` FROM media WHERE chat_jid=? AND message_id=?`
)

// MediaTimeliness is SC-3 for enabled live media in the trailing day and week.
type MediaTimeliness struct {
	DayDone, DayTotal   int
	WeekDone, WeekTotal int
}

// MediaTimeliness counts non-skipped live rows and those completed no more than five minutes after enqueue.
func (s *Store) MediaTimeliness(ctx context.Context, now time.Time) (MediaTimeliness, error) {
	var m MediaTimeliness
	day := now.Add(-24 * time.Hour).Unix()
	week := now.Add(-7 * 24 * time.Hour).Unix()
	err := s.read.QueryRowContext(ctx, `SELECT
 COALESCE(SUM(CASE WHEN enqueued_at>=?1 AND state='done' AND done_at>=enqueued_at AND done_at-enqueued_at<=300 THEN 1 ELSE 0 END),0),
 COALESCE(SUM(CASE WHEN enqueued_at>=?1 THEN 1 ELSE 0 END),0),
 COALESCE(SUM(CASE WHEN state='done' AND done_at>=enqueued_at AND done_at-enqueued_at<=300 THEN 1 ELSE 0 END),0),
 COUNT(*)
 FROM media INDEXED BY media_live_enqueued
 WHERE source='live' AND state<>'skipped' AND enqueued_at>=?2`, day, week).
		Scan(&m.DayDone, &m.DayTotal, &m.WeekDone, &m.WeekTotal)
	return m, err
}

// MediaByID reads one media row with its raw proto.
func (s *Store) MediaByID(ctx context.Context, chat, id string) (Media, error) {
	var m Media
	err := s.read.QueryRowContext(ctx, mediaByIDQuery, chat, id).Scan(m.fieldsWithRawProto()...)
	return m, err
}

// MediaThumb reads the thumbnail of a media row (empty when it has none).
func (s *Store) MediaThumb(ctx context.Context, chat, id string) ([]byte, error) {
	var thumb []byte
	if err := s.read.QueryRowContext(ctx, `SELECT thumb FROM media WHERE chat_jid=? AND message_id=?`, chat, id).Scan(&thumb); err != nil {
		return nil, fmt.Errorf("thumbnail of %s: %w", id, err)
	}
	return thumb, nil
}

// NextMediaDue returns the earliest pending download time. The media_due index makes this one index seek.
func (s *Store) NextMediaDue(ctx context.Context) (time.Time, bool, error) {
	var due sql.NullInt64
	if err := s.read.QueryRowContext(ctx, `SELECT MIN(next_attempt_at) FROM media WHERE state='pending'`).Scan(&due); err != nil {
		return time.Time{}, false, err
	}
	if !due.Valid {
		return time.Time{}, false, nil
	}
	return time.Unix(due.Int64, 0), true, nil
}

// ClaimMedia marks the next pending row that is due at now as downloading and returns it with its raw proto, or nil.
func (s *Store) ClaimMedia(ctx context.Context, now time.Time) (*Media, error) {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var m Media
	err = tx.QueryRowContext(ctx, claimMediaQuery, now.Unix()).Scan(m.fieldsWithRawProto()...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE media SET state='downloading' WHERE chat_jid=? AND message_id=? AND state='pending'`, m.ChatJID, m.MessageID); err != nil {
		return nil, err
	}
	if err = mediaMoved(ctx, tx, m.ChatJID, "pending", "downloading", m.DeclaredSize, 1); err != nil { // same bucket: no change
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	m.State = "downloading"
	return &m, nil
}

// UpdateMedia writes the download outcome of a row: path, hash, state, reason, error class, HTTP status, attempts, the
// re-upload path and the times.
func (s *Store) UpdateMedia(ctx context.Context, m Media) error {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var old string
	var size int64
	err = tx.QueryRowContext(ctx, `SELECT state,declared_size FROM media WHERE chat_jid=? AND message_id=?`, m.ChatJID, m.MessageID).Scan(&old, &size)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // nothing to update
	}
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE media SET path=?,sha256=?,state=?,reason=?,error_class=?,http_status=?,attempts=?,retry_direct_path=?,updated_at=?,next_attempt_at=?,done_at=? WHERE chat_jid=? AND message_id=?`,
		m.Path, m.SHA256, m.State, m.Reason, m.ErrorClass, m.HTTPStatus, m.Attempts, m.RetryDirectPath, m.UpdatedAt, m.NextAttemptAt, m.DoneAt, m.ChatJID, m.MessageID); err != nil {
		return classify(err)
	}
	// The count of the old bucket moves to the new one in the same transaction (schema 4.3 step 6).
	if err = mediaMoved(ctx, tx, m.ChatJID, old, m.State, size, 1); err != nil {
		return err
	}
	return tx.Commit()
}

// ResetMediaOnStart puts interrupted downloads back in the queue. downloading and pending share a chat_stats bucket, so
// the counts do not move.
func (s *Store) ResetMediaOnStart(ctx context.Context, now time.Time) error {
	if _, err := s.write.ExecContext(ctx, `UPDATE media SET state='pending',reason='' WHERE state='downloading';`); err != nil {
		return err
	}
	return s.FailStaleRetries(ctx, now)
}

// FailStaleRetries fails re-upload requests that have had no answer for 24 hours.
func (s *Store) FailStaleRetries(ctx context.Context, now time.Time) error {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	cutoff := now.Add(-24 * time.Hour).Unix()
	rows, err := tx.QueryContext(ctx, `SELECT chat_jid,COUNT(*) FROM media WHERE state='retry_requested' AND updated_at<? GROUP BY chat_jid`, cutoff)
	if err != nil {
		return err
	}
	moved := map[string]int{}
	for rows.Next() {
		var chat string
		var n int
		if err = rows.Scan(&chat, &n); err != nil {
			_ = rows.Close()
			return err
		}
		moved[chat] = n
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE media SET state='failed',reason='no re-upload' WHERE state='retry_requested' AND updated_at<?`, cutoff); err != nil {
		return err
	}
	for chat, n := range moved {
		if err = mediaMoved(ctx, tx, chat, "retry_requested", "failed", 0, n); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// NextExpiredMedia returns the expired row to request a re-upload for next (queue order), or nil.
func (s *Store) NextExpiredMedia(ctx context.Context) (*Media, error) {
	var m Media
	err := s.read.QueryRowContext(ctx, nextExpiredQuery).Scan(m.fieldsWithRawProto()...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}
