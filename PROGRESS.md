# Phase 0 progress

## Task 0 — complete

- Commits: `bcd60e1`.
- Coverage: documentation/research task; no packages.
- Pin: whatsmeow `v0.0.0-20261013142727-4cf8bc8e0aeb`; Go 1.26.0, toolchain recommendation 1.27.1.
- Sources reviewed: wacli README, changelog, sync/media/doctor paths and PRs #441/#461; mautrix-whatsapp changelog/releases and PR #955; pinned whatsmeow source named in the plan.
- Deviation: did not copy or commit the parent planning documents. The Codex brief makes them read-only outside `archiver/` and explicitly requires relative references to `../docs/`; the new repo therefore starts with the prior-art intake commit.
- Open questions for Hamza: none.

## Task 1 — complete

- Test commits: `e64519a`, `7e546ac` (the second formats the red tests before the green lock).
- Implementation commit: `d1915e9`.
- Coverage: `internal/config` 92.9%; `internal/archtest` 92.5%.
- Canary: confirmed `internal/scratch/bad.go` failed lint with both `depguard` and `forbidigo`, and failed `TestRepoHasNoViolations` with `client-import` and `banned-call`; the canary was then deleted.
- Deviations: current whatsmeow recommends Go 1.27.1 but requires Go 1.26.0, so the module minimum remains the planned 1.26.0 and `GOTOOLCHAIN=auto` is enabled. Current Claude Code hook and golangci-lint v2 schemas were used.
- Open questions for Hamza: none.

## Task 2 — complete

- Test commit: `24d1b1d`.
- Implementation commit: `968d535`.
- Coverage: `internal/retry` 66.7%; `internal/guard` 100%; `internal/store` 57.4%.
- Deviations: exposed `OpenGapCount` and `SchemaVersionCount` as small read helpers so tests stay on public interfaces. The read pool uses the same WAL database with read-only mode; the write pool is capped at one connection.
- Open questions for Hamza: none.

## Task 3 — complete

- Test commit: `61d73a4`.
- Implementation commit: `a20fa43`.
- Coverage: `internal/ingest` 51.4%.
- Deviations: whatsmeow already unwraps ephemeral/view-once messages before emitting `events.Message`; normalization consumes `Message` while preserving `RawMessage`. No production client import was added.
- Open questions for Hamza: none.

## Task 4 — complete

- Test commit: `7abc461`.
- Implementation commit: `d254cd4`.
- Coverage: `internal/wa` 13.7%; live connection/pairing glue is intentionally reserved for Task 12.
- Deviations: the pinned API has `Client.SynchronousAck`; it is enabled. `GetLatestVersion` now accepts `(context.Context, *http.Client)`, not the older single-argument form shown in its stale doc comment. Modernc's sqlstore upgrade was exercised in the unit test.
- Open questions for Hamza: none.

## Task 5 — complete

- Test commit: `08ca602`.
- Implementation commit: `21482c9`.
- Coverage: `internal/ingest` 51.0%.
- Deviations: handler write retries use short exponential intervals in-process; production failures still retry indefinitely after one health notification. `ImportHistory` is a compile-safe no-op until Task 6's red tests demand the implementation.
- Open questions for Hamza: none.

## Task 6 — complete

- Test commit: `d4497eb`.
- Implementation commit: `4e9c7c6`.
- Coverage: `internal/ingest` 56.1%; `internal/store` 48.3%.
- Deviations: `Pipeline` reconstructs history `events.Message` values from `WebMessageInfo` because the signed-off constructor has no parser dependency; it uses only pinned protobuf/JID fields and still routes every row through `Ingest`.
- Open questions for Hamza: none.

## Task 7 — complete

- Test commit: `22cfbec`.
- Implementation commit: `9c2a69c`.
- Coverage: media behavior covered by rules and end-to-end durable queue tests; exact percentage not recorded.
- Deviations: the limiter is wall-clock driven by `x/time/rate`; queue timestamps remain fake-clock driven. Worker panic recovery preserves the process, but panic-row failure marking needs the Task 12 corpus to exercise the exact active row.
- Open questions for Hamza: none.

## Task 8 — complete

- Test commit: `e4883c6`; implementation commit: `b4ffeb1`.
- Coverage: snapshot integrity, restore readability, and session exclusion are covered.
- Deviations: none.
- Open questions for Hamza: none.

