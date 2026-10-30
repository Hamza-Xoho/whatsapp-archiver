package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// ErrConstraint marks a write that can never succeed: rejected by a SQLite constraint, or by the Go validation of a
// batch (an unknown kind, bad details). Retrying the same write is pointless, so callers treat it as permanent.
var ErrConstraint = errors.New("constraint violation")

// ErrOldArchive is returned by Open and OpenReadOnly for an archive written before the V1 schema (R16, ADR-35).
var ErrOldArchive = errors.New("archive predates the V1 schema (ADR-35): use a fresh data folder")

func classify(err error) error {
	var se *sqlite.Error
	if errors.As(err, &se) && se.Code()&0xff == sqlite3.SQLITE_CONSTRAINT {
		return fmt.Errorf("%w: %w", ErrConstraint, err)
	}
	return err
}

// invalid reports a batch or argument that fails the Go validation; like a constraint error it is permanent.
func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrConstraint, fmt.Sprintf(format, args...))
}

//go:embed migrations/*.sql
var migrations embed.FS

// Store is the archive: one writer connection and a pool of readers (CON: exactly one connection writes archive.db).
type Store struct {
	write *sql.DB
	read  *sql.DB
	path  string
}

type Chat struct{ JID, Name, Type, ParentJID string }

// Participant is the directory row of a JID: the latest push name seen on a message. UpdatedAt is the receive time of
// the message that carried the name (unix seconds; 0 means now).
type Participant struct {
	JID, PushName string
	UpdatedAt     int64
}
type IDMapping struct {
	LID, PN, Source string
	FirstSeen       time.Time
}

// Message is a row of messages. Kind is one of MessageKinds ("" means text). Details is JSON validated per kind
// (ValidateDetails; "" means {}). Mentions are JIDs as received.
type Message struct {
	ChatJID, ID, SenderJID, PushName, Kind, Text, Caption, QuotedID, QuotedSender, Source, Details string
	Mentions                                                                                       []string
	Timestamp, ReceivedAt                                                                          int64
	FromMe, Forwarded, ViewOnce                                                                    bool
	ForwardScore, EphemeralSecs                                                                    int
	RawProto                                                                                       []byte
}

// MessageEvent is a row of message_events. SourceID is the event's own message ID (R11), "" for undecryptable. Cause
// is one of EventCauses. Source "" means live.
type MessageEvent struct {
	ChatJID, TargetID, Kind, EventID, SourceID, Cause, SenderJID, Source, NewText, Value string
	Timestamp, ReceivedAt                                                                int64
	RawProto                                                                             []byte
}
type ChatEvent struct {
	ChatJID, Kind, EventID, SenderJID, Value string
	Timestamp, ReceivedAt                    int64
	RawProto                                 []byte
}

// Media is a row of the media queue. RawProto is not a column: ClaimMedia, NextExpiredMedia and MediaByID fill it from
// messages.raw_proto (R4) and Apply ignores it. SourceRank 0 on insert means "by source": 1 for live, 2 otherwise.
// Thumb is the last column (R5) and is read only by MediaThumb.
type Media struct {
	ChatJID, MessageID, SenderJID, Source, Type, MIME, Filename, Path, SHA256, State, Reason, ErrorClass, RetryDirectPath string
	DeclaredSize, UpdatedAt, MessageTimestamp, EnqueuedAt, NextAttemptAt, DoneAt                                          int64
	SourceRank, DurationS, Width, Height, Pages, HTTPStatus, Attempts                                                     int
	FromMe                                                                                                                bool
	Thumb, RawProto                                                                                                       []byte
}
type ChatStat struct {
	CanonicalJID, Name                                string
	MessageCount                                      int
	Oldest, Newest                                    int64
	MediaFailed, MediaExpired, MediaPending, OpenGaps int
}
type SyncCoverage struct {
	SyncType                       string
	ReceivedChunks, ImportedChunks int
	LastProgress                   int
	LastChunkTime, Oldest, Newest  int64
	Complete                       bool
}
type Coverage struct {
	Chunks, LastProgress int
	Complete             bool // a chunk of the latest FULL (else INITIAL_BOOTSTRAP) sync reported progress 100
	LastChunkTime        int64
	OldestByChat         map[string]int64
	Syncs                []SyncCoverage
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
	if err = checkSchema(write); err != nil { // before anything changes the file
		_ = write.Close()
		return nil, err
	}
	if _, err = write.Exec("PRAGMA journal_mode=WAL"); err != nil {
		_ = write.Close()
		return nil, fmt.Errorf("enable WAL: %w", err)
	}
	if err = migrate(write); err != nil {
		_ = write.Close()
		return nil, err
	}
	if err = ensureV1Columns(write); err != nil {
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

// ensureV1Columns upgrades archives created by an earlier V1 build without changing their schema version. V1 is not
// yet released, and its migration remains version 1; fresh archives already contain these columns.
func ensureV1Columns(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(chat_rules)`)
	if err != nil {
		return fmt.Errorf("inspect chat rules schema: %w", err)
	}
	found := false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, typ string
		var defaultValue any
		if err = rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return fmt.Errorf("inspect chat rules column: %w", err)
		}
		found = found || name == "inherited_from"
	}
	if err = rows.Close(); err != nil {
		return fmt.Errorf("inspect chat rules schema: %w", err)
	}
	if !found {
		if _, err = db.Exec(`ALTER TABLE chat_rules ADD COLUMN inherited_from TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("upgrade chat rules inheritance: %w", err)
		}
	}
	return ensureV1IndexShapes(db)
}

