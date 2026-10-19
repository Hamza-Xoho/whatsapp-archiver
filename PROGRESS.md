# Phase 0 progress

## Summaries of completed sections

Full text of every section below, verbatim and under the same headings: [docs/reviews/2026-10-19-progress-phase0-archive.md](docs/reviews/2026-10-19-progress-phase0-archive.md).

### Tasks 0 to 11 (all complete)

- Task 0 (prior-art intake): reviewed wacli, mautrix-whatsapp and the pinned whatsmeow source. Commit `bcd60e1`.
- Task 1 (skeleton, config, guardrails): config loader, archtest and lint guards, canary-checked. Tests `e64519a`, `7e546ac`; implementation `d1915e9`.
- Task 2 (archive store, retry, guard, testkit): one write pool, read pool, retry helper. Tests `24d1b1d`; implementation `968d535`.
- Task 3 (normaliser and scope filter): events normalised from `events.Message`, raw message kept. Tests `61d73a4`; implementation `a20fa43`.
- Task 4 (receive-only WhatsApp wrapper): wrapper with `SynchronousAck` on, version lookup. Tests `7abc461`; implementation `d254cd4`.
- Task 5 (ingest pipeline): handler write retries, one `Ingest` path. Tests `08ca602`; implementation `21482c9`.
- Task 6 (history sync import and ledger): history rows rebuilt as messages and routed through `Ingest`. Tests `d4497eb`; implementation `4e9c7c6`.
- Task 7 (media download queue): durable table-backed queue with a rate limiter and panic recovery. Tests `22cfbec`; implementation `9c2a69c`.
- Task 8 (backups): snapshot, integrity check, restore, `session.db` excluded. Tests `e4883c6`; implementation `b4ffeb1`.
- Task 9 (health monitor and ntfy alerts): ntfy contract, gated state alerts, persisted health snapshot. Tests `1ba594d`; implementation `77ba5e0`.
- Task 10 (record and replay harness): message record/load and stable JID scrubbing. Tests `1b59973`; implementation `9b2e679`.
- Task 11 (CLI, service, setup guide): pairing guard, version output, hardened Linux unit. Tests `e58e322`; implementation `5aeb538`.

### Phase 0 fixes

- Stage 0 (workspace tidy): plan, charter, research and prompts moved into the repo; tooling fixed so plain `make check` works on macOS (M1).
  Commits `b4c9e06`, `44a2d05`.
- B1 (never drop an event): every raw write goes through one `Pipeline.persist` that retries until stored. Tests `0662664`; fix `2e89ce2`.
- H2 (retry back-off): `persist` sleeps between retries and treats SQLite constraint errors as permanent. Tests `2f6f10d`; fix `685a1eb`.
- B2 (reconnect policy) and M3 (whatsmeow logger): pure reconnect policy in `internal/wa/reconnect.go`, whatsmeow log adapter.
  Tests `d37ed83`, `599479b`; fix `6107842`.
- H18 (unpaired run), M13 a/b (pair feedback), B2 carry-overs: `run` on an unpaired folder exits with "not paired" and one alert. Tests `39d45e6`, `b43e550`; fix `985fbf8`.
- H5 (history messages parsed like live ones): `NewHistoryParser` in `internal/wa/history.go` parses history messages as live ones. Test `b0548c9`; fix `0d568b2`.
- H6 (chat names kept, excluded chats never stored) and B1/H2 carry-overs: a nameless live message no longer wipes a known name; excluded history chats are not stored.
  Test `18b31ef`; fix `e29458d`.
