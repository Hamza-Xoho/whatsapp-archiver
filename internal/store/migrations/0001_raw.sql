PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS schema_version (
  version INTEGER PRIMARY KEY,
  applied_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS chats (
  jid TEXT PRIMARY KEY,
  name TEXT NOT NULL DEFAULT '',
  type TEXT NOT NULL DEFAULT '',
  parent_jid TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS participants (
  jid TEXT PRIMARY KEY,
  display_name TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS id_mappings (
  lid TEXT NOT NULL,
  pn TEXT NOT NULL,
  source TEXT NOT NULL DEFAULT '',
  first_seen INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (lid, pn)
);
CREATE INDEX IF NOT EXISTS id_mappings_pn ON id_mappings(pn, lid);
CREATE TABLE IF NOT EXISTS messages (
  chat_jid TEXT NOT NULL,
  id TEXT NOT NULL,
  sender_jid TEXT NOT NULL DEFAULT '',
  ts INTEGER NOT NULL,
  received_at INTEGER NOT NULL,
  source TEXT NOT NULL CHECK(source IN ('live','history','replay')),
  text TEXT NOT NULL DEFAULT '',
  quoted_id TEXT NOT NULL DEFAULT '',
  forwarded INTEGER NOT NULL DEFAULT 0,
  media_type TEXT NOT NULL DEFAULT '',
  raw_proto BLOB NOT NULL,
  PRIMARY KEY (chat_jid, id)
);
CREATE TABLE IF NOT EXISTS message_events (
  chat_jid TEXT NOT NULL,
  target_id TEXT NOT NULL,
  kind TEXT NOT NULL,
  event_id TEXT NOT NULL,
  sender_jid TEXT NOT NULL DEFAULT '',
  ts INTEGER NOT NULL DEFAULT 0,
  received_at INTEGER NOT NULL DEFAULT 0,
  new_text TEXT NOT NULL DEFAULT '',
  value TEXT NOT NULL DEFAULT '',
  raw_proto BLOB NOT NULL DEFAULT X'',
  PRIMARY KEY (chat_jid, target_id, kind, event_id)
);
CREATE TABLE IF NOT EXISTS chat_events (
  chat_jid TEXT NOT NULL,
  kind TEXT NOT NULL,
  event_id TEXT NOT NULL,
  sender_jid TEXT NOT NULL DEFAULT '',
  ts INTEGER NOT NULL DEFAULT 0,
  received_at INTEGER NOT NULL DEFAULT 0,
  value TEXT NOT NULL DEFAULT '',
  raw_proto BLOB NOT NULL DEFAULT X'',
  PRIMARY KEY (chat_jid, kind, event_id)
);
CREATE TABLE IF NOT EXISTS history_sync_chunks (
  id TEXT PRIMARY KEY,
  sync_type TEXT NOT NULL DEFAULT '',
  chunk_order INTEGER NOT NULL DEFAULT 0,
  progress INTEGER NOT NULL DEFAULT 0,
  conversation_count INTEGER NOT NULL DEFAULT 0,
  message_count INTEGER NOT NULL DEFAULT 0,
  oldest_ts INTEGER NOT NULL DEFAULT 0,
  newest_ts INTEGER NOT NULL DEFAULT 0,
  received_at INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS media (
  chat_jid TEXT NOT NULL,
  message_id TEXT NOT NULL,
  sender_jid TEXT NOT NULL DEFAULT '',
  source TEXT NOT NULL DEFAULT 'live',
  type TEXT NOT NULL DEFAULT '',
  mime TEXT NOT NULL DEFAULT '',
  declared_size INTEGER NOT NULL DEFAULT 0,
  raw_proto BLOB NOT NULL DEFAULT X'',
  path TEXT NOT NULL DEFAULT '',
  sha256 TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL,
  reason TEXT NOT NULL DEFAULT '',
  attempts INTEGER NOT NULL DEFAULT 0,
  retry_direct_path TEXT NOT NULL DEFAULT '',
  updated_at INTEGER NOT NULL DEFAULT 0,
  message_ts INTEGER NOT NULL DEFAULT 0,
  from_me INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (chat_jid, message_id)
);

CREATE VIEW IF NOT EXISTS chat_aliases AS
SELECT lid AS alias_jid, lid AS canonical_jid FROM id_mappings
UNION SELECT pn, lid FROM id_mappings
UNION SELECT chat_jid, chat_jid FROM messages WHERE chat_jid NOT IN (SELECT lid FROM id_mappings UNION SELECT pn FROM id_mappings)
UNION SELECT jid, jid FROM chats WHERE jid NOT IN (SELECT lid FROM id_mappings UNION SELECT pn FROM id_mappings);

CREATE VIEW IF NOT EXISTS messages_resolved AS
SELECT * FROM (
  SELECT m.*, COALESCE(a.canonical_jid, m.chat_jid) AS canonical_jid,
         ROW_NUMBER() OVER (PARTITION BY COALESCE(a.canonical_jid, m.chat_jid), m.id ORDER BY m.received_at, m.chat_jid) AS duplicate_rank
  FROM messages m LEFT JOIN chat_aliases a ON a.alias_jid = m.chat_jid
) WHERE duplicate_rank = 1;

CREATE VIEW IF NOT EXISTS open_gaps AS
SELECT e.* FROM message_events e
LEFT JOIN chat_aliases ea ON ea.alias_jid=e.chat_jid
WHERE e.kind='undecryptable' AND NOT EXISTS (
  SELECT 1 FROM messages_resolved m
  WHERE m.canonical_jid=COALESCE(ea.canonical_jid,e.chat_jid) AND m.id=e.target_id
);
