package archiveread

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const responseBudget = 28 * 1024

type Result struct {
	SchemaVersion int            `json:"schema_version"`
	Data          any            `json:"data"`
	Scope         map[string]any `json:"scope"`
	ObservedAt    string         `json:"observed_at"`
	Warnings      []string       `json:"warnings"`
	NextCursor    string         `json:"next_cursor"`
	HasMore       bool           `json:"has_more"`
}
type Engine struct {
	configPath string
	initial    Config
	secret     [32]byte
	gate       chan struct{}
}
type snapshot struct {
	tx          *sql.Tx
	family      string
	cfg         Config
	ids         *identities
	fingerprint string
	policyOK    bool
}

func New(configPath string) (*Engine, error) {
	c, e := LoadConfig(configPath)
	if e != nil {
		return nil, e
	}
	x := &Engine{configPath: configPath, initial: c, gate: make(chan struct{}, 1)}
	if _, e = rand.Read(x.secret[:]); e != nil {
		return nil, failure("INTERNAL_ERROR")
	}
	return x, nil
}

func (e *Engine) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case e.gate <- struct{}{}:
		return nil
	default:
		return failure("BUSY")
	}
}
func normalizeError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return failure("QUERY_TOO_BROAD")
		}
		return ctx.Err()
	}
	var ce *codedError
	if errors.As(err, &ce) {
		return err
	}
	var sqliteErr interface{ Code() int }
	if errors.As(err, &sqliteErr) && (sqliteErr.Code()&255 == 5 || sqliteErr.Code()&255 == 6) {
		return failure("BUSY")
	}
	return failure("ARCHIVE_UNAVAILABLE")
}
func regular(path string) (os.FileInfo, error) {
	s, e := os.Lstat(path)
	if e != nil || !s.Mode().IsRegular() {
		return nil, failure("ARCHIVE_UNAVAILABLE")
	}
	return s, nil
}
func (e *Engine) open(ctx context.Context) (*snapshot, func(), error) {
	cfg, err := LoadConfig(e.configPath)
	ok := err == nil
	if !ok {
		cfg = e.initial
		cfg.Policy = Policy{Mode: "selected"}
	}
	info, err := regular(cfg.Archive)
	if err != nil {
		return nil, nil, err
	}
	// Read only the fixed SQLite header before asking SQLite to open anything.
	// A cleanly stopped WAL archive still has WAL header bytes after its siblings
	// disappear. Opening it ordinarily in a writable directory would create them.
	headerFile, err := os.Open(cfg.Archive)
	if err != nil {
		return nil, nil, failure("ARCHIVE_UNAVAILABLE")
	}
	openedInfo, statErr := headerFile.Stat()
	if statErr != nil || !os.SameFile(info, openedInfo) {
		_ = headerFile.Close()
		return nil, nil, failure("ARCHIVE_UNAVAILABLE")
	}
	var header [100]byte
	_, err = io.ReadFull(headerFile, header[:])
	_ = headerFile.Close()
	if err != nil || string(header[:16]) != "SQLite format 3\x00" {
		return nil, nil, failure("ARCHIVE_UNAVAILABLE")
	}
	walMode := header[18] == 2 || header[19] == 2
	for _, suffix := range []string{"-wal", "-shm"} {
		_, statErr := os.Lstat(cfg.Archive + suffix)
		if statErr == nil {
			if _, statErr = regular(cfg.Archive + suffix); statErr != nil {
				return nil, nil, statErr
			}
		} else if walMode || !os.IsNotExist(statErr) {
			return nil, nil, failure("ARCHIVE_UNAVAILABLE")
		}
	}

	u := &url.URL{Scheme: "file", Path: cfg.Archive}
	q := u.Query()
	q.Set("mode", "ro")
	q.Add("_pragma", "query_only(1)")
	q.Add("_pragma", "busy_timeout(250)")
	q.Add("_pragma", "trusted_schema(0)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, nil, failure("ARCHIVE_UNAVAILABLE")
	}
	db.SetMaxOpenConns(1)
	closeDB := func() { _ = db.Close() }
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		closeDB()
		return nil, nil, normalizeError(ctx, err)
	}
	cleanup := func() { _ = tx.Rollback(); closeDB() }
	family, err := recognize(ctx, tx)
	if err != nil {
		cleanup()
		return nil, nil, normalizeError(ctx, err)
	}
	ids, err := loadIdentities(ctx, tx, cfg.Policy)
	if err != nil {
		cleanup()
		return nil, nil, normalizeError(ctx, err)
	}
	current, err := regular(cfg.Archive)
	if err != nil || !os.SameFile(info, current) {
		cleanup()
		return nil, nil, failure("ARCHIVE_UNAVAILABLE")
	}
	// SameFile describes replacement; Sys includes stable platform file identity, not mutable size/time.
	identity := fileIdentity(info)
	return &snapshot{tx: tx, family: family, cfg: cfg, ids: ids, fingerprint: identity, policyOK: ok}, cleanup, nil
}