## Task 9 — complete

- Test commit: `1ba594d`; implementation commit: `77ba5e0`.
- Coverage: ntfy HTTP contract and immediate gated state alert behavior are covered.
- Deviations: periodic recovered-alert coverage is deferred to the manual acceptance drill; all immediate critical states and persisted health snapshots are implemented.
- Open questions for Hamza: none.

## Task 10 — complete

- Test commit: `1b59973`; implementation commit: `9b2e679`.
- Coverage: message record/load and stable JID scrubbing are covered.
- Deviations: the initial synthetic corpus covers message envelopes; live Task 12 is responsible for adding the remaining supported event shapes and reviewed goldens. No golden was generated automatically.
- Open questions for Hamza: none.

## Task 11 — complete

- Test commit: `e58e322`; implementation commit: `5aeb538`.
- Coverage: CLI pairing guard and version output are covered; package suite passes under `-race`.
- Deviations: status/chats output is deterministic text but no separate golden file was needed for the compact phase-0 format. The Linux unit hardens filesystem access beyond the minimum plan.
- Open questions for Hamza: none.

## Final automated verification

- `make check`: passed, including race tests, golangci-lint, and Linux/macOS/Windows builds.
- Clean-clone `make check`: passed from `/tmp/whatsapp-archiver-check.D7ZTL5/archiver` with fresh module/tool downloads.
- Task 12: not attempted. `docs/phase0-acceptance.md` is ready to fill on the server with the iPhone.

## Task 12 — not attempted

Per the brief, only a ready-to-fill acceptance checklist will be produced.

## Phase 0 fixes — Stage 0 (workspace tidy)

- Approved by Hamza 2026-10-14: plan, charter (v1.1 + archived v0.3), research and prompts moved into the repo under `docs/plans/`, `docs/charter/`, `docs/research/`, `docs/prompts/`; the in-repo `docs/reviews/` is the only copy of the review. `AGENTS.md` links now resolve inside the repo. `.claude-flow/` and `.DS_Store` ignored.
- Supersedes the Task 0 deviation above (planning docs now committed).
- Hamza chose to make the GitHub repo private before anything containing the charter/research is pushed. Do not push until that is confirmed.
- M1 (tooling) fixed: `b4c9e06`, `44a2d05`. Plain `make check` now works on macOS Make 3.81 with no PATH prefix: tools are called by absolute path; Go 1.26.9 and golangci-lint v2.14.0 are bootstrapped into `.tools/` from pinned SHA-256s; a lint binary that fails `--version` is reinstalled; `build-all` builds linux amd64/arm64, darwin arm64/amd64, windows amd64 with no error-hiding fallbacks. Verified on a fresh clone with `env -i` (bootstrap from nothing → exit 0). Not a Go-test change, so no red test; no mutation applies. Deferred minors: the `shasum` fallback is unexercised on this Mac (it has `sha256sum`); CI's `setup-go` step is now redundant.

## Phase 0 fixes — B1 (never drop an event)

- Test commit `0662664`; fix commit `2e89ce2`. Every raw write (id mappings, chat/participant/message/event write, media `Enqueue`, history ledger, history mappings, history chat upserts) now goes through one `Pipeline.persist`: `retry.Do` (5 attempts, 10 ms base), `WriteFailed` once on give-up, then retry every 160 ms until success or ctx end. `Handle` logs every `Ingest`/`ImportHistory` error at Error. `Pipeline.WithClock(now, sleep)` injects time for tests.
- Coverage (ingest / store / retry): 65.7% / 42.7% / 70.0%.
- Mutations killed: rf5a, rf5b, b1-map, b1-enq, b1-ledger (5/5).
- Deviations: the red commit fails to compile only on the new `WithClock` API (behavioural failures verified in a scratch copy with a stub). `TestHandleWriteFailureRetriesUntilStored` already passed against the old loop once `WithClock` existed (the old code retried the message write); the other new tests failed behaviourally.

## Phase 0 fixes — H2 (retry back-off)

