# V1 schema design (W2)

Status: signed off by Hamza, 26 Oct 2026, with the recommended answers to section 7. Code may start (plan W2). Sources: `docs/plans/2026-10-26-v1-plan.md`
(W2, root causes 2, 3 and 5, invariants), `AGENTS.md`, charter CON-2, CON-7, FR-10, ADR-24 and ADR-35, the
current `internal/store/migrations/0001_raw.sql` and `0002_settings.sql`, `internal/store` and `internal/ingest`.

## 1. Goals and constraints

- **One squashed migration.** `0001_raw.sql` and `0002_settings.sql` are replaced by `0001_v1.sql`. There is no
  upgrade path (ADR-35): V1 starts on a fresh data folder. The file stays under 500 lines, like every Go file.
- **CON-2.** `messages`, `message_events`, `chat_events`, `id_mappings` and `history_sync_chunks` are insert-only.
  `TestNoUpdateOrDeleteOnRawTables` keeps guarding them. No new raw table is added; the new tables are control
  or derived tables and are mutable.
- **CON-7.** JIDs are stored exactly as received. LID and phone forms are linked by `id_mappings` and resolved at
  read time through the derived `chat_canonical` table, never by rewriting a stored row.
- **One writer.** Exactly one connection writes `archive.db`. All derived tables are maintained by that writer in
  the same transaction as the raw insert that changes them; `RebuildDerived()` recreates them from raw tables.
- **Keys never stored.** `archive.db` is backed up, so no key material goes into it (existing rule in
  `ingest/normalize.go`: message secrets stripped, `keyMaterial` protocol messages stored without `raw_proto`).
- **Performance targets the schema must allow** (plan, Verification): `StatusReport` under 100 ms at 300k
  messages, chat list p95 under 150 ms, search under 300 ms, an idle media queue with few store calls per minute.

## 2. Rulings (decided here, not questions)

- **R1. Stable row ids.** `messages` and `message_events` get `seq INTEGER PRIMARY KEY`; the old primary keys
  become `UNIQUE`. `VACUUM` may renumber an implicit rowid, so the FTS rowid must be an explicit column.
- **R2. One write transaction per event.** `ingest.Writer` becomes `Apply(ctx, store.Batch)`: chat, participant,
  message or event or chat event, media row and all derived updates in one transaction. Today they are 4 to 5
  separate transactions (`pipeline.go:246-278`), which could not keep `chat_stats` consistent. ID mappings stay a
  separate, earlier transaction because the exclusion filter must see them before deciding.
  `media.Queue.Enqueue` becomes a wake signal only; the row is already inserted by `Apply`.
- **R3. Derived tables are maintained in Go, not by SQL triggers.** The FTS body needs the Go Arabic folder
  (ADR-24), and canonical merges need multi-step logic. A rebuild-equals-incremental test (task d) catches a write
  path that forgets an update.
- **R4. `media.raw_proto` is dropped.** The downloader reads `messages.raw_proto` by `(chat_jid, id)`. This removes
  a full copy of every media proto and keeps the queue rows narrow.
- **R5. Big blobs are the last column** of every table (`raw_proto`, `thumb`, `blob`). SQLite then reads the
  other columns without touching overflow pages, so scans and index lookups stay cheap.
- **R6. CHECK only on closed vocabularies** (`source`, media `state` and `error_class`, alert `kind`/`state`,
  spool `blob_format`, JSON validity). Open-ended vocabularies (`messages.kind`, event `kind` and `cause`,
  connection `state` and `cause_class`) are validated in Go against one list each, so a new value never needs a
  table rebuild of an insert-only table after V1.
- **R7. Times are unix seconds** in every column, as today. Order ties are broken by `id` (viewer) or `seq`.
- **R8. Canonical JID** of a connected component of `id_mappings` is its smallest LID (LID preferred, charter
  section 11); a JID with no mapping is its own canonical. Every JID the archive stores as a chat or sender has a
  `chat_canonical` row, so reads use inner joins. The name stays `chat_canonical` though it also covers senders.
