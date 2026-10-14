package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// ErrConstraint marks a write rejected by a SQLite constraint; retrying the same write can never succeed.
var ErrConstraint = errors.New("constraint violation")

func classify(err error) error {
	var se *sqlite.Error
	if errors.As(err, &se) && se.Code()&0xff == sqlite3.SQLITE_CONSTRAINT {
		return fmt.Errorf("%w: %w", ErrConstraint, err)
	}
	return err
}

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct {
	write *sql.DB
	read  *sql.DB
	path  string
}
type Chat struct{ JID, Name, Type, ParentJID string }
type Participant struct{ JID, DisplayName string }
type IDMapping struct {
	LID, PN, Source string
	FirstSeen       time.Time
}
type Message struct {
	ChatJID, ID, SenderJID, Source, Text, QuotedID, MediaType string
	Timestamp, ReceivedAt                                     int64
	Forwarded                                                 bool
	RawProto                                                  []byte
}
type MessageEvent struct {
	ChatJID, TargetID, Kind, EventID, SenderJID, NewText, Value string
	Timestamp, ReceivedAt                                       int64
	RawProto                                                    []byte
}
type ChatEvent struct {
	ChatJID, Kind, EventID, SenderJID, Value string
	Timestamp, ReceivedAt                    int64
	RawProto                                 []byte
}
type Media struct {
	ChatJID, MessageID, SenderJID, Source, Type, MIME, Path, SHA256, State, Reason, RetryDirectPath string
	DeclaredSize, UpdatedAt, MessageTimestamp                                                       int64
	Attempts                                                                                        int
	RawProto                                                                                        []byte
}
type ChatStat struct {
	CanonicalJID, Name    string
	MessageCount          int
	Oldest, Newest        int64
	MediaFailed, OpenGaps int
}
type HistoryChunk struct {
	ID, SyncType                                          string
	ChunkOrder, Progress, ConversationCount, MessageCount int
	Oldest, Newest, ReceivedAt                            int64
}
type Coverage struct {
	Chunks, LastProgress int
	LastChunkTime        int64
	OldestByChat         map[string]int64
}

func Open(path string) (*Store, error) {
	if err := ensureParent(path); err != nil {
		return nil, err
	}
	dsn := "file:" + filepath.ToSlash(path) + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	write, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open archive writer: %w", err)
	}
	write.SetMaxOpenConns(1)
	if _, err = write.Exec("PRAGMA journal_mode=WAL"); err != nil {
		_ = write.Close()
		return nil, fmt.Errorf("enable WAL: %w", err)
	}
	data, err := migrations.ReadFile("migrations/0001_raw.sql")
	if err != nil {
		_ = write.Close()
		return nil, err
	}
	if _, err = write.Exec(string(data)); err != nil {
		_ = write.Close()
		return nil, fmt.Errorf("migrate archive: %w", err)
	}
	if _, err = write.Exec("INSERT OR IGNORE INTO schema_version(version,applied_at) VALUES(1,?)", time.Now().Unix()); err != nil {
		_ = write.Close()
		return nil, err
	}
	read, err := sql.Open("sqlite", dsn+"&mode=ro")
	if err != nil {
		_ = write.Close()
		return nil, fmt.Errorf("open archive reader: %w", err)
	}
	return &Store{write: write, read: read, path: path}, nil
}

func ensureParent(path string) error { return mkdirAll(filepath.Dir(path)) }

func (s *Store) Close() error {
	rerr := s.read.Close()
	werr := s.write.Close()
	if werr != nil {
		return werr
	}
	return rerr
}
func (s *Store) Path() string { return s.path }

func (s *Store) insert(ctx context.Context, query string, args ...any) (bool, error) {
	result, err := s.write.ExecContext(ctx, query, args...)
	if err != nil {
		return false, classify(err)
	}
	n, err := result.RowsAffected()
	return n > 0, err
}

func (s *Store) UpsertChat(ctx context.Context, c Chat) error {
	_, err := s.write.ExecContext(ctx, `INSERT INTO chats(jid,name,type,parent_jid) VALUES(?,?,?,?) ON CONFLICT(jid) DO UPDATE SET name=COALESCE(NULLIF(excluded.name,''),name),type=excluded.type,parent_jid=excluded.parent_jid`, c.JID, c.Name, c.Type, c.ParentJID)
	return classify(err)
}
func (s *Store) UpsertParticipant(ctx context.Context, p Participant) error {
	_, err := s.write.ExecContext(ctx, `INSERT INTO participants(jid,display_name) VALUES(?,?) ON CONFLICT(jid) DO UPDATE SET display_name=COALESCE(NULLIF(excluded.display_name,''),display_name)`, p.JID, p.DisplayName)
	return classify(err)
}
func (s *Store) InsertIDMapping(ctx context.Context, m IDMapping) (bool, error) {
	return s.insert(ctx, `INSERT INTO id_mappings(lid,pn,source,first_seen) VALUES(?,?,?,?) ON CONFLICT DO NOTHING`, m.LID, m.PN, m.Source, m.FirstSeen.Unix())
}

