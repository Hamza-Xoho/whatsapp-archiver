package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

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
		return false, err
	}
	n, err := result.RowsAffected()
	return n > 0, err
}

func (s *Store) UpsertChat(ctx context.Context, c Chat) error {
	_, err := s.write.ExecContext(ctx, `INSERT INTO chats(jid,name,type,parent_jid) VALUES(?,?,?,?) ON CONFLICT(jid) DO UPDATE SET name=excluded.name,type=excluded.type,parent_jid=excluded.parent_jid`, c.JID, c.Name, c.Type, c.ParentJID)
	return err
}
func (s *Store) UpsertParticipant(ctx context.Context, p Participant) error {
	_, err := s.write.ExecContext(ctx, `INSERT INTO participants(jid,display_name) VALUES(?,?) ON CONFLICT(jid) DO UPDATE SET display_name=excluded.display_name`, p.JID, p.DisplayName)
	return err
}
func (s *Store) InsertIDMapping(ctx context.Context, m IDMapping) (bool, error) {
	return s.insert(ctx, `INSERT INTO id_mappings(lid,pn,source,first_seen) VALUES(?,?,?,?) ON CONFLICT DO NOTHING`, m.LID, m.PN, m.Source, m.FirstSeen.Unix())
}

func (s *Store) InsertMessage(ctx context.Context, m Message) (bool, error) {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin insert message: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var exists int
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM messages x LEFT JOIN chat_aliases xa ON xa.alias_jid=x.chat_jid LEFT JOIN chat_aliases ma ON ma.alias_jid=? WHERE x.id=? AND COALESCE(xa.canonical_jid,x.chat_jid)=COALESCE(ma.canonical_jid,?))`, m.ChatJID, m.ID, m.ChatJID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check message alias: %w", err)
	}
	if exists != 0 {
		return false, nil
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO messages(chat_jid,id,sender_jid,ts,received_at,source,text,quoted_id,forwarded,media_type,raw_proto) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`, m.ChatJID, m.ID, m.SenderJID, m.Timestamp, m.ReceivedAt, m.Source, m.Text, m.QuotedID, m.Forwarded, m.MediaType, m.RawProto)
	if err != nil {
		return false, fmt.Errorf("insert message %s: %w", m.ID, err)
	}
	if err = tx.Commit(); err != nil {
		return false, fmt.Errorf("commit message %s: %w", m.ID, err)
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
	_, err := s.write.ExecContext(ctx, `INSERT INTO media(chat_jid,message_id,sender_jid,source,type,mime,declared_size,raw_proto,path,sha256,state,reason,attempts,retry_direct_path,updated_at,message_ts) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(chat_jid,message_id) DO NOTHING`, m.ChatJID, m.MessageID, m.SenderJID, m.Source, m.Type, m.MIME, m.DeclaredSize, m.RawProto, m.Path, m.SHA256, m.State, m.Reason, m.Attempts, m.RetryDirectPath, m.UpdatedAt, m.MessageTimestamp)
	return err
}
func (s *Store) SetMediaState(ctx context.Context, chat, id, state, reason string) error {
	_, err := s.write.ExecContext(ctx, `UPDATE media SET state=?,reason=?,updated_at=? WHERE chat_jid=? AND message_id=?`, state, reason, time.Now().Unix(), chat, id)
	return err
}

func (s *Store) Aliases(ctx context.Context, jid string) ([]string, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT alias_jid FROM chat_aliases WHERE canonical_jid=(SELECT canonical_jid FROM chat_aliases WHERE alias_jid=? LIMIT 1) AND alias_jid<>? ORDER BY alias_jid`, jid, jid)
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