- **R9. History spool stores the decrypted blob, not the raw notification.** The plan's `notification_raw` holds
  the blob's media key, which is key material (`HISTORY_SYNC_NOTIFICATION` is in `keyMaterial`). The spool keeps
  the downloaded, decrypted, still zlib-compressed bytes (no new zstd dependency) plus the notification's
  non-key fields as JSON.
- **R10. History ledger stages.** `history_sync_chunks` gains `stage` (`received`, `imported`) and `spool_id`;
  the key becomes `(id, stage)`. W5 writes the `received` row with the spool row and the `imported` row after the
  import; both are inserts, so the table stays insert-only.
- **R11. Gap closing.** `source_id` is the event's own message ID for every event that came from a message
  (reaction, edit, revoke, pin, poll vote, protocol, secret_encrypted, stub); it is `''` for `undecryptable` (whose
  own ID is `target_id`). A gap closes on a message with `id = target_id` or a non-undecryptable event with
  `source_id = target_id`, in any alias of the chat. This closes `…6F66` (a protocol event, no target, own ID
  stored) and keeps `…FCCF` open (a reaction *targeting* it is not its source).
- **R12. `post_outage` is not stored.** W3's tag for gaps within 2 min after a reconnect is computed at read time
  from `message_events.received_at` and `connection_periods.ended_at`; raw rows stay facts.
- **R13. Edits are searchable.** An edit's `new_text` is indexed with FTS rowid `-message_events.seq`; a positive
  rowid is `messages.seq`. Revoked messages stay in the archive and in search (see Q2 for the UI).
- **R14. History reactions and poll updates.** Reactions and poll updates embedded in a history `WebMessageInfo`
  are stored as `reaction` / `poll_vote` events with `source='history'` and `source_id` = their own key ID, so the
  viewer shows reactions on history messages too (today they are dropped). Field names come from a prior-art
  lookup in task (c).
- **R15. Poll votes are decrypted in `internal/wa` at receive time** (the poll's message secret lives only in
  `session.db`); the event `value` holds `{"options":["…"]}`. It is a local read, not a send (CON-1 safe), and
  needs a prior-art row. A vote that cannot be decrypted keeps `value=''`.
- **R16. Old archive guard.** `0001_v1.sql` creates `archive_meta('schema','v1')`. An old archive already lists
  version 1 in `schema_version`, so the new file would be skipped silently; `Open` therefore refuses an archive
  without `archive_meta` ("archive predates the V1 schema (ADR-35): use a fresh data folder").
- **R17. Process-down periods are recorded.** A `last_alive_at` setting is written every 60 s; at start-up the
  writer inserts a closed `connection_periods` row (`state='not_running'`) from it to now. Whether it counts as
  outage minutes is Q3.
- **R18. FTS tokenizers (spike 8, `docs/v1/spike8.md`).** The body is folded in Go (FR-10), then indexed in two
  contentless tables with the same rowids: `search_fts` (`unicode61 remove_diacritics 0`, word search, terms of 1-2
  characters) and `search_tri` (`trigram`, substrings and attached prefixes such as ال/و/ب, terms of 3+ characters).
  Both use `contentless_delete=1`. Trigram costs about 2.9x index size and 2x insert time; queries stay under 2 ms
  at 100k rows. `highlight()`/`snippet()` return NULL on contentless tables, so in-chat highlighting is done in Go.
  Emoji are not searchable (accepted).
- **R19. Kept:** `media.thumb` stays as the plan says (the proto's JPEG thumbnail, about 2 to 6 KB per media
  message, duplicated from `raw_proto`) so the viewer never parses protos per bubble.
