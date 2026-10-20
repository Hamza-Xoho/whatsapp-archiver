# Phase 2 Control UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give Hamza a local web dashboard on ryzen to see what the archiver is doing, pair WhatsApp, choose which chats are logged, set media rules, read chats, and check storage and backups, with capture running uninterrupted underneath.

**Architecture:** A new `internal/web` package, served by the same `archiver run` process on `127.0.0.1` and reached through an SSH tunnel. It uses server-rendered `html/template` pages with htmx, plus one Server-Sent Events stream fed by a new in-memory `internal/activity` bus. Settings that change at runtime move from `config.yaml` into new mutable tables in `archive.db`, added by a second migration. The five raw tables stay insert-only, and every write still goes through the store's one writer connection.

**Tech Stack:** Go 1.26 (through `make`), `html/template`, `net/http`, `embed`; htmx 2.0.4 and its SSE extension 2.2.2, vendored as embedded files (no CDN, no JS toolchain); `rsc.io/qr` v0.2.0 (already in `go.sum`) for the pairing QR image; `modernc.org/sqlite`.

**Spec:** the charter `docs/charter/WhatsApp Archiver — Project Charter & Build Plan.md` (v1.2): section 13, phase 2 row; ADR-27 (the trimmed scope); FR-1, FR-2, FR-5, FR-9, FR-18, FR-25, and the UI parts of FR-23/FR-24; section 12 (screens and live views); ADR-19 (Go templates, htmx, SSE). Hamza's phase 2 answers (20 Oct 2026) are below. `AGENTS.md` is the workflow contract.

## Hamza's phase 2 answers (20 Oct 2026)

- **Access:** the UI listens on loopback only and is reached with `ssh -L 8080:localhost:8080 hamza@192.168.0.104`. There is no login in phase 2. Tailscale and login come in phase 4 (FR-21).
- **Reading:** a basic read-only chat viewer is in scope (messages with names, edits, deletions, reactions and media). Full-text and Arabic search stay in phase 3 (FR-10).
- **Destructive actions:** shown, not built. The Backups screen shows how to restore by command, with no restore button. The Storage screen shows usage and projections, with no delete or retention buttons.
- **Chat modes:** "everything, with exclusions" only. Selected-chats mode and the 48-hour pending tray (FR-3) are deferred; new chats are flagged as new.

## Out of scope (deferred, with where it goes)

- Modules, Queue screen, pipeline strips, module toggles and engine settings: phase 1, per ADR-27.
- Selected mode and the pending tray (FR-3).
- Community expand (FR-2): `chats.parent_jid` is mostly empty today, and filling it needs a group-metadata query to WhatsApp that is not yet cleared under CON-1. Communities show as plain groups.
- Search (FR-10), MCP (FR-11), the wizard (FR-16), re-pairing after logout (FR-19), and remote access with login (FR-21).
- Restore from the UI (FR-23) and media retention or deletion (FR-25).

## Global Constraints

