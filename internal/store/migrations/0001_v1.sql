-- V1 schema (docs/v1/schema.md section 3), one squashed migration (ADR-35): there is no upgrade path from the schema
-- before V1. Store.Open refuses an archive that has schema_version rows but no archive_meta (R16).
PRAGMA foreign_keys = ON;
CREATE TABLE IF NOT EXISTS schema_version (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL);
CREATE TABLE archive_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
INSERT INTO archive_meta(key, value) VALUES ('schema', 'v1');

-- Mutable directory tables (upserted by the writer).
CREATE TABLE chats (                          -- unchanged
  jid TEXT PRIMARY KEY, name TEXT NOT NULL DEFAULT '', type TEXT NOT NULL DEFAULT '',
  parent_jid TEXT NOT NULL DEFAULT '');
CREATE TABLE participants (
  jid TEXT PRIMARY KEY,
  push_name TEXT NOT NULL DEFAULT '',         -- latest push name seen on a message
  contact_name TEXT NOT NULL DEFAULT '',      -- from the whatsmeow contact store (task e)
  updated_at INTEGER NOT NULL DEFAULT 0);

-- Raw, insert-only (CON-2).
CREATE TABLE id_mappings (                    -- unchanged
  lid TEXT NOT NULL, pn TEXT NOT NULL, source TEXT NOT NULL DEFAULT '',
  first_seen INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (lid, pn));
CREATE INDEX id_mappings_pn ON id_mappings(pn, lid);

CREATE TABLE messages (
  seq            INTEGER PRIMARY KEY,         -- stable insert order; FTS rowid (R1)
  chat_jid       TEXT NOT NULL,
  id             TEXT NOT NULL,
  sender_jid     TEXT NOT NULL DEFAULT '',
  from_me        INTEGER NOT NULL DEFAULT 0,
  push_name      TEXT NOT NULL DEFAULT '',    -- as on this message
  kind           TEXT NOT NULL,               -- text image video audio voice document sticker gif location
                                              -- live_location contact contacts poll event system unknown
  text           TEXT NOT NULL DEFAULT '',    -- body: conversation/extended text, poll question, event name
  caption        TEXT NOT NULL DEFAULT '',    -- media caption
  quoted_id      TEXT NOT NULL DEFAULT '',
  quoted_sender  TEXT NOT NULL DEFAULT '',
  mentions       TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(mentions)),  -- JIDs as received
  forwarded      INTEGER NOT NULL DEFAULT 0,
  forward_score  INTEGER NOT NULL DEFAULT 0,
  view_once      INTEGER NOT NULL DEFAULT 0,
  ephemeral_secs INTEGER NOT NULL DEFAULT 0,
  details        TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(details)),   -- per kind, section 4.2
  source         TEXT NOT NULL CHECK (source IN ('live','history','replay')),
  ts             INTEGER NOT NULL,
  received_at    INTEGER NOT NULL,
  raw_proto      BLOB NOT NULL,               -- last column (R5)
  UNIQUE (chat_jid, id));
CREATE INDEX messages_chat_ts   ON messages(chat_jid, ts, id);
CREATE INDEX messages_chat_kind ON messages(chat_jid, kind, ts);
CREATE INDEX messages_global_ts ON messages(ts DESC, seq DESC, received_at);
CREATE INDEX messages_unknown   ON messages(seq) WHERE kind = 'unknown';

CREATE TABLE message_events (
  seq         INTEGER PRIMARY KEY,
  chat_jid    TEXT NOT NULL,
  target_id   TEXT NOT NULL,                  -- the message the event is about ('' when none)
  kind        TEXT NOT NULL,                  -- edit revoke protocol reaction pin poll_vote undecryptable
                                              -- secret_encrypted stub
  event_id    TEXT NOT NULL,
  source_id   TEXT NOT NULL DEFAULT '',       -- the event's own message ID (R11)
  cause       TEXT NOT NULL DEFAULT '',       -- undecryptable: no_sender_key untrusted_identity (untrusted_identity never produced; see W2(c))
                                              -- view_once_unavailable other
  sender_jid  TEXT NOT NULL DEFAULT '',
  source      TEXT NOT NULL DEFAULT 'live' CHECK (source IN ('live','history','replay')),
  ts          INTEGER NOT NULL DEFAULT 0,
  received_at INTEGER NOT NULL DEFAULT 0,
  new_text    TEXT NOT NULL DEFAULT '',
  value       TEXT NOT NULL DEFAULT '',
  raw_proto   BLOB NOT NULL DEFAULT X'',
  UNIQUE (chat_jid, target_id, kind, event_id));
CREATE INDEX message_events_source ON message_events(chat_jid, source_id) WHERE source_id <> '';
CREATE INDEX message_events_gaps   ON message_events(chat_jid, target_id) WHERE kind = 'undecryptable';