- **R20. Chat list rows** exist for every canonical seen in `messages`, `chats`, `chat_rules` or `chat_seen`
  (zero counts allowed), as today. `chat_stats` carries the resolved display `name` and `type` so the list reads one
  table; rules and the New flag are joined (a few hundred rows).

## 3. Proposed `0001_v1.sql`

```sql
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
CREATE INDEX messages_received  ON messages(received_at);
CREATE INDEX messages_unknown   ON messages(seq) WHERE kind = 'unknown';

CREATE TABLE message_events (
  seq         INTEGER PRIMARY KEY,
  chat_jid    TEXT NOT NULL,
  target_id   TEXT NOT NULL,                  -- the message the event is about ('' when none)
  kind        TEXT NOT NULL,                  -- edit revoke protocol reaction pin poll_vote undecryptable
                                              -- secret_encrypted stub
  event_id    TEXT NOT NULL,
  source_id   TEXT NOT NULL DEFAULT '',       -- the event's own message ID (R11)
  cause       TEXT NOT NULL DEFAULT '',       -- undecryptable: no_sender_key untrusted_identity
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

CREATE VIEW open_gaps AS
SELECT e.*, c.canonical AS canonical_jid
FROM message_events e JOIN chat_canonical c ON c.alias = e.chat_jid
WHERE e.kind = 'undecryptable'
  AND NOT EXISTS (SELECT 1 FROM chat_canonical a JOIN messages m ON m.chat_jid = a.alias
                  WHERE a.canonical = c.canonical AND m.id = e.target_id)
  AND NOT EXISTS (SELECT 1 FROM chat_canonical a JOIN message_events s ON s.chat_jid = a.alias
                  WHERE a.canonical = c.canonical AND s.source_id = e.target_id
                    AND s.source_id <> '' AND s.kind <> 'undecryptable');  -- <> '' lets the partial index apply

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
  media TEXT NOT NULL DEFAULT '', updated_at INTEGER NOT NULL);
CREATE TABLE chat_seen (jid TEXT PRIMARY KEY, first_seen INTEGER NOT NULL,
  acknowledged INTEGER NOT NULL DEFAULT 0);
```

Dropped: views `chat_aliases` and `messages_resolved` (replaced by `chat_canonical`; their per-query build scanned
the archive, root cause of the 3 s `StatusReport`). `IF NOT EXISTS` is dropped from tables: the file runs once, on an
empty database.

## 4. Per-table notes

### 4.1 Who writes what

| Table | Class | Writer | Notes |
|---|---|---|---|
| `messages`, `message_events`, `chat_events`, `id_mappings`, `history_sync_chunks` | raw, insert-only | `store.Apply` via `ingest.Pipeline.Ingest` (live, history, replay) | `ON CONFLICT DO NOTHING`; guard test unchanged |
| `chats`, `participants` | directory, upsert | `Apply`; `participants.contact_name` also from task (e) | write skipped when nothing changed (`… DO UPDATE … WHERE` differs) |
| `media` | queue, mutable | row inserted by `Apply`; state by `internal/media` through store methods | every state change adjusts `chat_stats` in its transaction |
| `chat_canonical`, `chat_stats`, `search_fts` | derived | the writer, same transaction as the raw change | `RebuildDerived()` |
| `connection_periods` | control | `internal/health` through store methods (W3) | one open row at most (partial unique index) |
| `alerts` | control | health alert outbox (W3) | stored before it counts as fired |
| `history_spool` | control | `wa` history handler writes (W5), importer marks `imported_at` | spool row + `received` ledger row in one transaction, then ack (ADR-37) |
| `settings`, `chat_rules`, `chat_seen` | UI state | web handlers through the writer | a `chat_rules` write ensures `chat_canonical` and `chat_stats` rows |

### 4.2 `messages.details` per kind

Validated in Go (`store/kinds.go`: one kind list, one struct per kind; unknown fields rejected; marshalled
deterministically). `{}` when a kind has nothing extra. Media facts (filename, duration, size, dimensions, pages)
live in `media`, not here.