- Build and test only through `make check` (`make test` / `make lint` while iterating).
- CON-1: never send anything a contact can see. `internal/web` and `internal/activity` must not import `go.mau.fi/whatsmeow` (the depguard rule `receive-only-client`). Pairing reaches the UI through `internal/wa` as plain strings.
- CON-2: `messages`, `message_events`, `chat_events`, `id_mappings` and `history_sync_chunks` stay insert-only, and `TestNoUpdateOrDeleteOnRawTables` stays green. The new tables (`settings`, `chat_rules`, `chat_seen`) are mutable and are not raw tables.
- CON-7: JIDs are stored as received. A chat rule set on one JID form applies to its aliases through `Store.Aliases`.
- Exactly one connection writes `archive.db`: every UI write goes through `*store.Store` write methods. The UI's reads use the store's read pool.
- NFR-2: logging never waits on the UI. A slow, stalled or disconnected browser must never block `Pipeline.Handle`, the media queue or the health monitor.
- Loopback only: `web.listen` defaults to `127.0.0.1:8080`, and `config.Load` rejects any host that is not `127.0.0.1`, `::1` or `localhost`.
- Every `POST` needs `Origin` (or `Referer`) equal to `http://<the request's Host>`, and every request needs a `Host` that is `localhost:<port>`, `127.0.0.1:<port>` or `[::1]:<port>`. Anything else gets 403, which blocks DNS rebinding and cross-site requests from Hamza's Mac browser.
- The UI shows message text only on the chat viewer and trace pages. The activity feed, logs, SSE payloads and alerts never carry message text, captions or media URLs.
- Vendored assets (`htmx.min.js` 2.0.4, `sse.js` 2.2.2) are embedded and pinned by SHA-256 in a test.
- Screens and copy: plain words, user's side of the screen ("Logging", "Not logging", "New"), no internal terms such as JID on labels. Show the JID only as secondary text.
- `AGENTS.md`: red, then green, then refactor. Commit the failing test as `test: ...` first. During green create `.claude/green.lock`, do not edit tests, and delete the lock after `make check`. Never delete, weaken or skip a test. A test whose required behaviour changes by ruling in this plan (Task 10) is changed in its own `test:` commit, with the reason in the message.
- Mutation specs per behaviour task in `docs/reviews/2026-10-20-phase2/specs-t<N>.json`. Run them with `MUT_REPO=$PWD python3 docs/reviews/2026-10-14-phase0-fixes-mutations/run.py <spec>`. Every mutant must be KILLED.
- Files stay under 500 lines. No `Co-Authored-By` trailer. Each task adds a short `PROGRESS.md` entry under "Phase 2".
- Never run `archiver pair` or `archiver run` against WhatsApp from agents. Tests use fakes at the `wa` boundary. Deploys need Hamza's yes per action.

## Review Focus

- A browser tab left open for hours, or a tunnel that drops mid-stream: the SSE handler must exit when the client goes away, and a full subscriber buffer drops that subscriber's events instead of blocking publishers. Pinned by Task 3 `TestSlowSubscriberNeverBlocksPublish` and Task 4 `TestEventsStreamEndsWhenClientGoes`.
- Turning logging off for a chat seen under its other JID form (phone vs LID): the next event under either form is not stored. Pinned by Task 2 `TestRuleOnPhoneFormExcludesLIDForm`.
- The first start after the upgrade, on the live archive (228k messages, a `config.yaml` with `exclude`): the migration runs once, the exclusions are imported once, and capture starts. Pinned by Task 1 `TestUpgradeFromVersion1KeepsRowsAndAddsSettings` and Task 2 `TestConfigExclusionsImportedOnce`.
- A media URL in a crafted request (`/media?chat=..&id=../../session.db`): only a `done` media row's own file under `<data>/media/` is served. Pinned by Task 7 `TestMediaServesOnlyDoneRowFileInsideMediaDir`.
- A chat with 80k messages opened in the viewer: it pages (newest 200 first, "older" link), never loads the whole chat. Pinned by Task 7 `TestViewerPagesLargeChat`.

---

### Task 1: Versioned migrations and the settings tables

**Files:**
- Modify: `internal/store/store.go` (`Open`, around lines 85-118: the migration step)
- Create: `internal/store/migrate.go`, `internal/store/migrations/0002_settings.sql`
- Test: `internal/store/migrate_test.go`

**Interfaces:**
- Produces: `func (s *Store) SchemaVersions(ctx context.Context) ([]int, error)`. Tables:
  - `settings(key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at INTEGER NOT NULL)`
  - `chat_rules(jid TEXT PRIMARY KEY, logging INTEGER NOT NULL DEFAULT 1, media TEXT NOT NULL DEFAULT '', updated_at INTEGER NOT NULL)`, where `media` is `''` (use the global rules) or a JSON `config.MediaRules`
  - `chat_seen(jid TEXT PRIMARY KEY, first_seen INTEGER NOT NULL, acknowledged INTEGER NOT NULL DEFAULT 0)`

