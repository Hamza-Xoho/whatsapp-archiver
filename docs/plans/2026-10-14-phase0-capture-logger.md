# Phase 0: Hardened Capture Logger — Implementation Plan (v2)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A headless Go service that pairs as a WhatsApp linked device, records every chat (minus exclusions) into a local insert-only SQLite archive with media, survives the ways whatsmeow loggers actually fail in 2026 (client-version cut-offs, decryption gaps, LID/phone-number splits, late history chunks, handler panics), takes nightly snapshots, and alerts the user's phone through ntfy — without ever sending anything a contact can see.

**Architecture:** One Go binary (`archiver`), Go module at the repo root. `internal/wa` is the only package that imports the whatsmeow client; it exposes a receive-only surface (run with an event handler, download media, request a media re-upload) and owns pairing, the session database and the client-version check. whatsmeow calls our handler synchronously; the handler hands each event to `internal/ingest`, where a normaliser (protobuf → internal types), a scope filter and the single `Ingest()` path write through `internal/store` (one write connection, insert-only raw tables, read views that resolve LID/phone aliases). Media downloads run in a separate worker pool whose queue is the `media` table itself (live before backlog, restart-safe). Session keys live in their own SQLite file and are never backed up. Phase 1 adds jobs and modules on top of this same database, so this schema is the charter's raw layer, not a throwaway.