- H9 (unique history ledger identity) and missing Task 6 tests: ledger ID is a hash of the chunk's deterministic encoding. Test `7ba2f46`; fix `01e9355`.
- H6b (review follow-ups to H5/H6/H9): an empty push name no longer wipes a known display name; chunk-ID encoding pinned. Test `0cffc31`; fix `6495743`.
- H1 (message insert cost independent of table size): the duplicate check resolves one chat's aliases instead of scanning `messages`. Test `d678734`; fix `2d879f6`.
- H8 (only a real revoke is a revoke; both edit shapes): `normalizeContent` switches on `ProtocolMessage.Type`. Test `c5fec02`; fix `1891941`.
- H7 (chat events keep payload, timestamp and a deterministic ID): `FromChatEvent` stores compact JSON of every changed field. Tests `7b42d86`, `eb43717`; fixes `bd796ff`, `48bf903`.
- H10 (every planned health alert rule, alertGate with recovered messages): one `alertGate`, at most one alert per 6 h per key, one "Recovered" message. Test `8bb8429`; fix `7e79a07`.
- M2 (ntfy Notify off the event-handler path): alerts go through a bounded queue and one sender goroutine. Tests `5bb89af`, `42a0396`; fix `d338503`.
- H7/H8 fix round 1 (review I1, I2, I3, M2, M3, M6): review findings on chat-event fields and edit/revoke handling. Test `5feb288`; fix `e8aa845`.
- H10/M2 fix round 1: failed `Notify` logged, a dropped alert fires again on its next trigger. Test `aaa4d74`; fix `8585f73`.
- H10 fix round 2 (never-connected startup): the disconnected rule also fires before the first connect. Test `6483e86`; fix `f48e20f`.
- H11 (media-worker panic marks the row failed and is reported): the row in flight is set `failed` with reason `panic`. Test `ec7edd4`; fix `1a38d19`.
- H3 (a valid media re-upload request): `media` gains `from_me`; `wa.MediaRef` is the row's identity for the request. Test `f36ab89`; fix `a67c164`.
- H4 (rate-limited re-upload requests with a refill pass): expired rows are requested by a rate-limited pass, once per row. Test `80250e6`; fix `26958df`.
- M4 (seconds-scale backoff and a later re-queue): a download round retries with 1+2+4+8 s waits, then re-queues later. Test `fe0c66c`; fix `ec8106b`.
- Media fix round 1 (I-1 bounded re-upload, I-2 reset error logged): a re-uploaded row that expires again fails after one request. Test `5a912af`; fix `6663f1c`.
- Media fix round 2 (request pass cannot spin on an unsaveable row): the pass ends when failing a row cannot be saved. Test `9ca2c95`; fix `6bfa971`.
- H16 (retention removes only snapshot folders) and H15 (service can write the backup path): retention parses the snapshot layout; the unit's `ReadWritePaths` lists the backup path.
  H16 test `3824f8e`, fix `b9ee812`; H15 test `db811df`, fix `fcef416`.
- H12 (CON-1 guard by prefix with an explicit allow-list): archtest bans calls by prefix with an allow-list. Tests `e62cd20`, `17fb8db`, `95d8998`; fix `221d677`.
- H12 fix round 1 (wrappers and client escape): the guard also flags wrappers and exported escapes of the client in `internal/wa`. Commits are `test: H12 round 1 ...` (two) and the fix after them (no hashes in the section).
- Ingest follow-ups: Cycle A makes the exclusion filter fail closed on an alias lookup error (tests `fead636`, fix `6ce35a4`); Cycle B stores other secret-encrypted messages as events, not empty message rows (test `8123e24`, fix `e9de0fb`).
- H17 (`status` / `chats`: read-only open, time zone, planned fields, golden tests): `store.OpenReadOnly`, goldens reviewed before `-update`. Test `d7f3bc2`; fix `4dcd391`; goldens `769a110`; extra tests `9d8ad3a`.
- H17 fix round 1 (client version race): the client version and reason are one `atomic.Pointer`. Commits `test: H17 round 1 ...` and `fix: H17 round 1 ...` (no hashes in the section).
- H14 (record / replay / scrub, DumpRaw and the synthetic golden): recorder, replay and keyed `Scrub`; golden `synthetic.sql` reviewed line by line before `-update`.
  Cycle A tests `cf5b911`, `c4a66d3`, fix `6eaea75`; Cycle B test `f5faa62`, fix `deec544`; round 1 `c0698e6`/`5621cb8`, `577ca91`/`1c806de`.