// ensureV1IndexShapes replaces two pre-release V1 indexes in place. Reusing the existing b-trees keeps fresh archive
// size and index count stable while making Logging's bounded order and media aggregation index-only.
func ensureV1IndexShapes(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin V1 index check: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, spec := range []struct{ name, marker, create string }{
		{"message_events_gaps", "received_at DESC", `CREATE INDEX message_events_gaps ON message_events(received_at DESC,seq DESC,chat_jid,target_id,cause) WHERE kind='undecryptable'`},
		{"media_pick", "error_class", `CREATE INDEX media_pick ON media(state,source_rank,message_ts DESC,error_class,reason)`},
	} {
		var definition string
		if err = tx.QueryRow(`SELECT COALESCE(sql,'') FROM sqlite_master WHERE type='index' AND name=?`, spec.name).Scan(&definition); err != nil {
			return fmt.Errorf("inspect %s index: %w", spec.name, err)
		}
		if strings.Contains(definition, spec.marker) {
			continue
		}
		if _, err = tx.Exec(`DROP INDEX ` + spec.name); err != nil {
			return fmt.Errorf("replace %s index: %w", spec.name, err)
		}
		if _, err = tx.Exec(spec.create); err != nil {
			return fmt.Errorf("create %s index: %w", spec.name, err)
		}
	}
	return tx.Commit()
}

// checkSchema refuses an archive from before V1 (R16). Such an archive lists version 1 in schema_version, so the new
// 0001 would be skipped silently; it has no archive_meta, which every V1 archive has. An empty schema_version (a
// crash between creating the table and applying 0001) is a fresh archive.
func checkSchema(db *sql.DB) error {
	has := func(table string) (bool, error) {
		var n int
		err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n)
		if err != nil {
			return false, fmt.Errorf("read archive schema: %w", err)
		}
		return n > 0, nil
	}
	hasVersion, err := has("schema_version")
	if err != nil || !hasVersion {
		return err
	}
	var versions int
	if err = db.QueryRow(`SELECT COUNT(*) FROM schema_version`).Scan(&versions); err != nil {
		return fmt.Errorf("read archive schema: %w", err)
	}
	if versions == 0 {
		return nil
	}
	hasMeta, err := has("archive_meta")
	if err != nil {
		return err
	}
	if !hasMeta {
		return ErrOldArchive
	}
	var schema string
	if err = db.QueryRow(`SELECT COALESCE((SELECT value FROM archive_meta WHERE key='schema'),'')`).Scan(&schema); err != nil {
		return fmt.Errorf("read archive schema: %w", err)
	}
	if schema != schemaV1 {
		return fmt.Errorf("archive schema is %q, this build reads %q", schema, schemaV1)
	}
	return nil
}

// schemaV1 is the value of archive_meta('schema') that 0001_v1.sql writes.
const schemaV1 = "v1"