// aliasSet selects the aliases of chat ?1: the chat itself, the lids of a phone number, the phone numbers of a lid and
// the other phone numbers of those lids. It uses only id_mappings primary-key and (pn, lid) index lookups, so its cost
// does not depend on the size of messages or chats. The chat_aliases view stays for report paths; it must not be used
// per message or per event because building it scans the archive.
const aliasSet = `SELECT ?1
 UNION SELECT lid FROM id_mappings WHERE pn=?1
 UNION SELECT pn FROM id_mappings WHERE lid=?1
 UNION SELECT pn FROM id_mappings WHERE lid IN (SELECT lid FROM id_mappings WHERE pn=?1)`

// dedupeQuery reports whether message ?2 already exists under any alias of chat ?1, probing messages by its
// (chat_jid, id) primary key.
const dedupeQuery = `SELECT EXISTS(SELECT 1 FROM messages WHERE id=?2 AND chat_jid IN (` + aliasSet + `))`

// aliasesQuery lists the aliases of chat ?1 other than itself.
const aliasesQuery = aliasSet + ` EXCEPT SELECT ?1 ORDER BY 1`

func (s *Store) InsertMessage(ctx context.Context, m Message) (bool, error) {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin insert message: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var exists int
	err = tx.QueryRowContext(ctx, dedupeQuery, m.ChatJID, m.ID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check message alias: %w", classify(err))
	}
	if exists != 0 {
		return false, nil
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO messages(chat_jid,id,sender_jid,ts,received_at,source,text,quoted_id,forwarded,media_type,raw_proto) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`, m.ChatJID, m.ID, m.SenderJID, m.Timestamp, m.ReceivedAt, m.Source, m.Text, m.QuotedID, m.Forwarded, m.MediaType, m.RawProto)
	if err != nil {
		return false, fmt.Errorf("insert message %s: %w", m.ID, classify(err))
	}
	if err = tx.Commit(); err != nil {
		return false, fmt.Errorf("commit message %s: %w", m.ID, classify(err))
	}
	n, err := result.RowsAffected()
	return n > 0, err
}

func (s *Store) InsertEvent(ctx context.Context, e MessageEvent) (bool, error) {
	if e.RawProto == nil {
		e.RawProto = []byte{}
	}
	return s.insert(ctx, `INSERT INTO message_events(chat_jid,target_id,kind,event_id,sender_jid,ts,received_at,new_text,value,raw_proto) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`, e.ChatJID, e.TargetID, e.Kind, e.EventID, e.SenderJID, e.Timestamp, e.ReceivedAt, e.NewText, e.Value, e.RawProto)
}
func (s *Store) InsertChatEvent(ctx context.Context, e ChatEvent) (bool, error) {
	if e.RawProto == nil {
		e.RawProto = []byte{}
	}
	return s.insert(ctx, `INSERT INTO chat_events(chat_jid,kind,event_id,sender_jid,ts,received_at,value,raw_proto) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`, e.ChatJID, e.Kind, e.EventID, e.SenderJID, e.Timestamp, e.ReceivedAt, e.Value, e.RawProto)
}
func (s *Store) InsertMedia(ctx context.Context, m Media) error {
	if m.RawProto == nil {
		m.RawProto = []byte{}
	}
	_, err := s.write.ExecContext(ctx, `INSERT INTO media(chat_jid,message_id,sender_jid,source,type,mime,declared_size,raw_proto,path,sha256,state,reason,attempts,retry_direct_path,updated_at,message_ts) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(chat_jid,message_id) DO NOTHING`, m.ChatJID, m.MessageID, m.SenderJID, m.Source, m.Type, m.MIME, m.DeclaredSize, m.RawProto, m.Path, m.SHA256, m.State, m.Reason, m.Attempts, m.RetryDirectPath, m.UpdatedAt, m.MessageTimestamp)
	return classify(err)
}
func (s *Store) SetMediaState(ctx context.Context, chat, id, state, reason string) error {
	_, err := s.write.ExecContext(ctx, `UPDATE media SET state=?,reason=?,updated_at=? WHERE chat_jid=? AND message_id=?`, state, reason, time.Now().Unix(), chat, id)
	return err
}
func (s *Store) MediaByID(ctx context.Context, chat, id string) (Media, error) {
	var m Media
	err := s.read.QueryRowContext(ctx, `SELECT chat_jid,message_id,sender_jid,source,type,mime,declared_size,raw_proto,path,sha256,state,reason,attempts,retry_direct_path,updated_at,message_ts FROM media WHERE chat_jid=? AND message_id=?`, chat, id).Scan(&m.ChatJID, &m.MessageID, &m.SenderJID, &m.Source, &m.Type, &m.MIME, &m.DeclaredSize, &m.RawProto, &m.Path, &m.SHA256, &m.State, &m.Reason, &m.Attempts, &m.RetryDirectPath, &m.UpdatedAt, &m.MessageTimestamp)
	return m, err
}
func (s *Store) ClaimMedia(ctx context.Context) (*Media, error) {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var m Media
	err = tx.QueryRowContext(ctx, `SELECT chat_jid,message_id,sender_jid,source,type,mime,declared_size,raw_proto,path,sha256,state,reason,attempts,retry_direct_path,updated_at,message_ts FROM media WHERE state='pending' ORDER BY CASE source WHEN 'live' THEN 0 ELSE 1 END,message_ts DESC LIMIT 1`).Scan(&m.ChatJID, &m.MessageID, &m.SenderJID, &m.Source, &m.Type, &m.MIME, &m.DeclaredSize, &m.RawProto, &m.Path, &m.SHA256, &m.State, &m.Reason, &m.Attempts, &m.RetryDirectPath, &m.UpdatedAt, &m.MessageTimestamp)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE media SET state='downloading' WHERE chat_jid=? AND message_id=? AND state='pending'`, m.ChatJID, m.MessageID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	m.State = "downloading"
	return &m, nil
}
func (s *Store) UpdateMedia(ctx context.Context, m Media) error {
	_, err := s.write.ExecContext(ctx, `UPDATE media SET path=?,sha256=?,state=?,reason=?,attempts=?,retry_direct_path=?,updated_at=? WHERE chat_jid=? AND message_id=?`, m.Path, m.SHA256, m.State, m.Reason, m.Attempts, m.RetryDirectPath, m.UpdatedAt, m.ChatJID, m.MessageID)
	return err
}
func (s *Store) ResetMediaOnStart(ctx context.Context, now time.Time) error {
	_, err := s.write.ExecContext(ctx, `UPDATE media SET state='pending',reason='' WHERE state='downloading';UPDATE media SET state='failed',reason='no re-upload' WHERE state='retry_requested' AND updated_at<?`, now.Add(-24*time.Hour).Unix())
	return err
}

func (s *Store) Aliases(ctx context.Context, jid string) ([]string, error) {
	rows, err := s.read.QueryContext(ctx, aliasesQuery, jid)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var v string
		if err = rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) OpenGapCount(ctx context.Context) (int, error) {
	var n int
	err := s.read.QueryRowContext(ctx, `SELECT COUNT(*) FROM open_gaps`).Scan(&n)
	return n, err
}
func (s *Store) SchemaVersionCount(ctx context.Context) (int, error) {
	var n int
	err := s.read.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_version`).Scan(&n)
	return n, err
}

func (s *Store) InsertHistoryChunk(ctx context.Context, h HistoryChunk) error {
	_, err := s.insert(ctx, `INSERT INTO history_sync_chunks(id,sync_type,chunk_order,progress,conversation_count,message_count,oldest_ts,newest_ts,received_at) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`, h.ID, h.SyncType, h.ChunkOrder, h.Progress, h.ConversationCount, h.MessageCount, h.Oldest, h.Newest, h.ReceivedAt)
	return err
}
func (s *Store) HistoryCoverage(ctx context.Context) (Coverage, error) {
	var c Coverage
	c.OldestByChat = map[string]int64{}
	err := s.read.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE((SELECT progress FROM history_sync_chunks ORDER BY received_at DESC,rowid DESC LIMIT 1),0),COALESCE(MAX(received_at),0) FROM history_sync_chunks`).Scan(&c.Chunks, &c.LastProgress, &c.LastChunkTime)
	if err != nil {
		return c, err
	}
	rows, err := s.read.QueryContext(ctx, `SELECT canonical_jid,MIN(ts) FROM messages_resolved WHERE source='history' GROUP BY canonical_jid`)
	if err != nil {
		return c, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var jid string
		var oldest int64
		if err = rows.Scan(&jid, &oldest); err != nil {
			return c, err
		}
		c.OldestByChat[jid] = oldest
	}
	return c, rows.Err()
}