- [ ] **Step 1: Write the failing tests.** `TestFreshArchiveHasVersions1And2` (`SchemaVersions` returns `[1 2]`). `TestUpgradeFromVersion1KeepsRowsAndAddsSettings`: build a v1 archive from `0001_raw.sql` alone with 3 message rows, then `Open` it. Expect `[1 2]`, 3 messages, and the three new tables present. `TestReopenAppliesNothingTwice`: open twice; `schema_version` has 2 rows. `TestMigrationsApplyInFileOrder`: the runner sorts `NNNN_*.sql` names numerically.
- [ ] **Step 2:** `make test`. Expect a compile failure on `SchemaVersions`, then failures on the missing tables.
- [ ] **Step 3: Implement** `migrate(db *sql.DB) error` in `migrate.go`. Read the embedded `migrations/*.sql`, parse the leading number, and apply each version not yet in `schema_version`, inside one transaction per file, inserting its row. `Open` calls it instead of the hard-coded 0001 exec. `OpenReadOnly` stays migration-free.
- [ ] **Step 4:** `make check`. Every existing store test is green, including `TestNoUpdateOrDeleteOnRawTables`.
- [ ] **Step 5:** Mutation specs (skip a version, apply out of order, re-apply an applied file). Commit `test:` and `feat: versioned migrations and settings tables`.

### Task 2: Settings and chat rules that take effect on the next event

**Files:**
- Create: `internal/store/settings.go`, `internal/ingest/rules.go`
- Modify: `internal/ingest/scope.go` (the `Filter` reads a live exclusion set), `internal/media/queue.go` and `internal/media/rules.go` (per-chat rules), `internal/config/config.go` (new `Web` section; the import marker), `cmd/archiver/main.go` (`serve` wiring)
- Test: `internal/store/settings_test.go`, `internal/ingest/rules_test.go`, `internal/media/rules_test.go`, `cmd/archiver/serveopen_test.go`

**Interfaces:**
- Produces (store):
  - `type ChatRule struct{ JID string; Logging bool; Media *config.MediaRules; UpdatedAt int64 }`
  - `func (s *Store) ChatRules(ctx) ([]ChatRule, error)`
  - `func (s *Store) SetChatRules(ctx, rules []ChatRule) error` (one transaction; bulk-safe)
  - `func (s *Store) Setting(ctx, key string) (string, bool, error)` / `func (s *Store) SetSetting(ctx, key, value string) error`
  - `func (s *Store) MarkChatSeen(ctx, jid string, at int64) (isNew bool, err error)` (`INSERT OR IGNORE`)
  - `func (s *Store) AcknowledgeChats(ctx, jids []string) error`
  - `func (s *Store) ImportConfigOnce(ctx, cfg *config.Config) (imported bool, err error)`: writes each `cfg.Exclude` JID as `logging=0`, and `cfg.Media`, alert thresholds and backup `time`/`keep` as settings under the keys below, then sets `settings['import.config.v1']`. A second call does nothing.
- Settings keys (values are JSON): `media.rules` (`config.MediaRules`), `alerts.thresholds` (`{no_events_after, disconnected_after, phone_quiet_after, disk_free_min_gb}`), `backup.schedule` (`{time, keep}`). `data_dir`, `time_zone`, `history`, `wa`, `alerts.ntfy` (it holds a token, and `archive.db` is backed up), `backup.destination` (it needs systemd write access) and `web` stay in `config.yaml`.
- Produces (ingest): `type RuleSet struct` with `func NewRuleSet(rules []store.ChatRule) *RuleSet`, `func (r *RuleSet) Excluded(jid string) bool`, `func (r *RuleSet) Replace(rules []store.ChatRule)` (concurrency-safe via `atomic.Pointer`). `NewFilter(rules *RuleSet, aliases func(string) ([]string, error)) *Filter` replaces the `[]string` parameter. `Filter.Excluded` keeps its alias logic.
- Produces (media): `NewQueue(dir string, global func() config.MediaRules, perChat func(chatJID string) *config.MediaRules, ...)`. `decide` uses the per-chat rules when non-nil, else the global ones.
- Produces (config): `Web struct{ Listen string \`yaml:"listen"\` }`, default `127.0.0.1:8080`, with the loopback check in `Load`.
- Produces (live settings): `func (m *health.Monitor) WithThresholds(f func() config.AlertConfig) *Monitor`. `check()` calls `f` each pass; `Ntfy` always comes from the file config. `backup.Schedule(ctx, cfg, clock, onResult, sched func() config.BackupConfig)`, where `sched` supplies `Time` and `Keep` at each scheduling decision and `Destination` stays from `cfg`.