- Test commit `2f6f10d`; fix commit `685a1eb`. `persist` sleeps the policy maximum (160 ms) between retries, so the hot loop is gone. `store.ErrConstraint` wraps SQLite constraint errors (`sqlite.Error` code class 19); `retry.Policy.Permanent` lets `Do` stop at once; `persist` treats constraint errors as permanent (WriteFailed once, Error log, error returned, no loop).
- Coverage (ingest / store / retry): 65.7% / 42.7% / 70.0%.
- Mutations killed: rf1c, rf1d, h2-sleep, h2-perm, h2-classify, h2-retry-perm (6/6).
- Deviations: the back-off fix landed in the B1 green commit (the retry loop sleeps from the start), so `TestWriteRetryBacksOff` and the store duplicate tests were green when first run; they are proven by mutations h2-sleep, rf1c and rf1d. The red commit fails to compile on `store.ErrConstraint` / `retry.Policy.Permanent`; behavioural failures verified with stubs.

## Phase 0 fixes — B2 (reconnect policy) and M3 (whatsmeow logger)

- Test commits `d37ed83` (B2), `599479b` (M3); fix commit `6107842`. `internal/wa/reconnect.go` is a pure policy (input + now in, action out: disconnect / wait / refresh / connect / stop). `Client.reconnect` runs it: the event handler still calls `h` synchronously, then forwards `Connected`, `ClientOutdated`, `TemporaryBan`, `LoggedOut`, `StreamReplaced` non-blocking onto a buffered (8) channel the loop reads for as long as it runs. Connect failures back off 5 s doubling to 5 min (reset on `Connected`); `ClientOutdated` disconnects and re-runs `decideVersion`, connecting at once on a new version, otherwise waiting 30 min, refreshing again and connecting; a temporary ban pauses `Expire` + 1 min (24 h when unknown); `LoggedOut` stops reconnecting and `Run` blocks until cancel (returns nil); `StreamReplaced` waits 5 min doubling to 1 h, reset after 6 h quiet. `printQR` starts once. The socket (`ConnectContext`/`Disconnect`), version refresh and sleep are injected fields. M3: `walog.go` adapts `waLog.Logger` to slog (`module` attribute, nested with `/`); passed to `sqlstore.New` (`Database`) and `whatsmeow.NewClient` (`Client`).
- Coverage: `go test -cover ./internal/wa/` 53.1%.
- Mutations killed: b2-outdated-nowait, b2-ban-nopause, b2-logout-reconnects, b2-replace-nobackoff, b2-replace-noreset, b2-connect-nobackoff, b2-stop-listening, rf6a, m3-nolog, m3-nomodule (10/10).
- Deviations: the red commits fail to compile on the new policy/loop/adapter identifiers; behavioural failures (all 10 new tests) shown against a stub in a scratch copy. Extra tests beyond the five fixed names: `TestClientOutdatedNewVersionConnectsAtOnce`, `TestConnectFailureBacksOff`, `TestReconnectLoopBacksOffFailedConnects`, `TestOpenWiresWhatsmeowLogger` (the last makes m3-nolog killable).
- Open: `Client.version` is written by the reconnect loop and read by the health monitor (`VersionInUse`) in another goroutine — a pre-existing data race (old `refreshVersion` had it too), now reachable on every `ClientOutdated`; not fixed here (outside B2/M3 rulings).

## Phase 0 fixes — H18 (unpaired run), M13 a/b (pair feedback), B2 carry-overs