| kind | `text` / `caption` | `details` |
|---|---|---|
| text | body / – | `{"link":{"url","title","description"}}` when a preview is present |
| image, video, gif, sticker, audio, voice, document | – / caption | `{}`; sticker `{"animated":bool}`; video note `{"ptv":true}` |
| location | – / – | `{"lat","lon","name","address","url"}` |
| live_location | – / caption | `{"lat","lon","accuracy_m","speed","sequence"}` |
| contact | display name / – | `{"display_name","vcard"}` |
| contacts | display name / – | `{"display_name","contacts":[{"display_name","vcard"}]}` |
| poll | question / – | `{"question","options":["…"],"max":n}` |
| event | name / – | `{"name","description","start_ts","end_ts","location":{"name","lat","lon"}\|null,"canceled":bool}` |
| system | notice text if any / – | `{"type":"<proto field name>"}` (call log, scheduled call, …) |
| unknown | – / – | `{"fields":["<top-level proto field names>"]}`; listed on the Logging page via `messages_unknown` |

Kind mapping: `audio` with PTT → `voice`; `video` with gif playback → `gif`; a bodyless message → `unknown`. History
stubs stay `stub` events, never message rows (a ciphertext stub must not take the `(chat_jid, id)` slot). The
key-only rule from `a87a30f` stays. The FTS body is `fold(text + caption + kind strings)`: poll options, location
name and address, contact names, document filename.

### 4.3 Derived-table maintenance inside `Apply`

1. `INSERT OR IGNORE INTO chat_canonical VALUES (x, x)` for the chat and sender JIDs (a mapped JID already has its
   row from the mapping transaction).
2. Directory upserts; if the chat's display name or type changed, update `chat_stats.name/type`.
3. Message: dedupe = `EXISTS` on `(alias, id)` over `chat_canonical_by` (probes the `UNIQUE` index); if new, insert,
   then `chat_stats`: `messages+1`, `last_ts`, `oldest_ts`, `oldest_history_ts`; FTS row if the body is not empty;
   media row (`media_pending+1` unless `skipped`); recompute `gaps_open` only when `message_events_gaps` has a row
   for this id in the canonical.
4. Event: `undecryptable` → recompute that canonical's `gaps_open`; a `source_id` matching a gap → recompute;
   an `edit` with text → FTS row `-seq`.
5. Mapping transaction: walk the component in `id_mappings` (fixed point, at most 4 hops); if any canonical
   changes, rewrite the component's `chat_canonical` rows, recompute `chat_stats` of the merged canonical from raw
   tables and delete the stale row. A merge costs O(rows of that chat) once; history mappings mostly arrive before
   the chat's messages, so most merges touch empty chats.
6. Media state change: read the old state, write, move one count between buckets, add `declared_size` to
   `media_bytes` on `done`.

`gaps_open` is recomputed (not incremented) because the per-chat gap set is tiny and recompute equals rebuild by
construction. `RebuildDerived()` (and `archiver rebuild-derived`, run with the service stopped, one transaction)
deletes `chat_canonical` and `chat_stats`, drops and recreates `search_fts`, then refills them from raw tables,
streaming messages by `seq`. Task (d)'s test compares incremental and rebuilt state: table dumps of the two tables
and the `fts5vocab` instance table of `search_fts`.

### 4.4 Index rationale