CREATE TABLE chat_events (                    -- unchanged
  chat_jid TEXT NOT NULL, kind TEXT NOT NULL, event_id TEXT NOT NULL,
  sender_jid TEXT NOT NULL DEFAULT '', ts INTEGER NOT NULL DEFAULT 0,
  received_at INTEGER NOT NULL DEFAULT 0, value TEXT NOT NULL DEFAULT '',
  raw_proto BLOB NOT NULL DEFAULT X'', PRIMARY KEY (chat_jid, kind, event_id));

CREATE TABLE history_sync_chunks (            -- ledger; R10
  id TEXT NOT NULL,                           -- content hash of the chunk, as today
  stage TEXT NOT NULL CHECK (stage IN ('received','imported')),
  spool_id INTEGER NOT NULL DEFAULT 0,
  sync_type TEXT NOT NULL DEFAULT '', chunk_order INTEGER NOT NULL DEFAULT 0,
  progress INTEGER NOT NULL DEFAULT 0, conversation_count INTEGER NOT NULL DEFAULT 0,
  message_count INTEGER NOT NULL DEFAULT 0, oldest_ts INTEGER NOT NULL DEFAULT 0,
  newest_ts INTEGER NOT NULL DEFAULT 0, received_at INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (id, stage));

-- Media queue (mutable).
CREATE TABLE media (
  chat_jid TEXT NOT NULL, message_id TEXT NOT NULL,
  sender_jid TEXT NOT NULL DEFAULT '', from_me INTEGER NOT NULL DEFAULT 0,
  source TEXT NOT NULL DEFAULT 'live' CHECK (source IN ('live','history','replay')),
  source_rank INTEGER NOT NULL,               -- 0 asked for by the viewer, 1 live, 2 history or replay
  type TEXT NOT NULL,                         -- image video gif audio voice document sticker (= messages.kind)
  mime TEXT NOT NULL DEFAULT '', declared_size INTEGER NOT NULL DEFAULT 0,
  filename TEXT NOT NULL DEFAULT '', duration_s INTEGER NOT NULL DEFAULT 0,
  width INTEGER NOT NULL DEFAULT 0, height INTEGER NOT NULL DEFAULT 0, pages INTEGER NOT NULL DEFAULT 0,
  message_ts INTEGER NOT NULL DEFAULT 0,
  enqueued_at INTEGER NOT NULL DEFAULT 0,     -- SC-3 start
  state TEXT NOT NULL CHECK (state IN
    ('pending','downloading','done','skipped','expired','retry_requested','failed')),
  reason TEXT NOT NULL DEFAULT '',            -- full, redacted (W4.1)
  error_class TEXT NOT NULL DEFAULT '' CHECK (error_class IN ('','network','expired','gone','key','other')),
  http_status INTEGER NOT NULL DEFAULT 0,
  attempts INTEGER NOT NULL DEFAULT 0, next_attempt_at INTEGER NOT NULL DEFAULT 0,
  retry_direct_path TEXT NOT NULL DEFAULT '',
  path TEXT NOT NULL DEFAULT '', sha256 TEXT NOT NULL DEFAULT '',
  updated_at INTEGER NOT NULL DEFAULT 0, done_at INTEGER NOT NULL DEFAULT 0,  -- SC-3 end
  thumb BLOB NOT NULL DEFAULT X'',            -- last column (R5, R19)
  PRIMARY KEY (chat_jid, message_id));
CREATE INDEX media_due  ON media(state, next_attempt_at);
CREATE INDEX media_pick ON media(state, source_rank, message_ts DESC);
CREATE INDEX media_live_enqueued ON media(enqueued_at) WHERE source = 'live';

-- Derived (rebuildable by RebuildDerived; maintained by the writer in the same transaction).
CREATE TABLE chat_canonical (alias TEXT PRIMARY KEY, canonical TEXT NOT NULL) WITHOUT ROWID;
CREATE INDEX chat_canonical_by ON chat_canonical(canonical, alias);
CREATE TABLE chat_stats (
  canonical TEXT PRIMARY KEY,
  name TEXT NOT NULL DEFAULT '',              -- chat name, else contact/push name, else ''
  type TEXT NOT NULL DEFAULT '',
  messages INTEGER NOT NULL DEFAULT 0,        -- distinct ids over all aliases
  last_ts INTEGER NOT NULL DEFAULT 0, oldest_ts INTEGER NOT NULL DEFAULT 0,
  oldest_history_ts INTEGER,                  -- NULL: no history message
  media_done INTEGER NOT NULL DEFAULT 0, media_failed INTEGER NOT NULL DEFAULT 0,
  media_expired INTEGER NOT NULL DEFAULT 0,   -- expired + retry_requested
  media_pending INTEGER NOT NULL DEFAULT 0,   -- pending + downloading
  media_bytes INTEGER NOT NULL DEFAULT 0,     -- declared_size of done rows (FR-25)
  gaps_open INTEGER NOT NULL DEFAULT 0) WITHOUT ROWID;