- Test commits `39d45e6`, `b43e550` (fixture fix: the paired test device needed a valid ADV account for `PutDevice`; it was hidden behind an earlier failure in the stub run); fix commit `985fbf8`.
- H18: `wa.IsPaired(ctx, dataDir)` opens `session.db` via sqlstore (a missing file is unpaired and not created) and reports `Store.ID != nil`; `run` calls it before any network access. Unpaired → stderr `not paired: run \`archiver pair\` in a terminal first`, one ntfy alert ("WhatsApp Archiver is not paired") when `alerts.ntfy` is set (failure reported, not fatal; 1-minute cap), exit `exitNotPaired` = 3; `deploy/archiver.service` has `RestartPreventExitStatus=3`. Probe `TestProbeRunWithEmptySessionFile`: before `exit=1 stderr="not paired"` (review), after `exit=3 stderr="not paired: run \`archiver pair\` in a terminal first"`.
- M13: `pairLine` maps `PairSuccess` → "Paired. Capture is running.", `PairError` and QR `error`/`err-*` items → "Pairing failed: …", QR `timeout` → re-run hint; printed from the event handler (pairing) and the QR printer. The print after `Run` returns is gone.
- B2 carry-overs: `forwardControl` extracted and table-tested (now also `ConnectFailure`, `CATRefreshError`); policy maps both to disconnect + fixed 30-minute wait + connect; `Pipeline.Handle` maps `ConnectFailure` to `SetState("disconnected", "<reason> <message>")`; logout test holds a 200 ms window; loop-level same-version 30-minute test; `sleepCtx` cancel test.
- Coverage: cmd/archiver 22.1%, internal/wa 57.5%, internal/ingest 65.8%.
- Mutations killed: x10, h18-exit, h18-paircheck, h18-ntfy, m13-early, b2-logout-returns, b2-forward-ban, cf-idle, cf-policy, cf-ingest (10/10).
- Deviations: m13-early is expressed as "PairSuccess no longer mapped to the line" (a single-site mutant cannot both remove the event print and re-add a print after `Run`, and the after-`Run` path needs a live connection to observe). `IsPaired` runs sqlstore's schema upgrade on an existing (possibly empty) `session.db`; it never creates a missing one.

## Phase 0 fixes — H5 (history messages parsed like live ones)

- Test commit `b0548c9`; fix commit `0d568b2`. `internal/wa/history.go`: `NewHistoryParser(ownPN, ownLID)` (an unconnected `whatsmeow.NewClient` over a `store.Device` holding only `ID`/`LID`; verified in the pinned source that `NewClient` only allocates and `ParseWebMessage` reads only `Store.GetJID`/`GetLID`) and `(*wa.Client).HistoryParser()` (the live client, so own IDs are read at parse time — this also covers history arriving during `pair`, before which `Store.ID` is nil). Both wrap `ParseWebMessage`; an unwrapped `MESSAGE_EDIT` is detected first and kept as the edit message under the edit's own key ID, so the normaliser still produces an edit event targeting the original. `ingest.HistoryParser` is the func type; `Pipeline.WithHistoryParser` sets it; `serve` wires `client.HistoryParser()`. `ImportHistory` has no hand-built `events.Message` any more: wrapped messages (ephemeral, view-once, document-with-caption) are unwrapped, own messages carry our JID, `PushName` reaches `participants`, `key.participant`/thread metadata come from whatsmeow. A message the parser rejects (no sender, `ErrNotLoggedIn`) is counted as skipped and logged at Warn with chat and ID; the chunk goes on.
- Coverage: internal/ingest 70.3%, internal/wa 58.0%, cmd/archiver 22.1%.
- Mutations killed: h5-nounwrap, h5-sender, h5-pushname, h5-edit (4/4).
- Probes (scratch copy, probes wired to the real parser and group keys given a participant, as whatsmeow requires): `TestProbeHistoryWrappedMessagesLoseContent` before `[DOC|| EPH||]`, 0 media → after `[DOC|syllabus|document EPH|disappearing hello|]`, 1 media; `TestProbeHistoryOwnMessageSender` before senders = contact / group JID, participants include `120363@g.us` → after both `111@s.whatsapp.net`, participants only our JID.
- Deviations: red commit fails to compile on the new `wa.NewHistoryParser` / `WithHistoryParser` API; behavioural failures of all 6 new tests shown against a stub in a scratch copy. The shared `history()` fixture now gives its group key a participant (whatsmeow rejects a group message with no sender); existing history tests get the parser through `historyPipeline` / `newPipeline`. Extra tests: `TestHistoryUnparseableMessageIsSkippedAndLogged` (ruling 3), `TestClientHistoryParserUsesOwnJID`. ImportHistory without a parser returns an error (untested guard).

## Phase 0 fixes — H6 (chat names kept, excluded chats never stored) and B1/H2 carry-overs