- [ ] **Step 1: Write the failing tests.**
  - `TestSetChatRulesBulkIsOneTransaction`: ten rules; a failure on the tenth leaves none.
  - `TestConfigExclusionsImportedOnce`: two imports give one rule per JID, and after a `SetChatRules` re-enabling one, a second import doesn't undo it.
  - `TestRuleChangeAppliesToNextEvent` (SC-2, ingest with a real store): event 1 stored, `Replace` with `logging=false`, event 2 for the same chat not stored.
  - `TestRuleOnPhoneFormExcludesLIDForm`.
  - `TestPerChatMediaRuleOverridesGlobal`: a chat with `Download: []` skips an image the global rules download.
  - `TestLoadRejectsNonLoopbackListen` (`0.0.0.0:8080`, `192.168.0.104:8080` rejected; `127.0.0.1:9000`, `localhost:8080`, `[::1]:8080` accepted).
  - `TestMarkChatSeenReportsNewOnce`.
- [ ] **Step 2:** `make test`. The tests fail to compile on the new names; then show each one failing behaviourally against a stub.
- [ ] **Step 3: Implement.** In `serve`: after `store.Open`, `ImportConfigOnce`, then load the rules and settings and build the `RuleSet` and the media rule funcs from the store. The health monitor reads its thresholds from the `alerts.thresholds` setting (fallback: config), and `backup.Schedule` reads `backup.schedule` at each scheduling decision. `Pipeline.Ingest` calls `MarkChatSeen` for each kept chat. Existing callers of `NewFilter` (`replay.go`, tests) pass `NewRuleSet` built from their exclusion list.
- [ ] **Step 4:** `make check`; existing filter, recorder and replay tests are green.
- [ ] **Step 5:** Mutation specs (`Replace` ignored, alias lookup skipped, import re-runs, per-chat rule ignored, loopback check removed). Commit.

### Task 3: Activity bus

**Files:**
- Create: `internal/activity/activity.go`
- Modify: `internal/ingest/pipeline.go`, `internal/ingest/history.go`, `internal/media/queue.go`, `internal/health/health.go`, `internal/backup/backup.go` callers in `cmd/archiver/main.go` (publish)
- Test: `internal/activity/activity_test.go`, plus one publish test per publisher

**Interfaces:**
- Produces:
  - `type Event struct{ At time.Time; Kind string; Chat string; Detail string }`. Allowed `Kind` values: `message`, `event`, `chat_event`, `history_chunk`, `media`, `state`, `backup`, `gap`, `excluded_skip`. `Detail` never holds message text, captions or URLs, only counts, states and types (for example `"image done"`, `"chunk FULL #12 · 412 messages"`).
  - `type Bus struct`; `func NewBus(ring int) *Bus` (ring = 500)
  - `func (b *Bus) Publish(e Event)` (never blocks)
  - `func (b *Bus) Subscribe(buf int) (ch <-chan Event, cancel func())`
  - `func (b *Bus) Recent() []Event`
  - A `nil` `*Bus` is a no-op publisher.
- `Pipeline.WithActivity(*activity.Bus)`, `Queue.WithActivity`, `Monitor.WithActivity` (publishes on state transitions only).

- [ ] **Step 1: Write the failing tests.**
  - `TestSlowSubscriberNeverBlocksPublish`: a subscriber that never reads, buffer 4; 10,000 publishes return within 1 s; the subscriber is dropped, and its channel is closed.
  - `TestRecentKeepsLast500`.
  - `TestNilBusIsNoop`.
  - `TestPipelinePublishesNoText`: a message with text "secret words" publishes an event whose fields don't contain it.
  - `TestMonitorPublishesOnTransitionOnly`.