| Query | Shape | Index |
|---|---|---|
| `StatusReport` / `archiver status` | read `chat_stats` (O(chats)) + ledger aggregate + `SUM(gaps_open)` | `chat_stats` PK; ledger is hundreds of rows. Well under 100 ms at 300k |
| Chat list (paged, newest first) | `chat_stats` keyset on `(last_ts, canonical)`, filters in SQL, rules/seen joined via `chat_canonical` | `chat_stats_recent`; name filter is a scan of a few hundred rows |
| Viewer page, both directions, jump to date | per alias (usually 1 to 2): `chat_jid=? AND (ts<? OR ts=? AND id<?) ORDER BY ts DESC,id DESC LIMIT n`, `UNION ALL`, merged in Go; duplicate ids across aliases dropped keeping the lowest `seq` | `messages_chat_ts`; each branch is an index range scan, so cost is O(page) not O(chat) |
| Viewer media-type filter | `chat_jid=? AND kind=?` by ts | `messages_chat_kind`; sender and text filters scan within `messages_chat_ts` |
| Page events, media, sender names | `target_id IN (page)` per alias; media by PK; names via `chat_canonical_by` → `participants` | event `UNIQUE`, media PK; fixes the N+1 `Aliases` calls |
| Quoted jump, trace, dedupe | `(alias, id)` | `messages` `UNIQUE (chat_jid, id)` |
| Open gaps | partial index of undecryptable rows; closers via `UNIQUE` and `message_events_source` | `message_events_gaps`, `message_events_source` |
| Media wake timer | `MIN(next_attempt_at) WHERE state='pending'` | `media_due` (one index seek) |
| Media claim / next expired / stale retries / reset | `state=? [AND next_attempt_at<=?] ORDER BY source_rank, message_ts DESC LIMIT 1` | `media_pick` (no sort, no blob read); root cause 5. Written `INDEXED BY media_pick`: without statistics SQLite picks `media_due` plus a temp sort of every due row (checked with `EXPLAIN QUERY PLAN`); a query-plan test pins it |
| SC-3 percentage | live rows with `enqueued_at` in 24 h / 7 d, `done_at - enqueued_at <= 300` | `media_live_enqueued` |
| Usage | per chat from `chat_stats.media_bytes`; per type a narrow scan of `media` (thumb is last); recent messages by `received_at` | `messages_received` |
| Search | `search_fts MATCH fold(q)` → `messages` by `seq` → canonical via `chat_canonical` | FTS; in-chat search filters the hits by alias set |
| Outage timeline, alert outbox, spool importer | by time / due / not imported | `connection_periods_time`, `alerts_due`, `history_spool_todo` |

Write cost: a message touches about 6 to 8 statements in one transaction with one commit, against 4 to 5 commits
today, so `insert_cost_test.go` should improve; indexes on `messages` are 4 (+ the `UNIQUE`).

## 5. Old column → new column

| Old | New |
|---|---|
| `messages` PK `(chat_jid, id)` | `seq INTEGER PRIMARY KEY` + `UNIQUE (chat_jid, id)` |
| `messages.text` (body or caption) | `text` (body) and `caption` (media caption) |
| `messages.media_type` (`''`, image, video, gif, audio, document, sticker) | `kind` (16 values; PTT audio → `voice`, `''` → `text` or other kinds) |
| `messages.forwarded` (score > 0) | `forwarded` (is-forwarded flag or score > 0) + `forward_score` |
| – | `from_me`, `push_name`, `quoted_sender`, `mentions`, `view_once`, `ephemeral_secs`, `details` |
| `message_events` PK | `seq` + `UNIQUE (chat_jid, target_id, kind, event_id)` |
| – | `message_events.source_id`, `cause`, `source` |
| `media.raw_proto` | dropped; read `messages.raw_proto` (R4) |
| `media.source` + `CASE source` order | `source` kept + `source_rank` |
| – | `media.filename, duration_s, width, height, pages, thumb, http_status, error_class, enqueued_at, done_at` |
| `participants.display_name` | `push_name`; + `contact_name`, `updated_at` |
| `history_sync_chunks` PK `id` | PK `(id, stage)` + `spool_id` |
| view `chat_aliases` | table `chat_canonical` |
| view `messages_resolved` | dropped (Go-side dedupe in the viewer; `COUNT(DISTINCT id)` in rebuild) |
| view `open_gaps` | rewritten (R11) |
| `chats`, `id_mappings`, `chat_events`, `settings`, `chat_rules`, `chat_seen`, `schema_version` | unchanged |
| – | new: `archive_meta`, `chat_stats`, `search_fts`, `connection_periods`, `alerts`, `history_spool` |