**Tech Stack:** Go 1.26+ (current whatsmeow `main` requires it); `go.mau.fi/whatsmeow` at the latest `main` pseudo-version (it has no tagged releases; CI bumps it weekly); `modernc.org/sqlite` (pure Go, no CGO; whatsmeow's session store opened with `_pragma=foreign_keys(1)`); `gopkg.in/yaml.v3`; `github.com/mdp/qrterminal/v3`; `golang.org/x/time/rate` (media re-request limiter); `log/slog` with JSON output; `go.uber.org/goleak` (tests only); `golangci-lint` with `depguard` and `forbidigo`; standard library `testing` and `net/http/httptest`.

**Specs:** WhatsApp Archiver — Project Charter & Build Plan v1.1, signed off 14 Oct 2026 (`docs/charter/WhatsApp Archiver — Project Charter & Build Plan.md`): phase 0 row of section 13 and spikes 1–5 in section 13b; requirements FR-4, FR-5, FR-6, FR-26, and FR-23 and FR-24 in basic form; constraints CON-1, CON-2, CON-5, CON-6, CON-7; ADR-13, ADR-15, ADR-20 to ADR-23, ADR-25, ADR-26. Design research: `docs/research/researchoutcome.md`.

## What changed from v1 of this plan

| Area | v1 | v2 | Why (research) |
| --- | --- | --- | --- |
| No-send guarantee | One grep test over the source | One wrapper package, `depguard` and `forbidigo` in lint, the grep test kept as an extra layer, a runtime receipt audit | whatsmeow sends protocol stanzas itself, so "no send code" can't be literal; lint is mechanical |
| Client version | Not handled | Version check at startup and after a rejection, operator override, `client_outdated` health state and alert, weekly bump workflow | WhatsApp cut-offs on 14 Sep and 5 Oct 2026 |
| Decryption failures | Not handled | Gap events stored, re-request from the phone turned on, gaps counted in `status` | wacli PR #441: undecryptable messages were silently dropped |
| Identities | JIDs as keys, no mapping | JIDs stored as received, `id_mappings` table, alias-aware dedup, scope filter and read views | WhatsApp's LID migration finished mid-2026 |
| History sync | Request full sync, import | Explicit day and size limits, chunk ledger, late chunks accepted, coverage shown in `status` | wacli PR #461 field data |
| Chat-level events | Group info only, via the message normaliser | `chat_events` table for group info, participants and app state | Research question 7, item 6 |
| Media | In-memory queue | Queue is the `media` table: live before backlog, restart-safe, rate-limited re-upload requests | mautrix-whatsapp #932; SC-3 must hold while history media downloads |
| Resilience | — | Panic recovery at two boundaries, one write connection, JSON logs to journald, `-race` and goleak | Research questions 6 and 7 |
| Testing | Go-constructed protobufs only | Plus recorded, scrubbed events from a test group replayed through ingest against golden database dumps | No fake WhatsApp server exists |
| Agent guardrails | Rules in `CLAUDE.md` | `CLAUDE.md` under 150 lines, hooks, separate test and implementation subagents, skills for procedures | `CLAUDE.md` is advisory; hooks are deterministic |
| SQLite driver | modernc with a mattn fallback | modernc only, with the foreign-keys pragma | whatsmeow #454 |
| Scope modes | Everything and selected | Everything plus exclude list; selected mode arrives with the UI in phase 2 | Can wait |
| Alerts | ntfy and Telegram | ntfy only | Can wait |
| Backups | Snapshots of both databases plus incremental media copy | Archive snapshots only; media copied by a documented rsync job; session never backed up | A stale session file breaks encryption sessions; less code |
| Layout | `core/` subfolder | Go module at the repo root | Research playbook layout |
| Prior art | — | Task 0 turns wacli and mautrix-whatsapp fixes into named tests | Don't rediscover fixes |

## Global Constraints

- **CON-1 (revised): nothing a contact can see is ever sent.** No messages, reactions, edits, deletions, read receipts, presence or typing. whatsmeow's own protocol traffic (acks, inactive delivery receipts, decryption retry receipts, key uploads, history-sync receipts, app-state fetches) is accepted. Enforcement:
  - Only `internal/wa` imports the client package `go.mau.fi/whatsmeow`. Other packages may import its data subpackages (`types`, `types/events`, `proto/...`), which hold no send methods.
  - Lint bans, everywhere, every method on the whatsmeow client whose name starts with `Send`, `Set`, `Update`, `Join`, `Leave`, `Revoke`, `Build`, `Mark`, `Subscribe`, `Create`, `Reject`, `Upload` or `Logout`, and names `SendPresence`, `SendChatPresence`, `MarkRead`, `SetForceActiveDeliveryReceipts` and `SendPeerMessage` explicitly. The single exception is `internal/wa/rerequest.go`, which may call `SendMediaRetryReceipt` (and `SendHistorySyncServerErrorReceipt`, if Task 0 finds the pinned version needs it for expired history blobs).
  - The app never calls `SendPresence`, so delivery receipts stay "inactive" and are not shown in contacts' apps.
  - `AutomaticMessageRerequestFromPhone` is turned on: it asks the user's own phone to resend messages this device couldn't decrypt.
- **CON-2:** raw tables are insert-only: `messages`, `message_events`, `chat_events`, `id_mappings`, `history_sync_chunks`. Edits, deletions, pins, reactions and decryption gaps are new rows; nothing in a raw table is ever updated or deleted. `chats`, `participants` and the state columns of `media` are metadata or control data and may update.
- **CON-7:** chat, sender and participant JIDs are stored exactly as received (LID or phone-number form). The two forms are linked through `id_mappings` and resolved by read views, never by rewriting rows.
- **CON-5:** everything under `data/` (session, archive, media, config with secrets, health snapshot) is gitignored. No secrets in the repo.
- **CON-6:** the live databases live on local disk under the data folder, never in a cloud-sync folder.
- **Scope:** log everything, with a per-chat exclude list (ADR-13). An excluded phone-number JID also excludes the same chat's LID form once a mapping is known, and the other way round. Selected-chats mode arrives with the UI in phase 2.
- **Always skip:** `status@broadcast`, typing and presence, receipts, calls.
- **Media defaults:** download documents, images and audio (including voice notes); video only up to **100 MB**; skip stickers and GIFs (store the message row, not the file). Live media is downloaded before backlog media. Expired media is re-requested at no more than **10 requests a minute**.
- **History:** request full sync at pairing with **3,650 days** and **4,096 MB** limits (configurable). Keep running after pairing; chunks can arrive hours later and after restarts.
- **Time:** WhatsApp timestamps are stored as UTC Unix seconds exactly as given, never rejected or corrected, alongside the time this device received the event. Output is shown in the configured time zone (default: the system's).
- **Session:** `data/session.db` and `data/archive.db` are separate files. The session file is never backed up, copied or restored; recovery is a fresh pairing.
- **Raw protobuf:** every stored message and event keeps its raw serialized protobuf, so future parsing improvements can re-read originals.
- **One writer:** exactly one connection writes to `archive.db`.
- **No fatal panics:** a panic in the event handler or a media worker is recovered, logged with its stack and counted; the process keeps running.
- **Platforms:** Ubuntu Server 26.04 LTS is the only service target in phase 0; the code must still build for macOS and Windows (pure Go makes this free; the disk-space check is stubbed on Windows).
- **Licences:** code adapted from openclaw/wacli (MIT) keeps its copyright notice in `NOTICE` and in the file header. Nothing is copied from mautrix-whatsapp (AGPL-3.0); it is read for ideas only. The repo itself has no licence for now (charter Q-8): don't add a `LICENSE` file or licence headers to our own code.

## Engineering Principles

These apply to every task and are summarised in `CLAUDE.md` in Task 1, so they bind every later phase too. A reviewer may reject a task for breaking one of them even if its tests pass.

**Test-driven development (strict red → green → refactor):**
- No production code without a failing test that demands it. Write the test, run it, **see it fail for the expected reason**, then write the least code that makes it pass.
- One behaviour per test; the test name states the behaviour (`TestInsertMessageIsIdempotent`, not `TestStore2`).
- Test through public interfaces only. If a test needs to reach into private state, the design is wrong, not the test.
- Use fakes at the edges (WhatsApp client, HTTP, clock, disk), never mocks of our own internal packages.
- Every bug found later gets a failing test first, then the fix.
- The refactor step is mandatory: after green, remove duplication and unclear names while tests stay green, then commit.

**Agent workflow (guards against test gaming):**
- **Red and green are different subagents.** A test-writer subagent writes the task's failing tests, runs them, confirms they fail for the expected reason, and commits them (`test: …`). A fresh implementer subagent then makes them pass without editing any `_test.go` file. While `.claude/green.lock` exists, the `guard-tests` hook rejects edits to test files.
- Golden files change only when a human runs `go test ./... -update` and reviews the diff.
- Hooks (Task 1): a Stop hook runs `make check`; a PreToolUse hook blocks deleting any `_test.go` file and blocks test edits during green. Claude Code ends a turn after 8 consecutive Stop-hook blocks, so the hook is a safety net, not a wall; the reviewer still checks.
- When a whatsmeow name is needed, look it up in `docs/prior-art.md` (Task 0), never guess.

**Least code that does the job (YAGNI):**
- Build only what a current test or a phase 0 requirement needs. No speculative options, hooks, interfaces with one implementation, or "for phase 1" code.
- Prefer the standard library and whatsmeow's own helpers over new dependencies or hand-written equivalents. Every new dependency needs a one-line justification in the commit message.
- An interface is introduced only when there are two implementations or a test needs a fake at an external boundary (WhatsApp, disk, network, clock).
- Delete code that no test exercises. Dead code is a review failure.
- Size guide, not a target: if a package passes ~300 lines of non-test code, stop and check whether it's doing two jobs. `internal/wa` may be larger but is split into files with one job each.

**Reuse before writing (DRY):**
- Before adding a function, search the repo for one that already does it. Extend an existing path rather than adding a parallel one.
- **One ingestion path:** live events, history sync and replayed fixtures all go through `ingest.Pipeline.Ingest()` (Task 5). Nothing writes raw rows any other way.
- One retry helper (`internal/retry`) used by store writes, media downloads and the notifier.
- One panic guard (`internal/guard`) used by the event handler and the media workers.
- One test-fixture package (`internal/testkit`) for temp stores, sample messages and a fake clock, shared by every package's tests.
- Duplicated logic in two places is fixed in the refactor step of the task that creates the second copy.

**Simple and readable:**
- Small functions, plain names, no clever abstractions. Code is read far more than written, mostly by Claude Code in later phases.
- Errors are returned and wrapped with context (`fmt.Errorf("insert message %s: %w", id, err)`), never swallowed or only logged.
- No global state; dependencies are passed in constructors.

**Concurrency:**
- Every goroutine has an owner and stops when its context is cancelled; every package that starts goroutines has `goleak.VerifyNone(t)` in its tests.
- No unbounded channels and no goroutine per event.
- Panics are recovered only at the two boundaries (event handler, media worker), through `internal/guard`.

**Verification gates (every task):** `make check` passes before the commit step. It runs `go test -race ./...`, `golangci-lint run` (vet, gofmt, errcheck, depguard, forbidigo) and `go build ./...` for `GOOS=linux`, `darwin` and `windows`. Line coverage of the task's package is reported in the commit message; aim for meaningful coverage of behaviour, not a number.

## Review Focus

1. **The same message arrives twice** (history sync and live, a reconnect replay, or a re-pair) → exactly one row. Tests in Task 2 (store idempotency) and Task 6 (history then live).
2. **An edit, deletion or reaction arrives for a message the archive doesn't have** → the event is still stored and nothing crashes. Tests in Task 2 and Task 3.
3. **A media download fails** (expired link, network drop) → the message row is kept, media is marked with the reason, expired media is re-requested at a limited rate, and failures are counted in `archiver status`. Tests in Task 7.
4. **The device is unlinked, or WhatsApp restricts the account** → an alert reaches the phone and the process stays up reporting `logged_out` or `temp_banned` instead of crash-looping. Tests in Task 9.
5. **The disk fills up or a database write fails** → no silent drops: the error is logged, an alert fires, and the write is retried until it succeeds rather than discarded. Tests in Task 5 (write retry) and Task 9 (disk and write alerts).
6. **WhatsApp rejects the client version** → a distinct `client_outdated` state, an alert naming the version in use, a fresh version check before each reconnect, no crash loop. Tests in Task 4 (version decision) and Task 9 (alert); drill in Task 12.
7. **A message can't be decrypted** → a gap event is stored, the phone is asked to resend, and a later successful decrypt becomes a normal message row that closes the gap. Tests in Task 3 and Task 5.
8. **The same chat arrives in LID and phone-number form** → no duplicate in the read views, the mapping is stored, and exclusions cover both forms. Tests in Tasks 2, 3, 5 and 6; spike 5 in Task 12.
9. **History chunks arrive late, out of order, or after a restart** → every chunk is stored and ledgered, and `status` shows how far back history is complete. Tests in Task 6.
10. **An event handler or media worker panics** → it is recovered, logged with its stack, counted and alerted; the process keeps capturing. Tests in Tasks 2, 5, 7 and 9.

---

## File Structure

```
whatsapp-archiver/
  go.mod                         # module at the repo root; go 1.26
  Makefile                       # check, build-all
  CLAUDE.md                      # under 150 lines: commands, invariants, package boundaries
  NOTICE                         # attribution for code adapted from openclaw/wacli (MIT)
  .gitignore                     # data/, *.db, *.db-*, /archiver, built binaries
  .golangci.yml                  # depguard (client import) + forbidigo (send methods)
  .github/workflows/
    ci.yml                       # make check on every push and pull request
    bump-whatsmeow.yml           # weekly: go get whatsmeow@main, make check, open a PR
  .claude/
    settings.json                # hooks: Stop → make check; PreToolUse → guard-tests
    hooks/guard-tests.sh         # blocks deleting tests, and editing tests while green.lock exists
    skills/
      bump-whatsmeow/SKILL.md    # Task 1
      add-event-kind/SKILL.md    # Task 5
      record-fixtures/SKILL.md   # Task 10
  cmd/archiver/main.go           # subcommands: pair, run, status, chats, backup, version
  internal/
    config/                      # YAML config load + defaults + validation
    retry/                       # the one retry-with-backoff helper
    guard/                       # the one panic guard
    testkit/                     # shared test helpers: temp store, sample messages, fake clock
    store/                       # archive.db: migrations, insert-only raw layer, read views, status queries
      migrations/0001_raw.sql
    wa/                          # the only importer of the whatsmeow client: session, pairing, version, download, re-requests
    ingest/                      # normaliser, scope filter, the single ingest path, history import, record/replay
    media/                       # table-backed download queue, rules, rate-limited re-requests, file layout
    backup/                      # archive snapshots, verify, retention
    health/                      # states, alert rules, ntfy notifier, health.json snapshot
    archtest/                    # source scans: client import rule and banned calls (extra layer behind lint)
  testdata/
    events/                      # recorded, scrubbed events from the test group (Task 12)
    golden/                      # golden database dumps for replay tests
  deploy/archiver.service        # systemd unit
  docs/
    prior-art.md                 # Task 0 output: edge cases → tests, pinned whatsmeow names, borrowed code
    MODULES.md                   # stub; the contract is written in phase 1
    phase0-setup.md              # Ubuntu Server install, config, pairing, service, updates, backups
    phase0-acceptance.md         # Task 12 results log
```

---

### Task 0: Prior-art intake

No production code. Turns what wacli and mautrix-whatsapp already learned into named tests, and pins the exact whatsmeow names every later task needs.

**Files:**
- Create: `docs/prior-art.md`

- [ ] **Step 1: Initialise the repo.** `git init` at the repo root; commit the existing `docs/` (charter, research, this plan) as `docs: charter v1.1, research, phase 0 plan`.
- [ ] **Step 2: Pin whatsmeow.** In a scratch folder, `go mod init scratch && go get go.mau.fi/whatsmeow@main`. Record the pseudo-version and the Go version its `go.mod` requires. Task 1 pins the same version.
- [ ] **Step 3: Read openclaw/wacli (MIT):** README, CHANGELOG, PR #441 (undecryptable messages), PR #461 (full history), its sync handler, media download and `doctor` code. For each fix that matters to phase 0, add a row to Table 1. Where its code is worth adapting, add a row to Table 3.
- [ ] **Step 4: Read mautrix-whatsapp releases v26.01–v26.09 and PR #955 (ideas only, AGPL; copy no code).** At minimum: backfill racing LID mappings and duplicate DMs from LID plus phone history data (v26.01); automatic app-state recovery (v26.02, v26.09); backfill without full history (v26.04); the new edit encryption scheme (v26.05); newer poll message versions (v26.07); nonsensical history-sync timestamps (v26.09); the logout-race panic during backfill (PR #955); throttling old media requests (issue #932).
- [ ] **Step 5: Read whatsmeow at the pinned version** (`update.go`, `receipt.go`, `retry.go`, `message.go`, `mediaretry.go`, `connectionevents.go`, `store/`, `types/events/`) and record in Table 2 the exact name, signature and file for each of:
  - fetching the latest client version (`GetLatestVersion`) and overriding the version in use;
  - the history-sync device properties: require full sync, days limit, size limit;
  - `AutomaticMessageRerequestFromPhone`;
  - whether acks can be sent only after our handler returns (a synchronous-ack option); if one exists, Task 4 turns it on so a crash before the write means WhatsApp redelivers;
  - events: `Message`, `UndecryptableMessage` (and its decrypt-fail field), `HistorySync`, `ClientOutdated`, `TemporaryBan`, `LoggedOut`, `StreamReplaced`, `Connected`, `Disconnected`, `MediaRetry`, `GroupInfo`, `JoinedGroup`, and the app-state events for archive, mute and pin;
  - where LID↔phone pairs appear: alternate-JID fields on message info, history-sync mapping lists, conversation fields;
  - the helper that turns a history-sync message into an `events.Message`;
  - media download, its 404/410 error values, `SendMediaRetryReceipt`, and decrypting a `MediaRetry` notification;
  - the sqlstore constructor and the DSN it needs with modernc.
- [ ] **Step 6: Write `docs/prior-art.md`** with three tables:
  - **Table 1, Edge cases:** case | source and date | our handling | test name | task. Every test name here must appear in that task's Step 1 list; the reviewer checks the two against each other.
  - **Table 2, Pinned whatsmeow names:** what | exact name and signature | file | pseudo-version.
  - **Table 3, Borrowed code:** wacli file → our file | licence (MIT) | what changed.
- [ ] **Step 7: Commit** — `docs: prior-art intake for phase 0`

---

### Task 1: Skeleton, config and guardrails

**Files:**
- Create: `go.mod`, `Makefile`, `CLAUDE.md`, `NOTICE`, `.gitignore`, `.golangci.yml`, `.github/workflows/ci.yml`, `.github/workflows/bump-whatsmeow.yml`, `.claude/settings.json`, `.claude/hooks/guard-tests.sh`, `.claude/skills/bump-whatsmeow/SKILL.md`, `docs/MODULES.md` (stub), `internal/config/config.go`, `internal/archtest/scan.go`
- Test: `internal/config/config_test.go`, `internal/archtest/archtest_test.go`, `internal/archtest/testdata/*.go.txt`

**Interfaces:**
- Produces:
  - `type Config struct { DataDir string; TimeZone string; Exclude []string; History HistoryRules; Media MediaRules; Backup BackupConfig; Alerts AlertConfig; WA WAConfig }`
  - `type HistoryRules struct { DaysLimit int /* 3650 */; SizeMBLimit int /* 4096 */ }`
  - `type MediaRules struct { Download []string /* "document","image","audio","video" */; VideoMaxMB int /* 100 */; Skip []string /* "sticker","gif" */; Workers int /* 2 */; RetryRequestsPerMinute int /* 10 */ }`
  - `type BackupConfig struct { Destination string; Time string /* "03:30" local */; Keep int /* 7 */ }`
  - `type AlertConfig struct { Ntfy *NtfyConfig; NoEventsAfter time.Duration /* 6h */; DisconnectedAfter time.Duration /* 15m */; PhoneQuietAfter time.Duration /* 240h */; DiskFreeMinGB int /* 5 */ }`
  - `type NtfyConfig struct { URL string /* full topic URL */; Token string /* optional */ }`
  - `type WAConfig struct { CheckLatestVersion bool /* true */; VersionOverride string /* e.g. "2.3000.1040847988", empty = none */ }`
  - `func Load(path string) (*Config, error)` — missing file → defaults; unknown key → error naming the key; malformed `VersionOverride` → error.
  - `func archtest.Scan(root string, rules Rules) ([]Violation, error)` — used only by the archtest tests.

- [ ] **Step 1: Write the failing tests**

```go
func TestLoadDefaults(t *testing.T) {
    c, err := config.Load(filepath.Join(t.TempDir(), "missing.yaml"))
    require.NoError(t, err)
    assert.Equal(t, 100, c.Media.VideoMaxMB)
    assert.ElementsMatch(t, []string{"document", "image", "audio", "video"}, c.Media.Download)
    assert.ElementsMatch(t, []string{"sticker", "gif"}, c.Media.Skip)
    assert.Equal(t, 10, c.Media.RetryRequestsPerMinute)
    assert.Equal(t, 3650, c.History.DaysLimit)
    assert.Equal(t, 4096, c.History.SizeMBLimit)
    assert.Equal(t, 7, c.Backup.Keep)
    assert.Equal(t, 6*time.Hour, c.Alerts.NoEventsAfter)
    assert.Equal(t, 240*time.Hour, c.Alerts.PhoneQuietAfter)
    assert.True(t, c.WA.CheckLatestVersion)
}
func TestLoadKeepsDefaultsForMissingFields(t *testing.T) { /* file sets only exclude -> media defaults unchanged */ }
func TestLoadRejectsUnknownKey(t *testing.T)            { /* "exlude: [...]" -> error mentioning "exlude" */ }
func TestLoadRejectsBadVersionOverride(t *testing.T)    { /* "2.3000" -> error mentioning "version_override" */ }

// archtest: the scanner is tested against fixture files first (red), then run over the real repo.
func TestScanFlagsClientImportOutsideWA(t *testing.T)   { /* testdata/bad_import.go.txt under a non-wa path -> 1 violation */ }
func TestScanFlagsBannedCall(t *testing.T)              { /* testdata/bad_send.go.txt calling client.SendMessage -> 1 violation */ }
func TestScanAllowsRetryReceiptInAllowListedFile(t *testing.T) { /* SendMediaRetryReceipt in internal/wa/rerequest.go -> 0 */ }
func TestRepoHasNoViolations(t *testing.T)              { /* Scan(repo root) -> empty */ }
```

- [ ] **Step 2: Run and confirm failure** — `go test ./internal/...` → FAIL (packages missing).
- [ ] **Step 3: Implement.**
  - `config.Load` with yaml.v3 and `KnownFields(true)`; apply defaults before unmarshal so partial files keep defaults.
  - `archtest.Scan`: walks `.go` files (skipping `vendor/` and `testdata/`), flags an import of exactly `"go.mau.fi/whatsmeow"` outside `internal/wa/`, and flags banned call names outside the allow-listed file. The banned list in `archtest` mirrors `.golangci.yml`; a comment in each points to the other.
  - `.golangci.yml`: `depguard` denies `go.mau.fi/whatsmeow` (the client package only, not its subpackages) outside `internal/wa`; `forbidigo` bans the method list from Global Constraints with the one allow-listed file. Check the exact rule syntax against the installed golangci-lint version; the canary step below proves it works.
  - `Makefile`: `check` runs the three gates; `build-all` cross-compiles.
  - `CLAUDE.md` (under 150 lines): build and test commands; invariants (CON-1, CON-2, CON-7, one ingest path, one write connection, no fatal panics, `data/` gitignored, session never backed up); package boundaries; the TDD and agent-workflow rules above; "look up whatsmeow names in `docs/prior-art.md`"; where the skills are.
  - `.claude/settings.json` and `guard-tests.sh`: Stop hook runs `make check`; PreToolUse hook on Edit, Write and Bash rejects deleting `*_test.go` and rejects editing `*_test.go` while `.claude/green.lock` exists. Check the hook schema against the current Claude Code docs.
  - `ci.yml` runs `make check` on push and pull request. `bump-whatsmeow.yml` runs weekly: `go get go.mau.fi/whatsmeow@main && go mod tidy && make check`, then opens a pull request (a scheduled workflow needs no third-party app; Renovate is an alternative).
  - `.claude/skills/bump-whatsmeow/SKILL.md`: read whatsmeow's commit log since the pinned pseudo-version for API changes, update `docs/prior-art.md` Table 2 if names moved, run `make check` and the replay corpus, build, deploy, confirm `archiver status` shows `connected` and the new version.
  - `NOTICE`, `.gitignore` (`data/`, `*.db`, `*.db-*`, `/archiver`, `dist/`), `docs/MODULES.md` stub ("written in phase 1; see charter section 10").
- [ ] **Step 4: Run tests** → PASS.
- [ ] **Step 5: Prove the guard bites (canary).** Create `internal/scratch/bad.go` that imports `go.mau.fi/whatsmeow` and calls `SendMessage` on a client. Run `golangci-lint run` and `go test ./internal/archtest/...` → both must fail, naming depguard, forbidigo and the scan. Delete the file. Paste the failing output into the commit message.
- [ ] **Step 6: Refactor** — with tests green, remove duplication, delete anything no test exercises, tidy names; then `make check`.
- [ ] **Step 7: Commit** — `feat: repo skeleton, config and guardrails`

---

### Task 2: Archive store, retry, guard and testkit

**Files:**
- Create: `internal/store/store.go`, `internal/store/views.go`, `internal/store/migrations/0001_raw.sql`, `internal/retry/retry.go`, `internal/guard/guard.go`, `internal/testkit/testkit.go`
- Test: `internal/store/store_test.go`, `internal/retry/retry_test.go`, `internal/guard/guard_test.go`

**Interfaces:**
- Produces (shared helpers, reused by every later task — do not re-implement elsewhere):
  - `func retry.Do(ctx context.Context, p retry.Policy, fn func() error) (attempts int, err error)` with `type Policy struct { Max int; Base time.Duration; Sleep func(time.Duration) }` — exponential backoff (`Base`, `2×Base`, `4×Base` …); `Sleep` is injectable so tests never wait.
  - `func guard.Run(log *slog.Logger, where string, onPanic func(where string, v any), fn func())` — runs `fn`, recovers a panic, logs it with the stack, calls `onPanic`.
  - `testkit.Store(t) *store.Store` (temp dir, auto-closed), `testkit.Message(chat, id string, ts time.Time) store.Message`, `testkit.Clock` (fake clock with `Now()` and `Advance(d)`).
- Produces (store):
  - `func Open(path string) (*Store, error)` — opens one write connection (`SetMaxOpenConns(1)`) and a separate read-only pool, WAL mode, `busy_timeout`, `foreign_keys(1)`; runs embedded migrations once; records `schema_version`.
  - `func (s *Store) UpsertChat(ctx, Chat) error`, `func (s *Store) UpsertParticipant(ctx, Participant) error` — metadata, may update.
  - `func (s *Store) InsertIDMapping(ctx, IDMapping) (inserted bool, err error)` — keyed by `(lid, pn)`.
  - `func (s *Store) InsertMessage(ctx, Message) (inserted bool, err error)` — keyed by `(chat_jid, id)`; also returns `false, nil` when a row with the same id already exists under the chat's mapped alias (checked in the same transaction, which the single write connection makes race-free).
  - `func (s *Store) InsertEvent(ctx, MessageEvent) (inserted bool, err error)` — keyed by `(chat_jid, target_id, kind, event_id)`; the target need not exist.
  - `func (s *Store) InsertChatEvent(ctx, ChatEvent) (inserted bool, err error)` — keyed by `(chat_jid, kind, event_id)`.
  - `func (s *Store) InsertMedia(ctx, Media) error`, `func (s *Store) SetMediaState(ctx, chatJID, msgID, state, reason string) error` — media state is control data and may change.
  - `func (s *Store) Aliases(ctx, jid string) ([]string, error)` — the other forms of a JID known from `id_mappings`.
  - `func (s *Store) ChatStats(ctx) ([]ChatStat, error)` — per canonical chat (through the read views): name, message count, oldest and newest timestamp, media failed count, open decryption gaps.
- Tables (raw layer of charter section 11): `chats`, `participants`, `id_mappings`, `messages` (incl. `raw_proto BLOB`, `source` = `live|history`, `ts` as given, `received_at`), `message_events` (incl. kind `undecryptable`), `chat_events`, `history_sync_chunks`, `media` (path, mime, declared size, sha256, state `pending|downloading|done|skipped|expired|retry_requested|failed`, reason, attempts, `retry_direct_path`, updated_at), `schema_version`.
- Views: `chat_aliases(alias_jid, canonical_jid)` (LID preferred when known), `messages_resolved` (one row per canonical chat and message id, earliest received), `open_gaps` (undecryptable events with no message row under either form).

- [ ] **Step 1: Write the failing tests**

```go
func TestRetryStopsOnSuccess(t *testing.T)   { /* fails twice then succeeds -> attempts == 3, sleeps Base, 2*Base */ }
func TestRetryGivesUpAtMax(t *testing.T)     { /* always fails, Max 5 -> attempts == 5, last error wrapped */ }
func TestGuardRecoversPanic(t *testing.T)    { /* fn panics -> onPanic called once with "where", no panic escapes */ }
func TestInsertMessageIsIdempotent(t *testing.T) {
    s := testkit.Store(t); m := testkit.Message("120363@g.us", "ABC", t0)
    ok1, _ := s.InsertMessage(ctx, m); ok2, _ := s.InsertMessage(ctx, m)
    assert.True(t, ok1); assert.False(t, ok2)
    assert.Equal(t, 1, countRows(t, s, "messages"))
}
func TestInsertMessageUnderKnownAliasIsDuplicate(t *testing.T) {
    /* insert id X in 9665…@s.whatsapp.net; InsertIDMapping(lid 1234…@lid, pn 9665…);
       insert id X in 1234…@lid -> inserted == false */
}
func TestAliasLearnedLaterCollapsesInView(t *testing.T) {
    /* X under PN, X under LID, then mapping -> 2 raw rows, 1 row in messages_resolved */
}
func TestEventForUnknownMessageIsStored(t *testing.T) {
    s := testkit.Store(t)
    ok, err := s.InsertEvent(ctx, store.MessageEvent{ChatJID: "120363@g.us", TargetID: "NOPE", Kind: "edit", EventID: "E1", NewText: "x"})
    require.NoError(t, err); assert.True(t, ok)
}
func TestIDMappingIsIdempotent(t *testing.T)            { /* same pair twice -> second false */ }
func TestOpenGapClosesWhenMessageArrives(t *testing.T)  { /* undecryptable event for X -> 1 open gap; insert X -> 0 */ }
func TestNoUpdateOrDeleteOnRawTables(t *testing.T) {
    /* scan store source: no UPDATE or DELETE against messages, message_events, chat_events, id_mappings, history_sync_chunks */
}
func TestWriteErrorIsReturned(t *testing.T)             { /* close the store; InsertMessage returns a non-nil error */ }
func TestChatStatsOldestNewest(t *testing.T)            { /* 3 messages t1<t2<t3 -> Oldest t1, Newest t3, Count 3 */ }
func TestTimestampStoredAsGiven(t *testing.T)           { /* ts in 2099 and ts 0 -> read back unchanged */ }
func TestReopenRunsMigrationsOnce(t *testing.T)         { /* open, close, open -> schema_version has one row per migration */ }
```

- [ ] **Step 2: Run and confirm failure.**
- [ ] **Step 3: Implement** `retry`, `guard` and `testkit` first (smallest pieces), then the store with `embed` for migrations and `INSERT … ON CONFLICT DO NOTHING` for raw tables (use `RowsAffected` for the `inserted` flag). One private `insert(ctx, query, args...) (bool, error)` serves every raw-table insert, so conflict handling exists once. The alias check in `InsertMessage` reuses the `chat_aliases` view.
- [ ] **Step 4: Run tests** → PASS.
- [ ] **Step 5: Refactor** — with tests green, remove duplication, delete anything no test exercises, tidy names; then `make check`.
- [ ] **Step 6: Commit** — `feat: insert-only archive store with alias views`

---

### Task 3: Normaliser and scope filter

Pure functions in `internal/ingest`; no I/O.

**Files:**
- Create: `internal/ingest/types.go`, `internal/ingest/normalize.go`, `internal/ingest/scope.go`
- Test: `internal/ingest/normalize_test.go`, `internal/ingest/scope_test.go`, Go-constructed fixtures in `internal/ingest/fixtures_test.go` (not real chats)

**Interfaces:**
- Consumes: `store` types, whatsmeow data subpackages (`types`, `types/events`, `proto/waE2E`, `proto/waHistorySync`) — allowed by depguard.
- Produces:
  - `type Kind string` with `KindMessage, KindEdit, KindRevoke, KindReaction, KindPin, KindPollVote, KindUndecryptable, KindGroupInfo, KindParticipants, KindAppState, KindPresence, KindTyping, KindReceipt, KindCall`.
  - `type Result struct { Kind Kind; Chat store.Chat; Sender store.Participant; Message *store.Message; Event *store.MessageEvent; ChatEvent *store.ChatEvent; Media *store.Media; Mappings []store.IDMapping; FromMe bool; Received time.Time }`
  - `func FromMessage(evt *events.Message, source string, received time.Time) Result` — `source` is `"live"` or `"history"`.
  - `func FromUndecryptable(evt *events.UndecryptableMessage, received time.Time) Result`
  - `func FromChatEvent(evt any, received time.Time) (Result, bool)` — group info, joined group, archive, mute, pin chat.
  - `func NewFilter(exclude []string, aliases func(jid string) []string) *Filter`; `func (f *Filter) Keep(chatJID string, kind Kind) bool`

- [ ] **Step 1: Write the failing tests** — one per case, each asserting `Kind` and the key fields; add every Task 3 test named in `docs/prior-art.md` Table 1.
  - plain text → `KindMessage`, `Text`, `Timestamp` as given, `SenderJID` as received, `RawProto` non-empty
  - reply → `QuotedID` set; forwarded → `Forwarded == true`
  - image / document / audio voice note / video / sticker → `Media` with correct `Type` and `Mime`; sticker gets `Type == "sticker"`
  - ephemeral and view-once wrappers are unwrapped to the inner message
  - protocol edit → `KindEdit`, `Event.TargetID` = edited message id, `Event.NewText` (old and new edit formats, per Table 1)
  - protocol revoke → `KindRevoke`, `Event.TargetID`
  - reaction → `KindReaction`; pin → `KindPin`; poll vote → `KindPollVote` (current and newer poll versions, per Table 1)
  - an edit whose target is unknown still yields a valid `Event` (Review Focus 2)
  - undecryptable → `KindUndecryptable`, `Event.TargetID` = message id, decrypt-fail mode kept, deterministic `EventID` so repeats are idempotent (Review Focus 7)
  - message info carrying an alternate JID → `Mappings` holds the LID↔phone pair
  - group info change, participant join/leave, archive and mute → `ChatEvent` with the right kind and a deterministic `EventID`
  - scope: excluded chat → false for every kind; excluded phone JID also excludes its mapped LID, and the other way round (Review Focus 8); not excluded → true; `status@broadcast` → false; presence, typing, receipt and call → false always; group info for a non-excluded group → true
- [ ] **Step 2: Run and confirm failure.**
- [ ] **Step 3: Implement** using whatsmeow's own unwrapping helpers (names from `docs/prior-art.md` Table 2); raw proto via `proto.Marshal`. Build the exclude set once in `NewFilter`; look aliases up only for chats not directly excluded.
- [ ] **Step 4: Run tests** → PASS.
- [ ] **Step 5: Refactor**, then `make check`.
- [ ] **Step 6: Commit** — `feat: event normaliser and alias-aware scope filter`

---

### Task 4: Receive-only WhatsApp wrapper

**Files:**
- Create: `internal/wa/wa.go` (open, run, pair), `internal/wa/session.go`, `internal/wa/version.go`, `internal/wa/props.go`, `internal/wa/download.go`, `internal/wa/rerequest.go` (the one allow-listed send file)
- Test: `internal/wa/wa_test.go`

**Interfaces:**
- Consumes: `config.Config`, `health` is not imported here (state changes arrive as whatsmeow events through the handler).
- Produces:
  - `func Open(ctx, cfg *config.Config, log *slog.Logger) (*Client, error)` — opens `data/session.db` through whatsmeow's sqlstore on modernc with `sessionDSN`; applies `historyProps` before pairing; turns on `AutomaticMessageRerequestFromPhone`; turns on synchronous acks if the pinned version has them (Task 0); decides the client version with `decideVersion`.
  - `func (c *Client) Paired() bool`
  - `func (c *Client) Run(ctx, h func(evt any), pairing bool) error` — connects; if `pairing` and not yet paired, prints QR codes to the terminal; registers `h` as the synchronous event handler (no channel in between, so a slow write slows whatsmeow instead of piling up events); reconnects; after a `ClientOutdated` event, re-runs `decideVersion` and reconnects with a newer version if one exists, otherwise waits 30 minutes and tries again; returns only on context cancel.
  - `func (c *Client) Download(ctx, rawProto []byte, directPathOverride string) ([]byte, error)` — returns `ErrMediaExpired` for whatsmeow's 404 and 410 errors.
  - `func (c *Client) RequestMediaRetry(ctx, ref MediaRef, rawProto []byte) error` — in `rerequest.go` only.
  - `func (c *Client) ApplyMediaRetry(evt *events.MediaRetry, rawProto []byte) (directPath string, err error)`
  - `func (c *Client) VersionInUse() string`
  - Private, tested: `sessionDSN(path string) string`, `decideVersion(built, latest, override string, latestErr error, checkLatest bool) (use, reason string)`, `historyProps(days, sizeMB int)`, `classifyDownloadErr(error) error`.
- `Client` has no method that sends anything visible. The whatsmeow client is a private field.

- [ ] **Step 1: Write the failing tests**
  - `TestSessionDSNEnablesForeignKeys`: the DSN contains `_pragma=foreign_keys(1)`, and opening whatsmeow's sqlstore on a temp file with it upgrades without error (guards against whatsmeow #454).
  - `TestDecideVersion` (table): override set → override, reason "override"; check on and latest newer than built → latest; check on and fetch failed → built, reason names the error; latest older than built → built; check off → built.
  - `TestHistoryPropsUseConfiguredLimits`: full sync required, days and size limits equal the config values.
  - `TestClassifyDownloadErr`: whatsmeow's 404 and 410 errors → `ErrMediaExpired`; other errors wrapped unchanged.
  - The archtest scan from Task 1 covers "only `rerequest.go` sends".
  - `Run` and pairing are thin glue over whatsmeow; they are verified live in Task 12 (pairing, reconnect, version drill), not with a fake.
- [ ] **Step 2: Run and confirm failure.**
- [ ] **Step 3: Implement**, using only names from `docs/prior-art.md` Table 2. Never call `SendPresence`. Leave whatsmeow's automatic delivery receipts as they are (inactive, not read receipts).
- [ ] **Step 4: Run tests** → PASS.
- [ ] **Step 5: Refactor**, then `make check`.
- [ ] **Step 6: Commit** — `feat: receive-only whatsmeow wrapper with version handling`

---

### Task 5: The ingest pipeline

**Files:**
- Create: `internal/ingest/pipeline.go`, `.claude/skills/add-event-kind/SKILL.md`
- Test: `internal/ingest/pipeline_test.go`

**Interfaces:**
- Consumes: `store.Store`, `Filter`, the normaliser, `retry`, `guard`.
- Produces:
  - `type Writer interface { InsertMessage(…); InsertEvent(…); InsertChatEvent(…); InsertIDMapping(…); UpsertChat(…); UpsertParticipant(…) }` — `*store.Store` satisfies it; tests wrap it to inject disk failures (the disk is an external boundary).
  - `type MediaQueue interface { Enqueue(ctx, store.Media) error; OnRetry(ctx, *events.MediaRetry) }` and `type HealthSink interface { Seen(t time.Time, fromMe bool); SetState(state, detail string); WriteFailed(err error); Panic(where string) }` — consumer-side interfaces so the pipeline never waits on media or health; tests use small recording fakes.
  - `func NewPipeline(w Writer, f *Filter, q MediaQueue, h HealthSink, log *slog.Logger) *Pipeline`
  - `func (p *Pipeline) Handle(evt any)` — the event handler given to `wa.Client.Run`. Wrapped in `guard.Run`. A small type switch only:
    - `*events.Message` → `Ingest(FromMessage(evt, "live", now))`
    - `*events.UndecryptableMessage` → `Ingest(FromUndecryptable(…))`
    - `*events.HistorySync` → `ImportHistory` (Task 6)
    - group info, joined group, archive, mute, pin → `Ingest(FromChatEvent(…))`
    - `*events.MediaRetry` → `q.OnRetry`
    - `Connected`, `Disconnected`, `LoggedOut`, `ClientOutdated`, `TemporaryBan`, `StreamReplaced` → `h.SetState`
    - anything else → debug log with the type name
  - `func (p *Pipeline) Ingest(ctx, r Result) error` — **the single ingestion path**: store mappings first (so the filter and dedup see them) → filter → write (wrapped in `retry.Do`) → media enqueue → `h.Seen`. Live events, history sync and replayed fixtures all call it; nothing else writes raw rows.
- Write failures never drop an event: after `retry.Do` gives up (Max 5), call `h.WriteFailed` once and keep retrying at the maximum interval until the write succeeds or the context ends. Returning from the handler lets whatsmeow ack the message, so returning early would lose it.

- [ ] **Step 1: Write the failing tests** (plus every Task 5 test named in `docs/prior-art.md` Table 1)
  - `TestHandleStoresKeptMessage`: a fixture `*events.Message` → one row.
  - `TestHandleDropsExcludedChat`: excluded chat → zero rows, zero media.
  - `TestHandleWriteFailureRetriesUntilStored`: the writer fails 7 times then succeeds → one row, exactly one `WriteFailed` (Review Focus 5).
  - `TestHandleRecoversPanic`: a writer that panics → `Panic("handler")` recorded, the next event is stored normally (Review Focus 10).
  - `TestUndecryptableThenResentClosesGap`: undecryptable X → one open gap; message X → gap closed, message row present (Review Focus 7).
  - `TestMappingFromAltJIDStoredBeforeFilter`: a message carrying an alternate JID for an excluded phone number → mapping stored, message dropped (Review Focus 8).
  - `TestSameMessageUnderLIDAndPhoneStoredOnce`: mapping known, X arrives under both forms → one row (Review Focus 8).
  - `TestChatEventsStored`: group name change and archive → two `chat_events` rows.
  - `TestFromMeUpdatesPhoneActivity`: a message sent from the user's phone → `Seen(t, true)`.
  - `TestUnknownEventIgnored`: an unrelated event type → no rows, no panic.
  - `goleak.VerifyNone`.
- [ ] **Step 2: Run and confirm failure.**
- [ ] **Step 3: Implement.** Write `.claude/skills/add-event-kind/SKILL.md`: add a normaliser test with a fixture, the `Kind` constant, the scope rule, the store kind, a replay fixture, a row in `docs/prior-art.md` if it came from prior art.
- [ ] **Step 4: Run tests** → PASS.
- [ ] **Step 5: Refactor**, then `make check`.
- [ ] **Step 6: Commit** — `feat: single ingest path with gap, alias and panic handling`

---

### Task 6: History sync import and ledger

**Files:**
- Create: `internal/ingest/history.go`; modify `internal/store/store.go` (ledger insert and coverage query)
- Test: `internal/ingest/history_test.go`, additions to `internal/store/store_test.go`

**Interfaces:**
- Consumes: whatsmeow's history-sync event and its per-message parse helper (names from Table 2), `FromMessage(evt, "history", now)`, `Ingest()`.
- Produces:
  - `func (p *Pipeline) ImportHistory(ctx, hs *events.HistorySync) (stored, skipped int, err error)` — writes the ledger row first (sync type, chunk order, progress, conversation and message counts, oldest and newest timestamp, received time); stores the chunk's LID↔phone mapping list; upserts chat names from conversation metadata; then sends each message through `Ingest()`; logs progress per chunk.
  - `func (s *Store) InsertHistoryChunk(ctx, HistoryChunk) error`
  - `func (s *Store) HistoryCoverage(ctx) (Coverage, error)` — chunks received, last progress, last chunk time, and oldest message per canonical chat.

- [ ] **Step 1: Write the failing tests** (plus every Task 6 test named in Table 1)
  - fixture chunk with 2 conversations (3 messages in a kept chat, 2 in an excluded chat) → `stored == 3`, `skipped == 2`, one ledger row
  - `TestHistoryThenLiveNoDuplicate`: import history containing `ABC`, then `Handle` a live event for `ABC` → 1 row (Review Focus 1)
  - `TestLateChunkAfterCompleteIsStored`: a chunk at progress 100, then another chunk later → both stored, two ledger rows (Review Focus 9)
  - `TestMappingsInChunkApplyToItsMessages`: one chunk holding DM data under both the LID and phone forms plus its mapping list → one conversation in `messages_resolved` (mautrix v26.01)
  - `TestNonsensicalHistoryTimestampStoredAsGiven`: a far-future and a zero timestamp → stored unchanged, import continues (mautrix v26.09)
  - `TestEmptyOrPartialChunkDoesNotPanic`: nil conversations, nil message bodies → no panic, counted as skipped (mautrix PR #955 idea)
  - `TestCoverageReportsOldestPerChat`
- [ ] **Step 2: Run and confirm failure.**
- [ ] **Step 3: Implement** as a loop that calls the parse helper, `FromMessage(…, "history", …)` and the existing `Ingest()`. No new filter or write code; if this task needs any, it belongs in `Ingest()`.
- [ ] **Step 4: Run tests** → PASS.
- [ ] **Step 5: Refactor**, then `make check`.
- [ ] **Step 6: Commit** — `feat: history sync import with chunk ledger`

---

### Task 7: Media download queue

**Files:**
- Create: `internal/media/queue.go`, `internal/media/rules.go`; modify `internal/store/store.go` (claim and reset queries)
- Test: `internal/media/queue_test.go`, `internal/media/rules_test.go`

**Interfaces:**
- Consumes: `store.Store`, `config.MediaRules`, `retry`, `guard`.
- Produces:
  - `type Downloader interface { Download(ctx, rawProto []byte, directPathOverride string) ([]byte, error); RequestMediaRetry(ctx, ref wa.MediaRef, rawProto []byte) error; ApplyMediaRetry(evt *events.MediaRetry, rawProto []byte) (string, error) }` — `*wa.Client` satisfies it; tests use a fake.
  - `func NewQueue(dataDir string, rules config.MediaRules, st *store.Store, d Downloader, clock Clock, log *slog.Logger) *Queue`
  - `func (q *Queue) Enqueue(ctx, m store.Media) error` — runs `decide`; inserts the row as `pending` or `skipped` with its reason; wakes a worker with a non-blocking send on a channel of capacity 1. Returns immediately.
  - `func (q *Queue) OnRetry(ctx, evt *events.MediaRetry)` — finds the row, calls `ApplyMediaRetry`, stores the new direct path in `retry_direct_path`, sets the row back to `pending`.
  - `func (q *Queue) Run(ctx)` — on start, resets `downloading` rows to `pending` and marks `retry_requested` rows older than 24 hours `failed` ("no re-upload"); then runs `Workers` workers, each wrapped in `guard.Run`, each claiming the next `pending` row (live before history, newest first).
  - `func decide(m store.Media, rules config.MediaRules) (action, reason string)` — pure.
  - `func (s *Store) ClaimMedia(ctx) (*MediaJob, error)`, `func (s *Store) ResetMediaOnStart(ctx, now time.Time) error`
- Download flow per row: `retry.Do(Policy{Max: 5})` for transient errors → on success write to a temp file, rename to `data/media/<chat-jid>/<YYYY-MM>/<message-id>.<ext>` (JID characters not allowed in Windows file names are replaced), record sha256, `done`. On `wa.ErrMediaExpired` → `expired`, then `RequestMediaRetry` under a `rate.Limiter` (`RetryRequestsPerMinute`, driven by the fake clock in tests) → `retry_requested`. After 5 failed attempts → `failed` with the reason.

- [ ] **Step 1: Write the failing tests**
  - document → file written at the layout path, state `done`, sha256 recorded
  - sticker → state `skipped`, no file
  - video of 150 MB declared size → state `skipped`, reason `over 100 MB cap`, no download attempted
  - fake downloader fails twice then succeeds → `done`, `attempts == 3`
  - fake downloader always fails → `failed`, reason saved, attempts capped at 5 with exponential backoff (Review Focus 3)
  - expired → one re-upload request; a `MediaRetry` event → downloaded with the new path → `done`
  - 30 expired items within one minute → exactly 10 requests sent; the rest are sent as the limiter refills
  - a live item enqueued after 100 history items is downloaded first (SC-3 under backlog)
  - `Enqueue` returns immediately while every worker is busy
  - rows left `pending` or `downloading` by a previous run are downloaded after restart
  - a worker panic is recovered, the row is marked `failed` with reason `panic`, the worker keeps going (Review Focus 10)
  - `goleak.VerifyNone`
- [ ] **Step 2: Run and confirm failure.**
- [ ] **Step 3: Implement.** The `media` table is the queue; there is no in-memory list. Retries use `retry.Do` from Task 2 — no second backoff implementation. Justify `golang.org/x/time/rate` in the commit message.
- [ ] **Step 4: Run tests** → PASS.
- [ ] **Step 5: Refactor**, then `make check`.
- [ ] **Step 6: Commit** — `feat: table-backed media queue with rate-limited re-requests`

---

### Task 8: Backups

**Files:**
- Create: `internal/backup/backup.go`
- Test: `internal/backup/backup_test.go`

**Interfaces:**
- Consumes: `config.BackupConfig`, the archive path.
- Produces:
  - `func Run(ctx, cfg *config.Config) (Report, error)` — `VACUUM INTO` a snapshot of `archive.db` into `<dest>/<timestamp>/archive.db`, run `Verify`, then delete snapshot folders beyond `Keep`. `session.db` is never included (ADR-26). Media is not copied by the app in phase 0; the setup guide adds an rsync job.
  - `type Report struct { Snapshot string; Bytes int64; Duration time.Duration }`
  - `func Verify(snapshotPath string) error` — opens the snapshot read-only and runs `PRAGMA integrity_check`.
  - `func Schedule(ctx, cfg, clock Clock, onResult func(Report, error))` — daily at `Backup.Time`.

- [ ] **Step 1: Write the failing tests**
  - the snapshot opens and has the same `messages` row count as the source (restore check)
  - a snapshot taken while another goroutine keeps inserting succeeds
  - `Verify` passes on a good snapshot and fails on a truncated file
  - with `Keep: 2`, three runs leave two snapshot folders
  - `TestSessionNeverBackedUp`: a data folder containing `session.db` → no file named `session.db` anywhere under the destination
  - destination missing or not writable → error returned (alert wired in Task 9)
  - `Schedule` fires once when the fake clock passes `Backup.Time`
- [ ] **Step 2: Run and confirm failure.**
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run tests** → PASS.
- [ ] **Step 5: Refactor**, then `make check`.
- [ ] **Step 6: Commit** — `feat: nightly archive snapshots with verify`

---

### Task 9: Health monitor and ntfy alerts

**Files:**
- Create: `internal/health/health.go`, `internal/health/ntfy.go`
- Test: `internal/health/health_test.go`, `internal/health/ntfy_test.go`

**Interfaces:**
- Produces:
  - `type Notifier interface { Notify(ctx, title, body string) error }` (external boundary); `func NewNtfy(cfg *config.NtfyConfig) Notifier` — POST to the topic URL with a `Title` header and, if set, a bearer token; sent through `retry.Do`.
  - `func NewMonitor(cfg *config.Config, n Notifier, clock Clock, diskFree func(path string) (uint64, error), versionInUse func() string) *Monitor`
  - Satisfies `ingest.HealthSink`: `Seen(t, fromMe)`, `SetState(state, detail)`, `WriteFailed(err)`, `Panic(where)`; plus `BackupResult(Report, error)`, `Run(ctx)` (checks every minute), `Status() Snapshot`.
  - States: `connected | disconnected | logged_out | client_outdated | temp_banned | stream_replaced`.
  - Writes `data/health.json` (temp file + rename) on every state change and every minute, so `archiver status` can read the running service's state without IPC.
- Alert rules:
  - immediately: `logged_out`; `client_outdated` (body names the version in use and says to update); `temp_banned` (body includes the expiry, and capture is paused per charter RISK-1); `stream_replaced` (another client took over the session)
  - `disconnected` for longer than `DisconnectedAfter`
  - no events for longer than `NoEventsAfter`
  - no activity from the user's own phone for longer than `PhoneQuietAfter` ("open WhatsApp on your phone to avoid the 14-day unlink")
  - backup failed; disk free below `DiskFreeMinGB`; write failures; recovered panics
  - one small `alertGate` type used by every rule: the same alert at most once per 6 hours, and one "recovered" message when it clears.
- `diskFree` uses `syscall.Statfs` on Linux and macOS; on Windows it's a stub returning "unknown".

- [ ] **Step 1: Write the failing tests**
  - ntfy: POST to the configured topic URL with the `Title` header and bearer token (httptest asserts method, path, headers, body)
  - `SetState("logged_out")` → exactly one notification; `Status()` reports it; repeated calls within 6 h → still one (Review Focus 4)
  - `SetState("client_outdated")` → one alert naming the version from `versionInUse` (Review Focus 6)
  - `SetState("temp_banned", "expires …")` → one alert including the expiry
  - fake clock advanced 6 h 1 min with no `Seen` → one "no events" alert; `Seen` afterwards → one "recovered"
  - fake clock advanced 10 days with only `Seen(t, false)` → one "phone quiet" alert
  - `diskFree` below threshold → one disk alert (Review Focus 5)
  - `WriteFailed` and `Panic` → one alert each per 6 hours (Review Focus 5, 10)
  - failed `BackupResult` → one alert naming the error
  - `health.json` is rewritten on a state change and parses back to the same snapshot
  - `goleak.VerifyNone`
- [ ] **Step 2: Run and confirm failure.**
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run tests** → PASS.
- [ ] **Step 5: Refactor**, then `make check`.
- [ ] **Step 6: Commit** — `feat: health monitor with ntfy alerts`

---

### Task 10: Record and replay harness

There is no fake WhatsApp server, so real events from the test group become the regression suite.

**Files:**
- Create: `internal/ingest/record.go`, `internal/ingest/replay.go`, `internal/ingest/scrub.go`, `.claude/skills/record-fixtures/SKILL.md`
- Test: `internal/ingest/record_test.go`, `internal/ingest/replay_test.go`

**Interfaces:**
- Produces:
  - `func NewRecorder(dir string) *Recorder`; `func (r *Recorder) Wrap(h func(any)) func(any)` — writes each supported event to `<dir>/<seq>-<kind>.json` (message info as JSON plus the protobuf as base64) before passing it on. Supported kinds: message, undecryptable, history sync, group info, joined group, archive, mute, pin, media retry.
  - `func Load(dir string) ([]any, error)` — rebuilds the events in order.
  - `func Scrub(in, out string) error` — replaces every phone number and LID, in JSON and inside protobuf string fields, with stable fakes (the same input always maps to the same fake); text is kept because the test group's content is written for the purpose.
  - `func Replay(ctx, p *Pipeline, events []any) error` — feeds `Handle` in order.
  - `func DumpRaw(st *store.Store) (string, error)` — sorted SQL dump of the raw tables, excluding `received_at`, for golden comparison.

- [ ] **Step 1: Write the failing tests**
  - record → load round trip is equal for each supported kind (Go-constructed events)
  - `Scrub` output contains none of the original numbers or LIDs (regex search) and maps each one consistently
  - `TestReplayMatchesGolden`: for each folder in `testdata/events/`, replay into a fresh store and compare `DumpRaw` with `testdata/golden/<name>.sql`; `-update` rewrites the golden file (by hand only)
  - start with one Go-constructed scenario in `testdata/events/synthetic/`; Task 12 adds the recorded corpus
- [ ] **Step 2: Run and confirm failure.**
- [ ] **Step 3: Implement.** Write `.claude/skills/record-fixtures/SKILL.md`: record only from the throwaway test group, scrub, review the diff by eye, commit; never record from real groups into `testdata/`.
- [ ] **Step 4: Run tests** → PASS.
- [ ] **Step 5: Refactor**, then `make check`.
- [ ] **Step 6: Commit** — `feat: record and replay harness with golden dumps`

---

### Task 11: CLI, service and setup guide

**Files:**
- Create: `cmd/archiver/main.go`, `deploy/archiver.service`, `docs/phase0-setup.md`
- Test: `cmd/archiver/main_test.go`, golden files in `cmd/archiver/testdata/`

**Interfaces:**
- Consumes: everything above.
- Produces subcommands (flags `--data <dir>`, default `./data`; config at `<data>/config.yaml`):
  - `archiver pair [--record <dir>]` — the same capture pipeline as `run`, with QR pairing enabled; prints "Paired. Capture is running." and keeps capturing until Ctrl-C, so no history chunk arrives at a process that isn't storing it. Refuses if already paired.
  - `archiver run [--record <dir>]` — wrapper + ingest + media queue + backup schedule + health monitor; refuses to start if not paired ("run `archiver pair` in a terminal first"); graceful shutdown on SIGTERM (stop taking events, finish the current one, close the store).
  - `archiver status` — reads `archive.db` read-only and `health.json`: connection state, client version in use and why (latest, built-in or override), last event, phone last active, history coverage ("complete back to <date>", chunks, last chunk time), then per chat: name, canonical JID, messages, **oldest** and newest (in the configured time zone), media failed / expired / pending, open decryption gaps; totals; recovered panics; data folder size.
  - `archiver chats` — lists captured chats with canonical JID, name, message count and whether excluded, to help fill the exclude list. Reads the archive only; makes no WhatsApp calls.
  - `archiver backup` — runs a backup now and prints the report.
  - `archiver version` — binary version, whatsmeow pseudo-version, client version last used.
- Logs: `slog` JSON to stderr; under systemd that goes to journald, which handles rotation.
- `deploy/archiver.service`: `Restart=always`, `RestartSec=10`, a dedicated user, `WorkingDirectory` set to the install folder.
- `docs/phase0-setup.md`, written for someone new to Linux, every command copy-paste ready with expected output:
  - install Ubuntu Server 26.04 LTS (USB with balenaEtcher, tick OpenSSH); SSH in from Mac or Windows; copy the built binary; create the data folder;
  - write `config.yaml` with a commented example: exclusions (DM exclusions by phone-number JID; anything that must never be stored goes in before pairing, because history arrives immediately and raw rows are never deleted), ntfy topic and the ntfy phone app, time zone;
  - `archiver pair`, scan from WhatsApp → Linked Devices, leave it running at least 10 minutes, Ctrl-C, install and start the service; late history chunks arrive on the next connection;
  - check `archiver status` and `archiver chats`; read logs with `journalctl`;
  - updating: why it matters (WhatsApp retires client versions every few weeks), how to install a new release, what the `client_outdated` alert means;
  - backups: the nightly snapshot, plus an rsync job (systemd timer) for `data/media/`; never copy or restore `session.db` — moving or recovering means re-pairing.
- [ ] **Step 1: Write the failing tests** — `status` and `chats` against a seeded temp data folder (including a decryption gap, history coverage and a `health.json`) print the expected output (golden-file comparison); `run` without a session exits with the "pair first" message.
- [ ] **Step 2: Run and confirm failure.**
- [ ] **Step 3: Implement** the CLI with the standard `flag` package and one subcommand switch; write the service file and guide.
- [ ] **Step 4: Run tests** → PASS; `make build-all` succeeds for Linux, macOS and Windows.
- [ ] **Step 5: Refactor**, then `make check`.
- [ ] **Step 6: Commit** — `feat: CLI, systemd service and setup guide`

---

### Task 12: Live acceptance and spikes (manual, on the server)

**Files:**
- Create: `docs/phase0-acceptance.md` (the results log); `testdata/events/<scenario>/` and `testdata/golden/<scenario>.sql` from the recorded corpus

- [ ] **Step 1: Before pairing.** The phone is an iPhone (charter Q-7); record its iOS and WhatsApp versions in the log. Write `config.yaml`: exclusions, ntfy topic, time zone. Confirm a test notification reaches the phone.
- [ ] **Step 2: Pair once, with recording (spike 1 starts).** `archiver pair --record ~/rec` against the real phone; leave it running at least 10 minutes; Ctrl-C; start the service with `--record ~/rec` for the first day only. Note the pairing time.
- [ ] **Step 3: Test group day.** In a WhatsApp group with one other person, exchange: text, reply, forward, edit, delete-for-everyone, reaction, pin, poll and a vote, voice note, PDF, image, sticker, a video over 100 MB, a group name change, adding and removing a participant; then archive and mute the chat on the phone. Expected: `archiver status` shows the group; each item is in `archive.db` (edits and deletion as events with the original text kept; group and app-state changes in `chat_events`); sticker and big video `skipped`; no message appears on anyone's phone from the archiver.
- [ ] **Step 4: Receipt audit (spike 4).** With a second phone in the test group, close every other linked device on Hamza's account (WhatsApp Desktop or Web), put his phone in airplane mode for 30 minutes and send messages from the second phone. Expected: the sender sees no extra delivery ticks, no read receipts and no "online" caused by the archiver. Watch for 24 hours with the phone back on: no "online" or typing ever attributed to the archiver. Any visible receipt halts phase 1 until explained.
- [ ] **Step 5: Version-drift drill (spike 3).** Stop the service. Set `version_override` to the retired version `2.3000.1040847988` and `check_latest_version: false`; start. Expected: state `client_outdated`, an ntfy alert within 5 minutes naming that version, the process stays up, `status` shows it. Then clear the override, set `check_latest_version: true`, restart → `connected`. (The handshake is rejected before any message is delivered, so this is safe on the real session.)
- [ ] **Step 6: Kill and restart.** `systemctl restart archiver` mid-conversation → no duplicates, nothing missed. Pull the network cable for 20 minutes → disconnected alert on the phone, recovery message on reconnect.
- [ ] **Step 7: Unlink.** Unlink the device from the phone → logged-out alert within a minute; re-pair with `archiver pair` (this also triggers a second full history sync).
- [ ] **Step 8: LID dedup (spike 5).** After the re-pair, run the query that counts `(canonical chat, message id)` pairs appearing more than once in raw `messages`, and the same count in `messages_resolved`. Record both. Raw duplicates are expected; any duplicate in the view means the dedup key changes before phase 1.
- [ ] **Step 9: History reach (spikes 1 and 2).** 48 hours after the first pairing, record per BMC group and per chat type: the oldest recovered message, when progress reached 100%, the number of chunks, and whether chunks arrived after restarts. If the course groups reach the start of term, mark backup import as not needed in the charter's later list. Compare the results with the Android data in the research (90 days on an ordinary pairing; about 22 months on full sync, or 3 years with a raised size limit); if iOS stops much sooner, raise backup import in the charter's later list.
- [ ] **Step 10: Commit the corpus.** Scrub `~/rec` (test group only), pick scenarios covering every kind from Step 3, commit them to `testdata/events/` with golden dumps, and confirm `TestReplayMatchesGolden` passes. Remove `--record` from the service. Delete `~/rec`.
- [ ] **Step 11: Backup restore.** Copy the latest snapshot to another folder, run `archiver status --data <copy>`, confirm the counts match (the copy has no session file and shows "not paired", as intended).
- [ ] **Step 12: Seven-day audit (SC-1, SC-10).** After 7 days, pick two groups and compare every message on the phone against `archiver status` and the database. Exit criterion: zero missing, and every decryption gap listed in `status` either recovered or explained. Any miss halts phase 1 until fixed.
- [ ] **Step 13: Commit** — `docs: phase 0 acceptance results`

**Phase 0 exit:** every step above recorded in `docs/phase0-acceptance.md`; spikes 1 to 5 have results and any decision they forced is written into the charter's decision log; the 7-day audit passes.