- H13 and M5 (branch fix/h13, base 2799fe0): every planned test exists, goleak in three packages, `serve` joins its goroutines; round 1 stops the serve context cancelling a healthy write.
  Tests `00ce8be`, `da5800b`, red `838cd5b`, fix `f73e9b7`; round 1 red `d3bc424`, fix `47e546f`.
- M8 (setup guide for a Linux beginner, acceptance log complete; docs only): `docs/phase0-setup.md` and `docs/phase0-acceptance.md` rewritten and every command checked. No commit hashes in the section.
- Final fix wave (C1, I1, I2, M10 exclude, H14 snapshot + replay exit, docs): bodyless history stubs skipped, fixed alert bodies, a 30 s handler drain on shutdown, strict `exclude` validation.
  C1 `23f84e8`/`7e5e4d5`; I1 `25b15fa`/`4f4ae34`; I2 `b61d29f`/`42bab40`; M10 `fb277a0`/`8de7fcd`; H14 `792d67f`/`8283610`, `487f1be`/`4823a25`; docs `0e28406`.

## Final automated verification

- `make check`: passed, including race tests, golangci-lint, and Linux/macOS/Windows builds.
- Clean-clone `make check`: passed from `/tmp/whatsapp-archiver-check.D7ZTL5/archiver` with fresh module/tool downloads.
- Task 12: not attempted. `docs/phase0-acceptance.md` is ready to fill on the server with the iPhone.

## Task 12 — not attempted

Per the brief, only a ready-to-fill acceptance checklist will be produced.

## Open before phase 1 (recorded 19 Oct 2026, for continuing later)

Carried over from the acceptance run on the server. Testing status and the step-by-step log are in the private `docs/testing-status-2026-10-19.md` (git-ignored).

### Fixed this session (not merged)

- Recovered alert after restart: after a restart the "recovered" message never fired, because `alertGate.fired` is in memory. Fix: `NewMonitor` reads the previous `health.json` and marks its alert state fired (`learnActiveAlerts`, `internal/health/health.go`). Red `a619f38` (`TestRecoveredAfterRestartWhenPreviousStateWasAlert`), fix `1e52a6d`, on branch `fix/recovered-after-restart`, not pushed. Deployed to ryzen 18 Oct 22:04; the old binary is kept as `/opt/whatsapp-archiver/archiver.prev`. Side effect: a restart while an alert is active no longer re-sends it within 6 hours.

### Open code work