CREATE INDEX chat_stats_recent ON chat_stats(last_ts DESC, canonical);
-- rowid = messages.seq (text + caption + kind strings) or -message_events.seq (edit text). R13, R18.
CREATE VIRTUAL TABLE search_fts USING fts5(body, content='', contentless_delete=1, tokenize='unicode61 remove_diacritics 0');
CREATE VIRTUAL TABLE search_tri USING fts5(body, content='', contentless_delete=1, tokenize='trigram');  -- spike 8

-- Aliases ga/gm/gs are distinct on purpose: the status report's query plan test counts scans of "messages m".
CREATE VIEW open_gaps AS
SELECT e.*, c.canonical AS canonical_jid,
  EXISTS (SELECT 1 FROM connection_periods p
          WHERE p.ended_at IS NOT NULL
            AND e.received_at BETWEEN p.ended_at AND p.ended_at + 120) AS post_outage
FROM message_events e JOIN chat_canonical c ON c.alias = e.chat_jid
WHERE e.kind = 'undecryptable'
  AND NOT EXISTS (SELECT 1 FROM chat_canonical ga JOIN messages gm ON gm.chat_jid = ga.alias
                  WHERE ga.canonical = c.canonical AND gm.id = e.target_id)
  AND NOT EXISTS (SELECT 1 FROM chat_canonical ga JOIN message_events gs ON gs.chat_jid = ga.alias
                  WHERE ga.canonical = c.canonical AND gs.source_id = e.target_id
                    AND gs.source_id <> '' AND gs.kind <> 'undecryptable');  -- <> '' lets the partial index apply

-- Control tables (mutable).
CREATE TABLE connection_periods (
  id INTEGER PRIMARY KEY,
  state TEXT NOT NULL,                        -- degraded reconnecting disconnected logged_out client_outdated
                                              -- temp_banned stream_replaced not_running
  started_at INTEGER NOT NULL, ended_at INTEGER,          -- NULL while open
  cause_class TEXT NOT NULL DEFAULT '',       -- dns tcp tls ws keepalive server logout other not_running
  error_count INTEGER NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',        -- logsafe-redacted, never a URL
  offline_preview_msgs INTEGER, offline_completed_count INTEGER);
CREATE UNIQUE INDEX connection_periods_open ON connection_periods((ended_at IS NULL)) WHERE ended_at IS NULL;
CREATE INDEX connection_periods_time ON connection_periods(started_at);

CREATE TABLE alerts (
  id INTEGER PRIMARY KEY,
  key TEXT NOT NULL,                          -- rule + subject, e.g. disconnected_after
  kind TEXT NOT NULL CHECK (kind IN ('raised','recovered','test')),
  title TEXT NOT NULL, body TEXT NOT NULL DEFAULT '',
  raised_at INTEGER NOT NULL,
  state TEXT NOT NULL CHECK (state IN ('queued','sent','skipped')),  -- skipped: no sink configured
  attempts INTEGER NOT NULL DEFAULT 0, next_attempt_at INTEGER NOT NULL DEFAULT 0,
  sent_at INTEGER, last_error TEXT NOT NULL DEFAULT '');
CREATE INDEX alerts_due ON alerts(state, next_attempt_at);
CREATE INDEX alerts_key ON alerts(key, raised_at);

CREATE TABLE history_spool (
  id INTEGER PRIMARY KEY,
  chunk_id TEXT NOT NULL UNIQUE,              -- = history_sync_chunks.id; a resent chunk is a no-op
  sync_type TEXT NOT NULL DEFAULT '', chunk_order INTEGER NOT NULL DEFAULT 0,
  progress INTEGER NOT NULL DEFAULT 0,
  meta TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(meta)),  -- notification fields without keys (R9)
  size INTEGER NOT NULL DEFAULT 0,
  received_at INTEGER NOT NULL, imported_at INTEGER,          -- NULL: not imported yet
  attempts INTEGER NOT NULL DEFAULT 0, error TEXT NOT NULL DEFAULT '',
  blob_format TEXT NOT NULL CHECK (blob_format IN ('zlib','raw')),
  blob BLOB NOT NULL);                        -- decrypted bytes; last column (R5); retention: Q1
CREATE INDEX history_spool_todo ON history_spool(id) WHERE imported_at IS NULL;

-- Kept unchanged from 0002_settings.sql.
CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at INTEGER NOT NULL);
CREATE TABLE chat_rules (jid TEXT PRIMARY KEY, logging INTEGER NOT NULL DEFAULT 1,
  media TEXT NOT NULL DEFAULT '', inherited_from TEXT NOT NULL DEFAULT '', updated_at INTEGER NOT NULL);
CREATE TABLE chat_seen (jid TEXT PRIMARY KEY, first_seen INTEGER NOT NULL,
  acknowledged INTEGER NOT NULL DEFAULT 0);
