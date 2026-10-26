package store

import (
	"context"
	"database/sql"
	"os"
	"time"
)

// growthWindow is how far back the weekly growth looks: four weeks, so a quiet or busy week does not decide it.
const growthWindow = 28 * 24 * time.Hour

// ChatBytes is the media size of one chat.
type ChatBytes struct {
	ChatJID string
	Bytes   int64
}

// Usage is what the archive's media takes and how fast the archive grows. Sizes are the declared sizes of the media
// rows that were downloaded (state done), so they cost one query and no file walk.
type Usage struct {
	ByChat      []ChatBytes // largest first
	ByMediaType map[string]int64
	// WeeklyGrowth is the average bytes per week over the last 28 days: the media downloaded in that time, plus the
	// database growth estimated as the messages received in that time times the archive's average bytes per message.
	WeeklyGrowth int64
}

// Usage reads the media sizes and the growth estimate as of now. The sizes of a chat are summed over all its JID forms
// (chat_canonical) and listed under its canonical JID; a media row alone makes no chat_stats row (R20), so the sums come
// from the media table itself. It uses the read pool only.
func (s *Store) Usage(ctx context.Context, now time.Time) (Usage, error) {
	u := Usage{ByMediaType: map[string]int64{}}
	err := s.scanRows(ctx, `SELECT COALESCE(a.canonical,x.chat_jid),SUM(x.declared_size) FROM media x LEFT JOIN chat_canonical a ON a.alias=x.chat_jid WHERE x.state='done' GROUP BY 1 ORDER BY 2 DESC,1`, nil, func(rows *sql.Rows) error {
		var c ChatBytes
		if err := rows.Scan(&c.ChatJID, &c.Bytes); err != nil {
			return err
		}
		u.ByChat = append(u.ByChat, c)
		return nil
	})
	if err != nil {
		return Usage{}, err
	}
	err = s.scanRows(ctx, `SELECT type,SUM(declared_size) FROM media WHERE state='done' GROUP BY type`, nil, func(rows *sql.Rows) error {
		var typ string
		var n int64
		if err := rows.Scan(&typ, &n); err != nil {
			return err
		}
		u.ByMediaType[typ] = n
		return nil
	})
	if err != nil {
		return Usage{}, err
	}
	since := now.Add(-growthWindow).Unix()
	var recentMedia, recentMessages, messages int64
	if err = s.read.QueryRowContext(ctx, `SELECT COALESCE(SUM(declared_size),0) FROM media WHERE state='done' AND updated_at>=?`, since).Scan(&recentMedia); err != nil {
		return Usage{}, err
	}
	if err = s.read.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(received_at>=?),0) FROM messages`, since).Scan(&messages, &recentMessages); err != nil {
		return Usage{}, err
	}
	var dbGrowth int64
	if info, err := os.Stat(s.path); err == nil && messages > 0 {
		dbGrowth = recentMessages * (info.Size() / messages)
	}
	u.WeeklyGrowth = (recentMedia + dbGrowth) / 4
	return u, nil
}