// OpenReadOnly opens an existing archive for reading only: no migration, no pragma that writes, no directory
// created, and every write method fails. While the service has the archive open (its -wal exists) the reader joins
// it normally. Without -wal and -shm nobody has the archive open, and a plain read-only open would create both files
// and leave them behind, so the file is read as is (immutable).
func OpenReadOnly(path string) (*Store, error) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("no archive at %s: run archiver first", path)
		}
		return nil, fmt.Errorf("stat archive: %w", err)
	}
	dsn := "file:" + filepath.ToSlash(path) + "?mode=ro&_pragma=busy_timeout(5000)"
	if !exists(path+"-wal") && !exists(path+"-shm") {
		dsn += "&immutable=1"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open archive reader: %w", err)
	}
	if err = checkSchema(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{write: db, read: db, path: path}, nil
}

func exists(path string) bool { _, err := os.Stat(path); return err == nil }

func ensureParent(path string) error { return mkdirAll(filepath.Dir(path)) }

func (s *Store) Close() error {
	rerr := s.read.Close()
	if s.write == s.read { // read-only store
		return rerr
	}
	werr := s.write.Close()
	if werr != nil {
		return werr
	}
	return rerr
}
func (s *Store) Path() string { return s.path }

// insert runs one write that may be a no-op (ON CONFLICT DO NOTHING) and reports whether it inserted a row.
func (s *Store) insert(ctx context.Context, query string, args ...any) (bool, error) {
	result, err := s.write.ExecContext(ctx, query, args...)
	if err != nil {
		return false, classify(err)
	}
	n, err := result.RowsAffected()
	return n > 0, err
}

// chatForms selects every JID form of chat ?1 from chat_canonical (R8): the chat itself and every alias that shares its
// canonical JID. The canonical is one primary-key probe and the aliases one range of chat_canonical_by, so the cost does
// not depend on the size of messages or chats. Every read of "the chat under any of its forms" uses it.
const chatForms = `SELECT ?1 UNION SELECT alias FROM chat_canonical WHERE canonical=COALESCE((SELECT canonical FROM chat_canonical WHERE alias=?1),?1)`

// dedupeQuery reports whether message ?2 already exists under any alias of chat ?1: it asks chat_canonical for the
// chat's canonical JID, then its aliases, and probes messages by its (chat_jid, id) unique index. A chat with no
// canonical row yet (its first message) is its own canonical.
const dedupeQuery = `SELECT EXISTS(SELECT 1 FROM chat_canonical a JOIN messages m ON m.chat_jid=a.alias
 WHERE a.canonical=COALESCE((SELECT canonical FROM chat_canonical WHERE alias=?1),?1) AND m.id=?2)`

// aliasesQuery lists the forms of chat ?1 other than itself: chatForms (the canonical group of ?1 in chat_canonical, two
// index probes) minus ?1, sorted so the result is deterministic.
const aliasesQuery = chatForms + ` EXCEPT SELECT ?1 ORDER BY 1`

// Aliases lists the JID forms that stand for the same chat as jid, as the ingest filter and the rule screens need them:
// every other member of its canonical group in chat_canonical (R8), sorted. For a person with two LIDs mapped to one
// phone number, each LID lists the other LID and the phone number; this is the set ChatList takes MIN(logging) over,
// so a rule on one LID stops them all (I2, Hamza, 26 Oct 2026). A chat the archive has not stored has no forms.
func (s *Store) Aliases(ctx context.Context, jid string) ([]string, error) {
	rows, err := s.read.QueryContext(ctx, aliasesQuery, jid)
	if err != nil {
		return nil, fmt.Errorf("query aliases of %s: %w", jid, err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var v string
		if err = rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("scan aliases of %s: %w", jid, err)
		}
		out = append(out, v)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("read aliases of %s: %w", jid, err)
	}
	return out, nil
}

// Canonical is the canonical JID of jid (R8): the smallest LID of its id_mappings component, or jid itself when it has
// no mapping or the archive has not stored it yet.
func (s *Store) Canonical(ctx context.Context, jid string) (string, error) {
	var canonical string
	err := s.read.QueryRowContext(ctx, `SELECT COALESCE((SELECT canonical FROM chat_canonical WHERE alias=?1),?1)`, jid).Scan(&canonical)
	if err != nil {
		return "", fmt.Errorf("canonical of %s: %w", jid, err)
	}
	return canonical, nil
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