- [ ] **Step 2:** Run them and see them fail. **Step 3:** Implement with a mutex-guarded ring and per-subscriber buffered channels, sending with `select default`. **Step 4:** `make check`, including goleak in every touched package. **Step 5:** Mutation specs (blocking send, text in `Detail`). Commit.

### Task 4: Web server skeleton

**Files:**
- Create: `internal/web/server.go`, `internal/web/guard.go`, `internal/web/sse.go`, `internal/web/templates/layout.html`, `internal/web/static/htmx.min.js`, `internal/web/static/sse.js`, `internal/web/static/app.css`
- Modify: `cmd/archiver/main.go` (`serve` adds the web server to `loops`)
- Test: `internal/web/server_test.go`, `internal/web/guard_test.go`, `internal/web/assets_test.go`

**Interfaces:**
- Produces:
  - `type Deps struct{ Store *store.Store; Rules *ingest.RuleSet; Health func() health.Snapshot; Activity *activity.Bus; Config *config.Config; Pairer Pairer; Now func() time.Time }`
  - `func New(d Deps) *Server`
  - `func (s *Server) Handler() http.Handler`
  - `func (s *Server) Run(ctx context.Context, listen string) error` (shuts down within 5 s when ctx ends)
  - Routes: `GET /` (dashboard), `GET /events` (SSE), `GET /static/*`. Later tasks add theirs to `routes()` in `server.go`.
- Pairer (implemented in Task 10; a fake until then): `type Pairer interface{ Paired() bool; StartPairing(ctx context.Context) (<-chan PairUpdate, error) }` and `type PairUpdate struct{ QR string; Done bool; Err string }`.

