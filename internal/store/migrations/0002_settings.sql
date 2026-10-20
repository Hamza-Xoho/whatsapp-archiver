-- Mutable UI state. These are not raw tables (CON-2 covers messages, message_events, chat_events, id_mappings
-- and history_sync_chunks only).
CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  updated_at INTEGER NOT NULL
);
-- media is '' (use the global rules) or a JSON config.MediaRules.
CREATE TABLE IF NOT EXISTS chat_rules (
  jid TEXT PRIMARY KEY,
  logging INTEGER NOT NULL DEFAULT 1,
  media TEXT NOT NULL DEFAULT '',
  updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS chat_seen (
  jid TEXT PRIMARY KEY,
  first_seen INTEGER NOT NULL,
  acknowledged INTEGER NOT NULL DEFAULT 0
);