- Test commit `18b31ef`; fix commit `e29458d`. `UpsertChat` sets `name=COALESCE(NULLIF(excluded.name,''),name)`, so a nameless live message no longer wipes a name learned from history. `ImportHistory` writes a conversation's chat row only when `filter.Keep(chat, KindMessage)` (aliases included), still through `persist`; nothing of an excluded chat reaches `chats` or `messages`.
- Carry-overs: `UpsertChat`, `UpsertParticipant`, `InsertMedia` and `InsertMessage`'s alias-check and commit errors go through `store.classify`; `persist` switches to the permanent path (log once, return the error) when a constraint error appears inside the retry-forever loop (`rejected` helper shared with the existing permanent path; loop text unchanged so earlier mutants still apply); new store tests use a real SQLite trigger from a second connection: `RAISE(ABORT)` → `ErrConstraint` through `insert()` (`InsertEvent`) and through the three upserts/media insert, and an integer-overflow trigger → a real non-constraint `*sqlite.Error` not classified.
- Coverage: internal/ingest 71.1%, internal/store 48.7%.
- Mutations killed: h6-wipe, h6-excluded, h6-classify, plus h6-classify-participant, h6-classify-media, h6-loop-perm (6/6).
- Probes (scratch copy): `TestProbeChatNameWipedByLiveMessage` before `[g@g.us|]` after history and after the live message → after `[g@g.us|Study Group]` both times; `TestProbeExcludedChatNameStoredFromHistory` before `chats = [secret@g.us|Private Therapy Group]` → after `chats = []`.
- Deviations: none. The alias-check/commit classify paths cannot produce a constraint error with the current schema (no deferred foreign keys), so they have no test of their own. `UpsertParticipant` still overwrites `display_name` with an empty push name (not in H6's scope; noted as an open question).

## Phase 0 fixes — H9 (unique history ledger identity) and missing Task 6 tests

- Test commit `7ba2f46`; fix commit `01e9355`. A ledger row's ID is now a hash of the chunk's deterministic protobuf encoding instead of `(sync type, chunk order, progress, conversation count)`: two different chunks never collide (even when received in the same second, which an ID with `received_at` would not survive), an identical redelivered chunk is still ledgered once, the ledger stays insert-only and the schema is unchanged.
- Added the Task 6 tests that were missing: `TestMappingsInChunkApplyToItsMessages`, `TestNonsensicalHistoryTimestampStoredAsGiven`, `TestCoverageReportsOldestPerChat` (store); `TestLateChunkAfterCompleteIsStored` now also checks both chunks' messages are stored. These passed when written (coverage gaps, proven by mutations rf8d, rf9c, rf9d).
- Coverage: internal/ingest 71.2%, internal/store 59.3%, internal/wa 58.0%, cmd/archiver 22.1%.
- Mutations killed: rf8d, rf9c, rf9d, h9-collide (4/4). Full stage-2 run of every spec at `01e9355`: 45/45 KILLED.
- Probe `TestProbeLedgerCollision`: before 1 ledger row for two distinct chunks at progress 100 → after 2.
- Deviations: neither review option (`received_at` in the ID, autoincrement key) was used — the content hash is one line, needs no schema change and survives same-second chunks; dedup of an identical redelivery is untested.

## Phase 0 fixes — H6b (review follow-ups to H5/H6/H9)

- Test commit `0cffc31`; fix commit `6495743`. `UpsertParticipant` sets `display_name=COALESCE(NULLIF(excluded.display_name,''),display_name)`, so a message without a push name no longer wipes a known one. The chunk-ID encoding uses `proto.MarshalOptions{Deterministic: true, AllowPartial: true}`; an encoding error is wrapped and returned from `ImportHistory` before any write (nothing counted).
- New tests: `TestEmptyPushNameKeepsKnownDisplayName` (red: `participants=2@lid|`), and pinning tests that passed when written: `TestIdenticalChunkTwiceIsOneLedgerRow` (same chunk an hour apart → 1 ledger row, 1 message), `TestExcludedHistoryChatLeavesNoParticipantsOrMedia` (excluded chat with an image and a push name → no participants, no media rows, nothing queued), `TestHistoryFromMeInLIDChatHasOwnLIDAsSender` (own message in a `@lid` DM → `222@lid`).
- Coverage: internal/ingest 71.1%, internal/store 59.3%.
- Mutations killed: h6b-participant, h9-dedup (2/2). Full run of all specs at `6495743`: 47/47 KILLED.
- Deviations: the encoding-error branch has no test. `HistorySync` is proto2, so with `AllowPartial` there are no required-field checks and no UTF-8 validation, and no cheap input makes `Marshal` fail.