## 6. Affected packages and the W2 task split

| Task | Package / file | Change |
|---|---|---|
| (b) migration and store | `internal/store/migrations` | `0001_v1.sql` replaces both files |
| (b) | `store/store.go` | split: `store.go` (Open, R16 guard, types), `write.go` (`Apply`, `Batch`), `kinds.go` (kind list, details validation), `media.go` (queue queries on `media_due`/`media_pick`, raw proto join), `ledger.go` (history chunks, spool), `control.go` (periods, alerts) |
| (b) | `store/dump.go`, `rawtables_test.go`, `migrate_test.go` | new columns in the dump; raw list unchanged; old migration tests replaced in their own `test:` commit citing ADR-35 |
| (b) | `internal/ingest/pipeline.go`, `types.go` | `Writer` → `Apply(Batch)`; media enqueue inside the batch; `Result` carries the typed message |
| (b) | `internal/media/queue.go` | `InsertMedia` gone (row comes from `Apply`), `Enqueue` = wake, `RawProto` read by join |
| (c) normaliser | `ingest/normalize.go`, `history.go` | every typed column and kind, `details`, `source_id` on events, history reactions and poll updates (R14), golden fixture per kind; `add-message-kind` skill |
| (c) | `internal/wa` | undecryptable `cause` mapping, poll vote decryption (R15); prior-art rows first |
| (d) derived | `store/derived.go`, `store/fold.go`, `cmd/archiver` | maintenance in `Apply`, `RebuildDerived()`, `archiver rebuild-derived`, FR-10 folder (after spike 8 for the tokenizer) |
| (e) contact names | `internal/wa` (contact store read), `store` participants upsert | `contact_name`; never copies `session.db` |
| (f) status and stats | `store/views.go`, `browse.go`, `usage.go`, `cmd/archiver` status, `internal/web` (dashboard, chats), `internal/view` | `StatusReport`, `ChatList`, `ChatPage`, `ChatName`, `Trace`, `ChatMedia`, `Aliases`, `OpenGapCount`, `Usage` on the new tables; 300k scale test |
| later (W3, W4, W5, W6) | `health`, `media`, `wa/download.go`, `web` | write and read the control tables and new media columns |

Callers today (for the split): `StatusReport` (cmd, web), `ChatList`/`ChatPage`/`ChatName`/`Trace`/`ChatMedia`/
`OpenGapCount`/`HistoryCoverage`/`Usage` (web), `Aliases` (cmd, ingest, web), `Insert*`/`Upsert*`/
`InsertHistoryChunk`/`MarkChatSeen` (ingest), `InsertMedia`/`ClaimMedia`/`UpdateMedia`/`NextExpiredMedia`/
`FailStaleRetries`/`ResetMediaOnStart`/`MediaByID` (media), `DumpRaw` (cmd, ingest tests).

## 7. Decisions (Hamza, 26 Oct 2026)

1. **History blob retention:** clear the blob 7 days after a successful import; keep the row's metadata.
2. **Deleted messages:** the viewer shows a placeholder by default with a "show deleted text" toggle; search
   results include them, marked deleted.
3. **Process-down time:** `not_running` periods count in the ADR-36 "outage minutes per day" figure, shown as their
   own cause.

## 8. Concerns for the implementers

- Every `ON CONFLICT DO NOTHING` insert must report "new" correctly (`RowsAffected`) so derived updates run once.
- The R16 guard must be tested against a copy of an old-schema file, so a V1 binary never runs on the old
  archive on ryzen.
- `search_tri` is part of V1 (spike 8); `RebuildDerived()` drops and recreates both FTS tables.