- [ ] **Step 1: Write the failing tests.**
  - `TestRejectsForeignHost` (`Host: evil.example` → 403).
  - `TestPostWithoutMatchingOriginIs403`.
  - `TestPostWithSameOriginPasses`.
  - `TestVendoredAssetsMatchPinnedHashes`: the SHA-256 of `htmx.min.js` 2.0.4 and `sse.js` 2.2.2, recorded in the test from the npm tarballs (`htmx.org@2.0.4/dist/htmx.min.js`, `htmx-ext-sse@2.2.2/sse.js`).
  - `TestEventsStreamSendsPublishedEvent`: an event published reaches an `httptest` SSE client within 2 s (SC-5's 2-second bound).
  - `TestEventsStreamEndsWhenClientGoes`: the handler returns after the client cancels, and goleak stays clean.
  - `TestRunStopsOnContextCancel`.
- [ ] **Step 2:** Run them and see them fail. **Step 3:** Implement; templates are parsed once from `embed.FS`, and the SSE events are named `activity` and `health`. **Step 4:** `make check`; `runService` stops the server with the other loops. **Step 5:** Mutation specs (guard bypass, SSE never exits). Commit.

### Task 5: Shared view helpers (refactor)

**Files:**
- Create: `internal/view/view.go`
- Modify: `cmd/archiver/status.go` (use `internal/view`)
- Test: `internal/view/view_test.go`; the existing `cmd/archiver` status goldens must stay byte-identical

**Interfaces:**
- Produces (moved from `cmd/archiver/status.go` and exported, unchanged behaviour): `func Location(cfg *config.Config) (*time.Location, error)`, `func FormatUnix(loc *time.Location, sec int64) string`, `func HumanSize(n int64) string`, `func DirSize(dir string) (int64, error)`, `func HistoryLine(loc *time.Location, c store.Coverage) string`.

- [ ] **Step 1:** Move the code with its tests into `view_test.go`, unchanged in assertions. **Step 2:** `make check`; the status goldens pass without `-update`. Commit `refactor: shared view helpers for CLI and web`.

### Task 6: Dashboard (health panel, history progress, activity feed)

**Files:**
- Create: `internal/web/dashboard.go`, `internal/web/templates/dashboard.html`
- Test: `internal/web/dashboard_test.go`

**Interfaces:**
- Consumes: `Deps.Health`, `Store.StatusReport`, `view.HistoryLine`, `Activity.Recent`.
- Produces: `GET /` and `GET /partials/health` (re-rendered on each `health` SSE event).

- [ ] **Step 1: Write the failing tests** (they render with a seeded store and a fake health func).
  - `TestDashboardShowsStateVersionAndHistoryLine`: "Connected", the client version and the `history:` text.
  - `TestDashboardWarnsWhenPhoneQuiet`: `PhoneLastActive` older than `phone_quiet_after` shows the inactivity warning text "Open WhatsApp on your phone soon".
  - `TestDashboardLists20RecentEvents`.
  - `TestDashboardCountsLoggingAndNotLoggingChats`.
  - `TestDashboardShowsOpenGapsCount`.
- [ ] **Steps 2-5:** Fail, implement, `make check`, commit.

### Task 7: Chats screen, chat viewer and message trace

**Files:**
- Create: `internal/web/chats.go`, `internal/web/viewer.go`, `internal/web/media.go`, `internal/web/templates/chats.html`, `internal/web/templates/viewer.html`, `internal/web/templates/trace.html`; `internal/store/browse.go`
- Test: `internal/web/chats_test.go`, `internal/web/viewer_test.go`, `internal/web/media_test.go`, `internal/store/browse_test.go`

**Interfaces:**
- Produces (store, read pool only):
  - `type ChatRow struct{ store.ChatStat; DisplayName string; Logging bool; HasMediaRule bool; New bool }`
  - `func (s *Store) ChatList(ctx context.Context, filter string) ([]ChatRow, error)`: `DisplayName` is the group name, else the participant's `display_name` for a direct chat, else the JID. `filter` is a case-insensitive substring match on the name or JID.
  - `type ViewMessage struct{ ID, Sender, SenderName, Text, EditedText, MediaType, MediaState string; TS int64; Deleted bool; Reactions []string; QuotedID string }`
  - `func (s *Store) ChatPage(ctx context.Context, canonicalJID string, beforeTS int64, limit int) ([]ViewMessage, error)`, which reads `messages_resolved` with the newest first.
  - `func (s *Store) Trace(ctx context.Context, chatJID, id string) (Trace, error)`, returning `type Trace struct{ Message store.Message; Events []store.MessageEvent; Media *store.Media; Gap bool }`.
- Produces (web):
  - `GET /chats?q=`
  - `POST /chats/rules` (form: `jid[]`, `logging=on|off`, optional `media=default|text-only|custom` with custom fields); it writes `SetChatRules`, calls `Rules.Replace` with the new full set, and returns the updated rows.
  - `POST /chats/ack` (`jid[]`)
  - `GET /chats/{jid}?before=` (viewer, page size 200)
  - `GET /chats/{jid}/m/{id}` (trace)
  - `GET /media?chat=&id=`

- [ ] **Step 1: Write the failing tests.**
  - `TestChatListUsesParticipantNameForDirectChats`.
  - `TestChatListFilter`.
  - `TestBulkRuleAppliesToAllSelected`: ten chats in one POST (FR-18), and `Rules.Excluded` is true for all ten right after.
  - `TestRuleChangeStopsCaptureEndToEnd` (SC-2): POST logging off, then feed an event through a real `Pipeline` on the same store and `RuleSet`, and confirm no new row.
  - `TestNewChatsFlaggedUntilAcknowledged`.
  - `TestViewerShowsEditedTextAndKeepsOriginal`, `TestViewerMarksDeletedMessage`, `TestViewerListsReactions`.
  - `TestViewerPagesLargeChat`: 1,000 messages; the first page has 200, and the "Older" link carries the 200th message's `ts`.
  - `TestTraceShowsGapForUndecryptable`.
  - `TestMediaServesOnlyDoneRowFileInsideMediaDir`: a `pending` row gives 404, a `done` row whose `path` points outside `<data>/media` gives 404, a traversal in `id` gives 404, and a valid row gives 200 with its MIME type.
- [ ] **Steps 2-5:** Fail, implement, `make check`, mutation specs (the rule write skips `Replace`, `ChatPage` ignores `before`, the media path check is removed), commit.

### Task 8: Settings screen

**Files:**
- Create: `internal/web/settings.go`, `internal/web/templates/settings.html`
- Modify: `internal/config/config.go` (export the field validators used by `Load`: `func ValidateMediaRules(MediaRules) error`, `func ValidateAlertThresholds(AlertConfig) error`, `func ValidateBackupSchedule(time string, keep int) error`)
- Test: `internal/web/settings_test.go`, `internal/config/config_test.go`

**Interfaces:**
- Produces: `GET /settings` and `POST /settings/{media|alerts|backup}`. A valid post writes the setting and applies it live: the media funcs and the monitor thresholds read it on their next use, and the backup scheduler re-reads it after its next wait. An invalid post re-renders with the field error and writes nothing. The config-file values (`data_dir`, `time_zone`, `wa`, `alerts.ntfy` configured yes/no without the token, `backup.destination`, `web.listen`) are shown read-only with "set in config.yaml".

- [ ] **Step 1: Write the failing tests.** `TestSettingsMediaSaveAppliesToNextDownloadDecision`, `TestSettingsRejectsInvalidVideoCap` (negative value: no write, error text shown), `TestSettingsNeverShowsNtfyToken`, `TestSettingsBackupTimeValidated` (`25:00` rejected).
- [ ] **Steps 2-5:** Fail, implement, `make check`, commit.

### Task 9: Storage screen and Backups & alerts screen (read-only plus a test alert)

**Files:**
- Create: `internal/web/storage.go`, `internal/web/backups.go`, `internal/web/templates/storage.html`, `internal/web/templates/backups.html`; `internal/store/usage.go`
- Modify: `internal/health/health.go` (`Snapshot` gains `LastBackup time.Time`, `LastBackupErr string`, `LastBackupPath string`, set in `BackupResult`)
- Test: `internal/web/storage_test.go`, `internal/web/backups_test.go`, `internal/store/usage_test.go`, `internal/health/health_test.go`

**Interfaces:**
- Produces:
  - `type Usage struct{ ByChat []ChatBytes; ByMediaType map[string]int64; WeeklyGrowth int64 }`
  - `func (s *Store) Usage(ctx context.Context, now time.Time) (Usage, error)`: bytes per chat and type from the `media.declared_size` of `done` rows; `WeeklyGrowth` is the average bytes per week of `done` media plus `archive.db` growth estimated from messages received in the last 28 days times the average row size.
- Storage page: the database file size, `<data>/media` size (`view.DirSize`), backup folder size, free disk, weekly growth, and projected days until full (`free / weekly growth`, "more than a year" above 365). A warning shows when the projection is under `disk_free_min_gb`'s equivalent.
- Backups page: the schedule, the destination, the last result (time, path, error), the restore steps as text copied from `README.md`'s restore section, and whether ntfy is configured. `POST /backups/test-alert` sends one ntfy notification, "Test alert from WhatsApp Archiver", through `health.Notifier`, and shows "Sent" or the error.

- [ ] **Step 1: Write the failing tests.** `TestUsageSumsDoneMediaByChatAndType`, `TestProjectionMoreThanAYear`, `TestProjectionDaysUntilFull`, `TestBackupResultRecordedInSnapshot`, `TestBackupsPageHasNoRestoreButton` (no `<form` posting to a restore route), `TestTestAlertSendsOnce` (fake notifier: 1 call).
- [ ] **Steps 2-5:** Fail, implement, `make check`, commit.

### Task 10: Pair in the browser (FR-1)

**Files:**
- Modify: `internal/wa/wa.go` (`Run` gets a pairing mode that sends QR codes to a channel instead of the terminal), `internal/wa/pair.go`
- Create: `internal/wa/pairer.go`, `internal/web/pair.go`, `internal/web/templates/pair.html`
- Modify: `cmd/archiver/main.go` (`run` no longer exits 3 when unpaired), `deploy/archiver.service` (unchanged unless step 1 shows a need), `README.md` (pairing through the tunnel)
- Test: `internal/wa/pairer_test.go`, `internal/web/pair_test.go`, `cmd/archiver/main_test.go`

**Interfaces:**
- Produces (wa): a `type Pairer struct` implementing `web.Pairer` through an adapter in `cmd/archiver`, so that `web` does not import `wa`. `StartPairing` starts the client in pairing mode and forwards each `QRChannelItem` code, then `Done` on `PairSuccess`, or `Err` with `pairLine`'s text. Only one pairing runs at a time, and a second call returns the running channel's updates.
- Produces (web):
  - `GET /pair`: when paired, it shows "Paired" and the first-history time. When unpaired, a "Pair WhatsApp" button.
  - `POST /pair/start`, then the page shows the QR as a PNG at `GET /pair/qr.png` (rendered with `rsc.io/qr`, level L), updated over SSE event `pair`.
  - When done, "Paired. Capture is running." and a link to the dashboard.
- New `run` behaviour (rules on H18): when unpaired, `run` sends the existing "not paired" ntfy alert (body now "Open the dashboard through the SSH tunnel and pair"), starts the web server and health monitor without connecting to WhatsApp, and connects only after a successful pairing from the UI. `exitNotPaired` is no longer returned by `run`. `archiver pair` in a terminal still works as before.

- [ ] **Step 1: Re-pin the tests whose required behaviour changes, in one `test:` commit with this reason: "phase 2 FR-1: run stays up unpaired and pairs from the dashboard (plan 2026-10-20, Task 10)".** The H18 tests (`cmd/archiver/main_test.go`, those asserting exit 3) now assert that `run` on an unpaired folder serves `GET /pair` with the "Pair WhatsApp" button, sends one alert with the new body, and makes no WhatsApp connection (the fake socket's `Connect` count is 0). Add `TestPairingStreamsQRThenDone` (fake QR channel: two codes, then success), `TestSecondStartPairingReusesRun`, `TestQRPNGIsValidImage` (decodes as PNG), and `TestPairPageWhenPairedHasNoButton`.
- [ ] **Step 2:** Run them and see them fail. **Step 3:** Implement. Check in the pinned whatsmeow source that pairing mode needs nothing beyond what `Run(ctx, h, pairing=true)` does today, and quote file:line in the report. **Step 4:** `make check`; the archtest CON-1 guards pass (`web` imports no whatsmeow, and `cmd/archiver` only the adapter types). **Step 5:** Mutation specs (connects while unpaired, QR sent to stdout instead of the channel, a second pairing started). Commit.

### Task 11: Phase 2 docs and the acceptance checklist

**Files:**
- Modify: `README.md` (a "Dashboard" section: tunnel command, screens, what moved from `config.yaml` into the dashboard, what stays in the file), `docs/MODULES.md` (no change unless it mentions the UI)
- Create: `docs/phase2-acceptance.md`

- [ ] **Step 1:** Write `docs/phase2-acceptance.md` with one step per phase 2 exit criterion, each with commands and expected results:
  - SC-2: exclude a chat in the UI; a week later, `SELECT COUNT(*) FROM messages WHERE chat_jid IN (<its aliases>) AND received_at > <switch time>` is 0.
  - SC-4: pairing, and enabling a chat, each under 1 minute with no file editing; timed by Hamza.
  - SC-5: a new message appears in the activity feed within 2 s.
  - "Capture runs uninterrupted through the switch": the first start of the phase 2 binary on ryzen shows the migration and import lines, and messages are still arriving 10 minutes later.
- [ ] **Step 2:** Every command in the README section is checked against the code. Commit as `docs:`.

## Order and parallelism

Task 1 → Task 2 → Task 3 → Task 4 → Task 5 → (Tasks 6, 7, 8 and 9 in any order; they share only `routes()` in `server.go` and `layout.html` navigation) → Task 10 → Task 11. Run one task at a time on one branch (`phase2-ui`), and fast-forward `main` after each review, per D2. Deploying the finished phase is a separate step with Hamza's yes, using `README.md`'s update steps, and then `docs/phase2-acceptance.md`.