func recognize(ctx context.Context, tx *sql.Tx) (string, error) {
	rows, err := tx.QueryContext(ctx, "SELECT name,type FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%'")
	if err != nil {
		return "", err
	}
	tables := map[string]bool{}
	for rows.Next() {
		var n, t string
		if err = rows.Scan(&n, &t); err != nil {
			_ = rows.Close()
			return "", err
		}
		tables[n] = t == "table"
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return "", err
	}
	family := "legacy"
	if isTable, exists := tables["archive_meta"]; exists {
		if !isTable {
			return "", failure("UNSUPPORTED_SCHEMA")
		}
		var marker string
		if err = tx.QueryRowContext(ctx, "SELECT value FROM archive_meta WHERE key='schema'").Scan(&marker); err != nil || marker != "v1" {
			return "", failure("UNSUPPORTED_SCHEMA")
		}
		family = "v1"
	}
	required := map[string][]string{
		"schema_version":      {"version", "applied_at"},
		"chats":               {"jid", "name", "type", "parent_jid"},
		"participants":        {"jid"},
		"id_mappings":         {"lid", "pn", "source", "first_seen"},
		"messages":            {"chat_jid", "id", "sender_jid", "ts", "received_at", "source", "text", "quoted_id", "forwarded", "raw_proto"},
		"message_events":      {"chat_jid", "target_id", "kind", "event_id", "sender_jid", "ts", "received_at", "new_text", "value", "raw_proto"},
		"chat_events":         {"chat_jid", "kind", "event_id", "sender_jid", "ts", "received_at", "value", "raw_proto"},
		"history_sync_chunks": {"id", "sync_type", "chunk_order", "progress", "conversation_count", "message_count", "oldest_ts", "newest_ts", "received_at"},
		"media":               {"chat_jid", "message_id", "sender_jid", "source", "type", "mime", "declared_size", "path", "sha256", "state", "reason", "attempts", "retry_direct_path", "updated_at", "message_ts", "from_me", "next_attempt_at"},
	}
	if family == "v1" {
		required["archive_meta"] = []string{"key", "value"}
		required["messages"] = append(required["messages"], "seq", "from_me", "push_name", "kind", "caption", "quoted_sender", "mentions", "forward_score", "view_once", "ephemeral_secs", "details")
		required["message_events"] = append(required["message_events"], "seq", "source_id", "cause", "source")
		required["participants"] = append(required["participants"], "push_name", "contact_name", "updated_at")
		required["history_sync_chunks"] = append(required["history_sync_chunks"], "stage", "spool_id")
		required["media"] = append(required["media"], "source_rank", "filename", "duration_s", "width", "height", "pages", "enqueued_at", "error_class", "http_status", "done_at", "thumb")
	} else {
		required["messages"] = append(required["messages"], "media_type")
		required["participants"] = append(required["participants"], "display_name")
		required["media"] = append(required["media"], "raw_proto")
	}
	for table, columns := range required {
		if !tables[table] {
			return "", failure("UNSUPPORTED_SCHEMA")
		}
		rs, queryErr := tx.QueryContext(ctx, "PRAGMA table_info("+table+")")
		if queryErr != nil {
			return "", queryErr
		}
		seen := map[string]bool{}
		for rs.Next() {
			var cid, notnull, pk int
			var name, typ string
			var def any
			if err = rs.Scan(&cid, &name, &typ, &notnull, &def, &pk); err != nil {
				_ = rs.Close()
				return "", err
			}
			seen[name] = true
		}
		err = rs.Err()
		_ = rs.Close()
		if err != nil {
			return "", err
		}
		for _, col := range columns {
			if !seen[col] {
				return "", failure("UNSUPPORTED_SCHEMA")
			}
		}
	}
	return family, nil
}