- [x] `archiver status` cost (19 Oct, `chatStatsQuery` in `internal/store/views.go`): one pass per aggregate instead of four per-chat correlated subqueries. Scale archive (200k messages, 32k media, 200 chats): 36.4 s wall and ~19.4 s CPU before, 5.7 s wall and ~5.5 s CPU after; `status` output identical. Tests `TestChatStatsMatchesPreviousQuery`, `TestChatStatsDoesNotRescanPerChat`. Task 3 below removed the second `messages_resolved` pass. Live archive not measured.
- [x] History "complete" label (D5, 19 Oct): let T = FULL if any FULL chunk exists, else INITIAL_BOOTSTRAP; start = latest `received_at` among T chunks with the lowest `chunk_order` (a restarted sync re-sends its first chunk); complete when a T chunk received at or after start has progress >= 100 (`historyChunksQuery`, `internal/store/store.go`). No sync id exists, so this is how the newest sync is found; limitation: a sync whose first chunk arrives after a later chunk reads "in progress" until a newer chunk arrives. Test `TestHistoryCompleteOnlyWhenLatestSyncReachesHundred` replaced the pinned test. "Back to" is still the global minimum over chats.
- Decided (19 Oct, by the controller at Hamza's request; code in the working tree, not committed): (a) ack only after an event is stored: `AddEventHandlerWithSuccessStatus`, and `EnableDecryptedEventBuffer` on, so a redelivered message is decrypted from the buffer (plaintext is nulled once the event is stored; hashes purged after 14 days). Test `TestOpenBuffersDecryptedEventsUntilStored`. (b) history stubs archived as message events, kind `stub`, value = stub type; no message row; `open_gaps` unchanged; a history CIPHERTEXT stub is not an open gap. (c) `--record` writes no event of an excluded chat (aliases included) and no history conversation or phone-number mapping of one. (d) message secrets (`MessageContextInfo.MessageSecret`, `BotMessageSecret`, also inside wrappers) are cleared from every raw proto written from now on; rows already in archive.db still hold them (open, see below).
- Open, needs Hamza: (1) a server-side scrub of message secrets already in `archive.db` on ryzen (a data change, not done). (2) history chunks are not gated by the ack change: an import interrupted by shutdown is still acked, so its messages can be lost; fix needs a ledger/ack ordering change. (3) merging `fix/recovered-after-restart` into `main` (not done; standing rule). (4) deploying the new binary (not done).
- [x] `backup` opens `archive.db` read-only (`openSource`, `mode=ro`, no `immutable`). Tests `TestSourceConnectionIsReadOnly`, `TestVacuumIntoWorksOnReadOnlySource`, `TestSnapshotOfWALArchiveWithoutShmVerifies`.
- [x] Small gaps: goleak `TestMain` in `wa`, `store`, `backup`; `go mod tidy` (only indirect markers and `go.sum` lines); exported-`any` rule `exported-any` in `internal/archtest` (red cases in `TestWAWrapperAndEscapeBypassesAreFlagged`; an untyped `var C = any(client)` is still not seen). The 500-line limit is met (H13 gap table moved to `docs/reviews/2026-10-19-h13-gap-table.md`).

### Operations (not code)

- Backups and the database share one SSD (`/dev/sdb2`). Plan: move `backup.destination` and the media copy to the 698 GB drive at `/mnt/data`.
- Ryzen ethernet fixed: `dhcp4: true` added to `/etc/netplan/00-installer-config.yaml` (backup `.bak-before-dhcp`). Address `192.168.0.104`; reachable as `ryzen.local`. The main router is the compound's, so no DHCP reservation is possible; the Deco is in access-point mode.
- Remote desktop (GNOME RDP) needs a monitor attached and a graphical session running.
- The media copy timer (04:30 nightly) has not been checked to run.

### Acceptance steps still needed before phase 1

- Step 2 and step 4 were skipped (step 2 blocks the test corpus in step 10; step 4 gates phase 1).
- Steps 6 to 10, 12 and 13 not run. Step 9 can run now; step 12 needs seven days of running.
- Step 3: the 100 MB video skip untested; sha256 file check not run; "nothing on anyone's phone" not checked.

## Phase 1 gate (plan `docs/plans/2026-10-19-phase1-gate.md`)

- Task 2 (history chunk acked only after it is stored, readiness Problem 4; closes "Open, needs Hamza" item 2): `ManualHistorySyncDownload` on; `Client.historyHandler` downloads the chunk under a 2-minute bound (`historyDownloadTimeout`) and acks only when the handler stored the notification and the chunk; 404/410 is logged at Error and acked (D4, no `DeleteMedia`); the ledger row is written last; `Handle` returns false on any history import error. Tests `bdb7f3c`; fix `58774b4`. `make check` green; mutation `specs-t2.json` 12/12 KILLED. Deviations: the notification message still reaches the handler (kept as its protocol event row) and only own-phone notifications are downloaded. Unproven: whether WhatsApp redelivers an un-acked notification after a restart (Task 8 step 6). Report: `.superpowers/sdd/2026-10-19-phase1-gate/task-2-report.md`.
- Task 3 (`archiver status` in one pass, readiness Problem 6, and the history complete rule, Problem 8): `StatusReport` (`internal/store/views.go`) evaluates the `messages_resolved` window once into a materialized CTE; the per-chat rows, the oldest history ts per chat and the open-gap lookup all read it. `ChatStats` and `HistoryCoverage` are thin wrappers, `status.go` calls only `StatusReport`. Scale archive (200k messages, 30k media, 200 chats): 6.0 s wall / ~6.0 s CPU before, 3.1 s / ~3.1 s after, output byte-identical. Commits: red `cc681a3`, green `802eee3`; rule: red `032df67`, fix `ba28414`; specs `ce08f87`, strengthened `7c1e001`; `specs-t3.json` 10/10 KILLED (after fix round 1: `ef513b1` test, `8a30b7e` fix, the first rule ignored `received_at`). Deviations: fixtures that stored chunks with an empty sync type now use `FULL` (status goldens unchanged, no `-update`); the alias view is still read once for media, once for gaps and once inside `messages_resolved`. Report: `.superpowers/sdd/2026-10-19-phase1-gate/task-3-report.md`.
- Task 4 (readable journal, readiness Problem 5): new `internal/logsafe.RedactURLs` (one regexp; query and fragment of an http(s) URL become `?REDACTED`, nothing else changes). It wraps the whatsmeow log text in `waLogger.logf`, the `err` attr of the two WARN sites in `media/queue.go` (re-upload request failed, retry answer not applied) and both `err` attrs in `wa/historydl.go`. `health.Monitor.SetState` logs Info `connection lost` (state, detail) when `connected` goes to any other state and `connection restored` (down_for) on return, only after this process logged a loss; the first connect and repeated states log nothing. Commits 318a1fc (test), 77c7d1a (fix), mutants 4b5032b: 11 of 11 killed. Deviation: the 7-day journal held no `mmg.whatsapp.net` lines, so the code grep drove the site list; `wa.go` connect/version errors are not URL-bearing and are left as is.
- Task 5 (`archiver version` names the build, deferred M9): `var version = "dev"` (was a const `0.1.0-phase0`); the Makefile sets `VERSION ?= $(shell git describe --always --dirty --tags 2>/dev/null || echo dev)` and passes `-ldflags "-X main.version=$(VERSION)"` in `build` and `build-all`. README deploy steps compare `archiver version` with the `git describe` of the deployed commit. Red `cd3ac24` (`TestVersionOutputUsesBuildStamp`, compile error against the const), fix `d26dbf1`; `make check` green (lint 0 issues). Deviation: M9 is not listed in PROGRESS.md's ledger, so no ledger row changed; the M9 row in `docs/reviews/2026-10-14-phase0-review.md` is left as is. Report: `.superpowers/sdd/2026-10-19-phase1-gate/task-5-report.md`.
- Task 6 (open decryption gaps, readiness Problem 3; D6: no sweep): read-only on a `.backup` copy of the live archive, deleted after (checked with `sudo -n ls`). 10 open of 31 `undecryptable`: class (a) = 0, (b) other JID form = 0, (c) never recovered = 9 (5 visible messages, 4 hide-mode events), plus `…6F66`, its own finding: a group member label change recovered as an event, matched by chat, sender and exact timestamp. The 5 missing visible messages need Hamza's decision (step 12: any miss halts phase 1; D6 did not accept losses). One line per gap in `docs/phase0-acceptance.md` step 12, commit `422085a`. The archive does not show "1 open on 18 Oct": open at end of day was 2, 3, 5, 10 (16 to 19 Oct); 24 of 31 events came on 19 Oct after the 01:29 restart (decrypted-event buffer build, SQLITE_BUSY Problem 2 not yet deployed), none near a disconnect. Deviation: step 4 not done. The planned rule (an event targets the gap's ID) would not close `…6F66` (its event has no target and does not store its own message ID) and would wrongly close `…FCCF`, a real message someone reacted to. A correct close needs the source message ID stored on event rows (a schema change), so it needs a ruling. No code, no tests, no mutation run. Report: `.superpowers/sdd/2026-10-19-phase1-gate/task-6-report.md`.