func (e *Engine) Call(parent context.Context, tool string, raw json.RawMessage) (result Result, err error) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	if err = e.acquire(ctx); err != nil {
		return result, err
	}
	defer func() { <-e.gate }()
	args, err := parseArgs(tool, raw)
	if err != nil {
		return result, err
	}
	// Optional artifacts deliberately do not invent or inspect a storage layout.
	if tool == "list_artifacts" || tool == "get_artifact" {
		return result, failure("FEATURE_UNAVAILABLE")
	}
	s, closeSnapshot, err := e.open(ctx)
	if err != nil {
		return result, err
	}
	defer closeSnapshot()
	result = Result{SchemaVersion: 1, Scope: map[string]any{"policy_mode": s.cfg.Policy.Mode}, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), Warnings: []string{"Archive evidence does not establish completeness or current connection health."}}
	if s.family == "legacy" {
		result.Warnings = append(result.Warnings, "Legacy structured fields and coverage facts may be unavailable.")
	}
	if !s.policyOK {
		result.Warnings = append(result.Warnings, "Policy is unavailable or invalid; content access is denied.")
	}
	switch tool {
	case "archive_status":
		result.Data = map[string]any{"schema_family": s.family, "features": map[string]bool{"artifacts": false, "text_search": true, "attachment_content_search": false}, "policy_mode": s.cfg.Policy.Mode, "limits": map[string]any{"request_ms": 2000, "busy_timeout_ms": 250, "max_items": 100, "context_max_items": 21, "query_characters": 512, "body_characters": 8000, "snippet_characters": 500, "search_field_characters": 65536}, "coverage": map[string]any{"oldest": nil, "newest": nil, "complete": nil}}
	case "list_chats":
		err = e.listChats(ctx, s, args, &result)
	case "search_messages", "read_conversation", "get_message", "get_context":
		err = e.readMessages(ctx, s, tool, args, &result)
	default:
		err = failure("INVALID_ARGUMENT")
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return Result{}, normalizeError(ctx, err)
	}
	b, marshalErr := json.Marshal(result)
	if marshalErr != nil || len(b) > responseBudget {
		return Result{}, failure("QUERY_TOO_BROAD")
	}
	return result, nil
}
func (e *Engine) Inspect(parent context.Context) (map[string]any, error) {
	r, err := e.Call(parent, "archive_status", json.RawMessage(`{}`))
	if err != nil {
		return nil, err
	}
	d := r.Data.(map[string]any)
	d["policy_valid"] = !strings.Contains(strings.Join(r.Warnings, " "), "Policy is unavailable")
	d["archive_readable"] = true
	d["wal_available"] = true
	return d, nil
}
func (e *Engine) Catalog(parent context.Context) ([]Chat, error) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	if err := e.acquire(ctx); err != nil {
		return nil, err
	}
	defer func() { <-e.gate }()
	s, closeSnapshot, err := e.open(ctx)
	if err != nil {
		return nil, err
	}
	defer closeSnapshot()
	s.cfg.Policy = Policy{Mode: "all"}
	if err = s.ids.setPolicy(s.cfg.Policy); err != nil {
		return nil, normalizeError(ctx, err)
	}
	chats, err := s.chatMetadata(ctx)
	return chats, normalizeError(ctx, err)
}

func fileIdentity(info os.FileInfo) string { return fmt.Sprintf("%v", identityFields(info)) }
