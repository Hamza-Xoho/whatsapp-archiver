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

## Phase 0 fixes — H1 (message insert cost independent of table size)

- Test commit `d678734`; fix commit `2d879f6`. `InsertMessage`'s duplicate check is the unexported const `dedupeQuery`: it resolves the one chat's aliases from `id_mappings` ({chat} ∪ lids of that phone number ∪ phone numbers of that lid ∪ other phone numbers of those lids, same set `chat_aliases` implies) and probes `messages` by `(chat_jid IN (…), id)` on the primary key, in the same write transaction. The `chat_aliases` view stays for read paths. Added `CREATE INDEX IF NOT EXISTS id_mappings_pn ON id_mappings(pn, lid)` to `0001_raw.sql`; M11 migrations are deferred and no deployed DB exists, so editing the baseline schema is safe.
- Tests (in-package, `insert_cost_test.go`): `TestInsertMessageCostDoesNotGrowWithTableSize` (EXPLAIN QUERY PLAN of `dedupeQuery` has no `SCAN messages`/`MATERIALIZE`; last 1,000 of 20,000 inserts take < 4x the first 1,000), `TestInsertMessageDuplicateAcrossSiblingPhoneNumbers` (pn→lid→other pn).
- Mutations killed: h1-view, rf1b, h1-noalias (3/3).
- Probe (scratch copy, 30k rows, inserts/s per 5k batch): before 2080, 1173, 807, 605, 493, 410 → after 5865, 6160, 6197, 6063, 6184, 6196.
- Other write paths: `UpsertChat`, `UpsertParticipant`, `InsertEvent`, `InsertChatEvent`, `InsertMedia`, `InsertIDMapping` are PK upserts/inserts, no view. `open_gaps` is read-only. Open item: `store.Aliases` (view-based) runs on every event through `ingest.Filter.Keep` (`cmd/archiver/main.go` wiring) and still materialises `chat_aliases` per event.
- Follow-up rulings: (1) the scaling test now compares 100 inserts into an empty archive with 100 inserts after bulk-seeding 20,000 rows (4 s under `-race`); a 4,000-insert first-500/last-500 version passed against the old view query (ratio ~3, 64 s under `-race`), so it could not be red; the seeded version fails the old query at ratio ~14 (0.69 s vs 9.9 s). Commit `525622f`. (2) `store.Aliases` now shares `aliasSet` with `dedupeQuery` (`aliasesQuery`); test `TestAliasesQueryUsesIndexLookupsOnly` (no MATERIALIZE/CO-ROUTINE and no table/index SCAN in the plan; the old view-based query did not scan `messages` thanks to predicate push-down but scanned `chats` and `id_mappings` per event). Test commit `388889d`, fix commit `3f73b22`. Aliases of a phone number with several lids is now the union over all of them (the view picked one arbitrary canonical).
- Mutation h1-aliases-view added, KILLED; full spec file at `3f73b22`: 51/51 KILLED. Coverage unchanged apart from store; store tests 9 s under `-race`.

## Phase 0 fixes — H8 (only a real revoke is a revoke; both edit shapes)

- Test commit `c5fec02`; fix commit `1891941`. `normalizeContent` switches on `ProtocolMessage.Type`: `MESSAGE_EDIT` → `KindEdit` (target = key ID, `NewText` = new text), `REVOKE` → `KindRevoke`; every other type (EPHEMERAL_SETTING, HISTORY_SYNC_NOTIFICATION, APP_STATE_SYNC_KEY_SHARE, …) → new `KindProtocol` message event, `Value` = type name, target = key ID if any, raw proto kept. A protocol message with no `Type` (proto2 default would read as REVOKE) is also `protocol` with empty `Value`. The top-level `EditedMessage` wrapper (what whatsmeow's `BuildEdit` sends; `UnwrapRaw` sets `IsEdit` and leaves the inner MESSAGE_EDIT protocol message) is unwrapped before classification, so a not-yet-unwrapped raw message normalises the same way.
- Tests: `TestProtocolEditOldAndNewFormats` (bare MESSAGE_EDIT, EditedMessage wrapper, wrapper after `UnwrapRaw`; red on the wrapper), `TestNonRevokeProtocolMessageIsNotARevoke` (four types + untyped, and through `Handle` into the store; red: `Kind=revoke`), and coverage tests that passed when written: `TestRevokeNormalizes`, `TestReactionNormalizes`, `TestPinNormalizes`, `TestPollVoteCurrentAndNewVersions`.
- Poll versions in the pinned proto: creation has `PollCreationMessage`, `V2`, `V3`, `V5`, `V6` (plus `PollAddOptionMessage`, `PollResultSnapshotMessage(V3)`); there is only one vote shape, `PollUpdateMessage{PollCreationMessageKey, Vote(PollEncValue), Metadata, SenderTimestampMS}`. The "newer version" case is therefore a vote on a `PollCreationMessageV5` poll.
- Coverage: internal/ingest 75.9%.
- Mutations killed: rf2a, rf2b, rf2c, h8-anyrevoke, h8-untyped, h8-newedit (6/6).
- Probe `TestProbeProtocolMessageMisclassified`: before `Kind=revoke EventKind="revoke"` → after `Kind=protocol EventKind="protocol"`.
- Deviations: `KindProtocol` added (ruling 1). Protocol events have empty `NewText` (`event()` copies the value into `NewText` for every kind; reactions and pins still do). `PollAddOptionMessage` and poll creation content are not normalised (stored as a message with raw proto). An edit's `NewText` only reads conversation / extended text, not edited media captions.

## Phase 0 fixes — H7 (chat events keep payload, timestamp and a deterministic ID)

- Test commits `7b42d86`, `eb43717`; fix commits `bd796ff`, `48bf903`. `FromChatEvent` moved to `chatevents.go`. `Value` is compact JSON from two local structs (ruling 5): group events (`GroupInfo`, `JoinedGroup`) store only the changed fields — notify, sender, reason, type, name, topic, announce, locked, ephemeral (disappearing timer seconds), join/leave/promote/demote as JID strings; archive/mute/pin store `action`, the state after it (`archived`, `muted` + `mute_end`, `pinned`) and `from_full_sync`. `RawProto` = `proto.Marshal(Action)` for app-state events, empty for group events (no proto). `ts` = the event's `Timestamp` (0 when zero; `JoinedGroup` has no timestamp, so 0). `SenderJID` from `Sender` when present. `EventID` = `stableID(chat, kind, JSON, ts)`.
- Rule (ruling 4): a `GroupInfo` with any join/leave/promote/demote is `KindParticipants`, even if it also renames etc.; its JSON keeps every changed field, so nothing is lost. `JoinedGroup` stays `KindGroupInfo`; app-state events stay `KindAppState`.
- An archive/mute/pin event whose `Action` is nil (whatsmeow passes `mutation.Action.Get…Action()`, which can be nil) is stored with only `action` and `from_full_sync`; it panicked in the first green (caught by the guard, event dropped), hence the second cycle.
- Tests: `TestChatEventsStored` (all five event types through `Handle` into the store; exact kind, sender, ts, JSON and raw hex), `TestSameGroupInfoTwiceIsOneRow`, `TestArchiveOnAndOffAreDistinct`, `TestAppStateEventWithoutActionIsStored`; all red before.
- Coverage: internal/ingest 83.9%.
- Mutations killed: y3, h7-ptr, h7-value, h7-ts, h7-participants, h7-sender, h7-raw, h7-nilaction, h7-zerots (9/9). Full spec file at `48bf903`: 66/66 KILLED. `h7-ptr` models a per-delivery ID with `time.Now().UnixNano()` (the mutant cannot add a `fmt` import).
- Probe `TestProbeChatEventShape`: before same EventID twice `false`, `Value=""`, `Timestamp=0`, archive true/false both `"archive"` → after `true`, `Value={"name":"New Name","join":["5@s.whatsapp.net"]}`, `Timestamp=500`, archive values distinguishable (`RawProto` 0 bytes is expected for a group event).
- Deviations / open: `JoinedGroup` has no WhatsApp timestamp, so two identical joins of the same group are one row. A `from_full_sync` re-emission of an unchanged state is a separate row (the flag is in the JSON, per ruling 4). Group fields outside the ruling's list (`Delete`, `Link`/`Unlink`, `NewInviteLink`, `MembershipApprovalMode`, `Suspended`, `UnknownChanges`, `JoinReason`) are not stored; a `GroupInfo` carrying only those has an empty-ish JSON and collapses with others at the same second.

## Phase 0 fixes — H10 (every planned health alert rule, alertGate with recovered messages)

- Test commit `8bb8429`; fix commit `7e79a07`. One `alertGate` (`fire(key, title, body)` at most once per 6 h per key while active; `clear(key, title)` one "Recovered: <title>" message if it had fired, then reset) is used by every rule. New rules: `disconnected` fires from `check()` while the state is `disconnected` for longer than `DisconnectedAfter`, measured from when the connection was lost (from monitor creation before the first `connected`); `phone_quiet` fires when `PhoneLastActive` (or creation) is older than `PhoneQuietAfter` with "open WhatsApp on your phone to avoid the 14-day unlink". `no_events` now measures from creation when nothing has been seen. Clearing: condition rules (`no_events`, `phone_quiet`, `disk`) clear in `check()` when the condition stops holding and on `Seen` (`phone_quiet` only for `fromMe`); `disconnected` and the immediate state alerts clear on `connected`; `backup` clears on the next successful `BackupResult`; `write_failed` and `panic:<where>` are gated only. The temp-ban body is "Temporarily banned until <expiry>; capture is paused".
- Tests (`internal/health/alerts_test.go`, package `health`, monitor running under `Run` so they also hold after M2): `TestLoggedOutAlertsOnceAndIsReported`, `TestOutdatedAlertNamesVersion`, `TestTempBanAlertIncludesExpiry`, `TestStateAlertRecoversWhenConnected`, `TestDisconnectedLongerThanThresholdAlerts`, `TestDisconnectedBeforeFirstConnectMeasuredFromStart`, `TestRecoveredMessageAfterEventsResume`, `TestPhoneQuietAlert`, `TestDiskBelowThresholdAlertsOnceThenRecovers`, `TestWriteFailedAndPanicAlertOncePerSixHours`, `TestFailedBackupAlertNamesErrorThenRecovers`, `TestHealthJSONRoundTrip`, `TestRunLeavesNoGoroutines` (goleak); `TestNtfyPostsToTopicPath`; `internal/ingest` `TestConnectionEventsReportHealthStates`. Red: 8 (temp ban "paused", all recovered cases, disconnected ×2, phone quiet, no events from start); the rest pinned behaviour that already held. `go.uber.org/goleak v1.3.0` added (module cache in `.cache/gomod`).
- Coverage: internal/health 92.2%, internal/ingest 85.4%.
- Mutations killed: rf4a, rf4b, rf4c, rf4d, rf5b-health, rf5c, rf6b, rf10d, rf10e, h10-disc, h10-quiet, h10-recovered, h10-downstart, h10-noevents-start (14/14).
- Probes: `TestProbeDisconnectedNeverAlerts` before 79 × "Capture may be stalled" only → after 80 × "Disconnected for …", 79 × "No events for …", 40 × phone-quiet (first at 241 h); `TestProbeNoRecoveredMessage` before `[Capture may be stalled]` → after `[No events for 2h0m0s; capture may be stalled, Recovered: No WhatsApp events]`.
- Deviations / open: `disconnected` clears only on `connected` (not when the state moves on to e.g. `logged_out`, which has its own alert), so no misleading "recovered". The rule applies to state `disconnected` only (ruling 2): if the first connect never produces any event (state stays empty) nothing alerts except no-events. `Seen` uses the receive time, so a `fromMe` message from a history sync also counts as phone activity (unchanged). Notify errors are still ignored.

## Phase 0 fixes — M2 (ntfy Notify off the event-handler path)

- Test commits `5bb89af`, `42a0396`; fix commit `d338503`. `alertGate` now hands alerts to `Monitor.notify`, which enqueues without blocking on a bounded channel (capacity 16); when full the alert is dropped and logged at Error (title and body). `Run` owns one sender goroutine that calls `Notify` with Run's context (SIGTERM cancels an in-flight send) and returns only after joining it. Alerts raised before `Run` starts wait in the queue. Once the context ends, the sender passes what is still queued to `Notify` with the ended context (a real ntfy send then fails at once) and exits; this keeps shutdown bounded and makes the stop-then-assert tests deterministic. `Monitor.WithLogger` (default `slog.Default()`) is wired in `serve`. `alertNotPaired` (H18) still uses `NewNtfy` directly and is unchanged.
- Tests (`internal/health/queue_test.go`): `TestSlowNotifierDoesNotBlockSetState` (a notifier that never answers: `SetState`/`WriteFailed`/42 alerts return; `Run` does not return while Notify is in flight, does after; Notify saw `context.Canceled`; red: blocked 5 s), `TestFullAlertQueueDropsAndLogs` (20 alerts without Run → exactly 4 Error logs naming the alert; red: build failure, `WithLogger` missing), `TestAlertsRaisedBeforeRunAreSent` (pinning). `TestImmediateStateAlertsAreGated` now runs the monitor and stops it before asserting (assertions unchanged). `42a0396` makes the full-queue test fail in 5 s instead of hanging when alerts block.
- Coverage: internal/health 92.5%.
- Mutations killed: m2-sync, m2-nojoin, m2-bgctx, m2-block, m2-nolog (5/5). Full spec file at `42a0396`: 85/85 KILLED (h10-downstart's match string was updated for the M2 constructor line and re-run).
- Probe (scratch, httptest ntfy that hangs): before `SetState` still blocked after 3 s → after it returns in 1.3 ms and `Run` returns 0.5 ms after cancel. The H10 probes call `check()` without `Run`, so after M2 they send nothing until `Run` runs; with `Run` added to the probe the output is the same as after H10 (80 disconnected, 79 no-events, 40 phone-quiet; no-events then recovered).
- Open: Notify errors are still discarded (not logged); `serve` does not wait for `mon.Run` to return before exiting.

## Phase 0 fixes — H7/H8 fix round 1 (review I1, I2, I3, M2, M3, M6)

- Test commit `5feb288`; fix commit `e8aa845`.
- I1: `groupChange` now also carries `delete` (`*types.GroupDelete`), `link`/`unlink` (`*types.GroupLinkChange`), `invite_link`, `membership_approval`, `suspended`/`unsuspended`, `join_reason`, `version` (`ParticipantVersionID`) and `unknown` (each `UnknownChanges` node's `String()`; attributes are sorted, so the text is deterministic).
- I2: `JoinedGroup` adds `version`, `create_key` and `created` (`GroupCreated`, unix seconds). A repeat join with a new participant version is a new row.
- I3: a `SecretEncryptedMessage` with `SecretEncType` MESSAGE_EDIT is `KindEdit`. Its target is `TargetMessageKey.ID`, `NewText` is empty (the content is encrypted), the raw proto is kept, and no message row is written. The other secret types (EVENT_EDIT, POLL_EDIT, POLL_ADD_OPTION, MESSAGE_SCHEDULE) are unchanged.
- M2: the chat-event `stableID` uses `UnixMilli`; the `ts` column stays in seconds.
- M3: `messageText` falls back to image, video and document captions.
- M6: protocol types APP_STATE_SYNC_KEY_SHARE, SHARED_DEVICE_CONTACT_HASH_KEY_SHARE, HISTORY_SYNC_NOTIFICATION (history blob `mediaKey` + `directPath`) and PEER_DATA_OPERATION_REQUEST_RESPONSE_MESSAGE (media keys, web message bytes) are stored as `protocol` events with empty raw_proto. `TestNonRevokeProtocolMessageIsNotARevoke` was changed in the test commit to expect no raw proto for its two key-carrying cases, per the M6 ruling (the expectation is stricter, not weaker).
- Tests (all red first): `TestApprovalModeChangesAreDistinct`, `TestGroupDeleteStoredWithReason`, `TestGroupInfoKeepsEveryChangeKind`, `TestRepeatJoinedGroupIsDistinct`, `TestArchiveToggleWithinOneSecondIsThreeRows`, `TestKeyShareProtocolMessageKeepsNoRawProto`, `TestSecretEncryptedEditIsAnEdit`, `TestEditedMediaCaptionIsNewText`.
- Coverage: internal/ingest 86.2%.
- Mutations: added h7-groupfields, h7-approval, h7-unknown, h7-joinversion, h7-secretedit, h7-ms, h7-caption and h8-keyraw. The h7-ptr, h7-ts and h7-zerots spec text was updated to the millisecond code. Full spec file at `e8aa845`: 93/93 KILLED.

## Phase 0 fixes — H10/M2 fix round 1

- Test commit `aaa4d74`; fix commit `8585f73`. Every failed `Notify` is logged at Error ("alert not sent", title, err). `notify` returns whether the alert was queued and `alertGate.fire` records the key only then (under the gate lock; queuing never blocks), so a dropped alert fires again on its next trigger. `no_events` is skipped (neither fired nor cleared) while the state is not `connected`; the disconnected rule covers that time. The temp-ban body reads the detail as a duration: "Temporarily banned for 24h0m0s (until 2023-11-15T22:13:20Z); capture is paused" (clock + duration, RFC3339 UTC); an unparseable detail is quoted verbatim ("Temporarily banned: <detail>; capture is paused").
- Tests: `TestFailedNotifyIsLoggedOncePerSend`, `TestDroppedAlertFiresAgainOnNextTrigger`, `TestNoEventsSilentWhileDisconnected`, `TestTempBanAlertSaysUntilWhen` (all red). `TestRecoveredMessageAfterEventsResume` now starts `connected` (ruling); assertions unchanged.
- Coverage: internal/health 93.0%.
- Mutations killed: m2-swallow, m2-dropfired, h10-noevents-while-disc, h10-tempban-until, h10-tempban-verbatim; rf4b, h10-noevents-start, m2-sync, m2-bgctx match strings updated for the new code. Full spec file at `8585f73`: 98/98 KILLED.
- Probe (with `Run`): 20 days disconnected → 80 disconnected, 0 no-events (was 79), 40 phone-quiet. `TestProbeNoRecoveredMessage` never sets a state, so it now sends nothing (no-events only applies while connected).
- Open: before the first `connected` (state empty) neither no-events nor disconnected alerts.

## Phase 0 fixes — H10 fix round 2 (never-connected startup)

- Test commit `6483e86`; fix commit `f48e20f`. The disconnected rule also fires while the state is still empty (never connected), measured from monitor creation, and clears with one "Recovered" on the first `connected`. No-events stays connected-only.
- Test: `TestNeverConnectedStartupAlertsDisconnected` (red: no alert). Mutation h10-neverconnected KILLED; h10-disc match string updated. Full spec file at `f48e20f`: 99/99 KILLED.

## Phase 0 fixes — H11 (media-worker panic marks the row failed and is reported)

- Test commit `ec7edd4`; fix commit `1a38d19`. `NewQueue` takes `onPanic func(string)` (nil allowed); `serve` passes `mon.Panic`. Each worker keeps its current row; the guard's callback (one `guard.Run` per worker pass, unchanged boundary) sets that row `failed` with reason `panic` through the store and calls `onPanic("media")` once; the worker then goes on with the next row. Media row writes that fail are logged at Error with chat, id and state (no second retry loop; the row is re-claimable after a restart).
- Tests (`internal/media/worker_test.go`, helpers in `helpers_test.go`): `TestWorkerPanicMarksRowFailedAndIsCounted` (red: poison row stayed `downloading`; compile-only red for the new argument, behavioural failure shown against a scratch stub that ignored it), and pinning tests that already held: `TestPendingAndDownloadingRowsRecoverAfterRestart`, `TestMediaMetadataRecorded` (layout path, sha256, `downloading` while claimed), `TestLiveMediaBeforeHistoryBacklog`. New tests wait by polling the store and stop by cancelling and waiting for `Run`; no sleeps.
- Coverage: internal/media 73.3%.
- Mutations killed: rf10c, rf10d-media, h11-row, x4, x5, x6, x7 (7/7).
- Probe `TestProbeWorkerPanic`: before poison row `state="downloading" reason=""` → after `state="failed" reason="panic"`; sibling `done` both times.

## Phase 0 fixes — H3 (a valid media re-upload request)

- Test commit `f36ab89`; fix commit `a67c164`. The `media` table gains `from_me` (edited into `0001_raw.sql`: no deployed database, M11 migrations deferred); `setMedia` copies `Result.FromMe`. `wa.MediaRef` is now the row's identity (`Chat`, `Sender`, `ID`, `FromMe`). `wa.RequestMediaRetry` builds `types.MessageInfo{MessageSource{Chat, Sender, IsFromMe, IsGroup: chat server g.us}, ID}` and takes the media key from the raw proto via `retryReceipt` (pure, unit-tested); errors are wrapped. One helper `rawMedia` decodes the raw proto, unwraps it with whatsmeow's `UnwrapRaw` (ephemeral, view-once, document-with-caption, device-sent, ...) and finds the media; `Download`, `ApplyMediaRetry` and the request all use it, so wrapped media now downloads too (before, a wrapped raw proto had "no downloadable media"). A row without a media key → `failed` with reason `no media key` (`wa.ErrNoMediaKey`); any other request error leaves the row `expired` and is logged. A `MediaRetry` answer that cannot be applied is logged.
- Tests: `internal/wa` `TestRequestMediaRetryReceivesMessageInfoAndKey` (group + wrapped document, 1:1 own ephemeral image), `TestRequestMediaRetryWithoutKeyIsErrNoMediaKey`; `internal/media` `TestExpiredMediaRetryCompletes` (one request with the row's ref and raw; `OnRetry` → downloaded from path `new` → `done`), `TestExpiredMediaWithoutKeyFails`; `internal/ingest` `TestMediaRowKeepsFromMe`. All red (compile-only for the new fields; behavioural failure shown against a scratch stub).
- Coverage: internal/media 87.6%, internal/wa 62.1%, internal/ingest 86.2%, internal/store 59.3%.
- Mutations killed: h3-emptyref, h3-key, h3-isgroup, h3-unwrap, h3-fromme, h3-fromme-store, h3-nokey (7/7).
- Probe `TestProbeExpiredRateLimit` (first ref line, adapted to the new `MediaRef`): before `Info.ID="" Info.Chat="" MediaKey=0 bytes` → after `{Chat:g@g.us Sender:s@lid ID:M00 FromMe:false}` (the key comes from the raw proto inside `wa`; the probe's raw is not a proto).

## Phase 0 fixes — H4 (rate-limited re-upload requests with a refill pass)

- Test commit `80250e6`; fix commit `26958df`. A download that ends in `wa.ErrMediaExpired` only marks the row `expired`. A request pass runs in the worker loop after each wake/tick (inside the same `guard.Run`; one pass at a time via `TryLock`, no new goroutine): it fails `retry_requested` rows older than 24 h (`store.FailStaleRetries`, also used by `ResetMediaOnStart`), then takes `expired` rows (`store.NextExpiredMedia`, live first, newest first) while `limiter.AllowN(clock.Now(), 1)` allows, and marks each `retry_requested` (or `failed: no media key`). Rows over the limit stay `expired` for a later pass, including after a restart. A request that fails for another reason leaves the row `expired` and ends the pass (likely not connected). The media column list and queue order are now one constant each in `store` (three readers).
- Tests (`internal/media/ratelimit_test.go`): `TestExpiredRequestsAreRateLimited` (30 expired, 10/min: exactly 10 at T0 after 20 further clock reads, 20 after the fake clock advances 1 min, 30 after a restart a minute later; each row requested once) and `TestUnansweredRetryRequestFailsAfterADay`; both red (no refill; stale request only failed on restart).
- Coverage: internal/media 86.6%, internal/store 55.3%.
- Mutations killed: rf3b, rf3c, h4-wallclock, h4-norevisit, h4-stale (5/5); x4 match string updated for the shared order constant (KILLED).
- Probe `TestProbeExpiredRateLimit` (restart line adapted to print requests and states): before restart `re-upload requests sent=2` (only 2 rows that were mid-download at cancel), 18 rows left `expired` → after restart `re-upload requests sent=10`, states `expired:10 retry_requested:20` (the probe's clock never advances, so the last 10 wait for the refill).

## Phase 0 fixes — M4 (seconds-scale backoff and a later re-queue; streaming and extension table deferred)

- Test commit `fe0c66c`; fix commit `ec8106b`. A download round is `retry.Do(Policy{Max: 5, Base: 1 s, Sleep: q.sleep})` (waits 1+2+4+8 s; `q.sleep` is injectable, nil means the context-aware timer). `wa.ErrMediaExpired` is permanent within a round (one download, then the re-upload path), as the plan's "retry.Do for transient errors" implies. After a round fails with any other error: if the row's total attempts are below 20 it goes back to `pending` with the new column `next_attempt_at` = now + 15 min on the queue clock (edited into `0001_raw.sql`, see H3); otherwise `failed` with the reason. `ClaimMedia(ctx, now)` claims only rows with `next_attempt_at <= now`. Cost (ruling 4): a permanently bad file is tried in 4 rounds over about 45 min before `failed`.
- Tests (`internal/media/backoff_test.go`; `newTestQueue` injects a no-op sleep so no test really sleeps): `TestTransientFailureIsRetriedAfterBackoff` (waits exactly 1, 2, 4, 8 s; `pending`, attempts 5, `next_attempt_at` = now + 900 s, not claimed during 20 further clock reads, `done` with attempts 6 after the clock advances 15 min), `TestAlwaysFailingDownloadFailsAfterTwentyAttempts` (4 rounds, `failed`, reason kept, 20 downloads), `TestExpiredMediaIsNotRetriedWithinARound`, `TestAttemptsCountedAcrossRetries` (pinning: fails twice, `done`, attempts 3). Red: compile-only for `q.sleep`/`NextAttemptAt`, behavioural failure shown against a scratch stub (the first three failed).
- Coverage: internal/media 91.7%, internal/store 55.3%, internal/wa 62.1%, internal/ingest 86.2%.
- Mutations killed: m4-terminal, rf3a, rf3d, m4-notbefore, m4-base, m4-perm, m4-sleep, m4-requeue (8/8). rf10c and h6-classify-media match strings updated for the H4/H3 code.
- Probe `TestProbeBackoffWindow`: before `attempts=5 spanning 153ms, then state="failed"` → after (1 s window) `attempts=2 spanning 1s, state="downloading"` (still inside the round); with a 16 s window `attempts=5 spanning 15.005s, then state="pending" attempts=5 next_attempt_at-now=900s`.
- Open: a cancelled context during a round is treated like a transient failure; the save then fails on the cancelled context (logged) and the row stays `downloading` until the restart reset. `TestDocumentDownloads` (pre-existing, 50 ms real sleep) failed once under parallel load during the H4 green and passed on four re-runs; left unchanged per the brief.

## Phase 0 fixes — media fix round 1 (I-1 bounded re-upload, I-2 reset error logged)

- Test commit `5a912af`; fix commit `6663f1c`. The request pass now fails, before taking a limiter token and without sending anything, an `expired` row that already has a `RetryDirectPath` (reason `re-uploaded media expired`) or `Attempts >= 20` (reason `download attempts exhausted`); so a message gets one re-upload request, and a crash loop cannot re-request it. `ResetMediaOnStart` errors are logged at Error (`stuck media rows not reset at start`).
- Tests (`internal/media/bound_test.go`): `TestReuploadedMediaThatExpiresAgainFailsAfterOneRequest`, `TestExpiredRowAtAttemptCapFailsWithoutRequest`, `TestResetMediaOnStartFailureIsLogged` (Run with a cancelled context so only the reset runs). Red: requests = 2; requests = 1; no Error record.

## Phase 0 fixes — media fix round 2 (request pass cannot spin on an unsaveable row)

- Test commit `9ca2c95`; fix commit `6bfa971`. `save` now reports whether the write succeeded; when failing an exhausted expired row cannot be saved, `requestPass` returns (the row stays expired, the next wake/tick retries) instead of re-reading it in a hot loop holding the pass lock.
- Test: `TestRequestPassEndsWhenFailingARowCannotBeSaved` (a SQLite trigger aborts updates to `failed`; red: pass still looping after 2 s).
## Phase 0 fixes — H16 (retention removes only snapshot folders) and H15 (service can write the backup path)

- H16: test commit `3824f8e`; fix commit `b9ee812`. `snapshotLayout` is the one layout `Run` writes and retention parses with; retention keeps the newest `Keep` folders by parsed time and ignores files and foreign folders. Any failure after the snapshot folder exists (open, VACUUM, Verify, Stat, retention) removes that folder; errors are wrapped with context.
- H15: test commit `db811df`; fix commit `fcef416`. `ReadWritePaths` now lists the data folder and `/srv/backups/whatsapp-archiver`; the guide says a different destination must be added there too and gives the `install -d` command. `TestServiceUnitCanWriteDataAndBackupFolders` (`cmd/archiver/unit_test.go`) reads the unit's `--data` and `ReadWritePaths` and the guide's `destination:` value, so they cannot drift.
- Tests (`internal/backup/backup_test.go`): `TestRetentionOnlyRemovesSnapshotFolders` and `TestFailedSnapshotLeavesNoFolder` were red; `TestKeepTwoLeavesTwoSnapshots` already held (it is the x13 killer). Task 8 gaps added and green on arrival: `TestSnapshotRowCountMatchesSource`, `TestSnapshotDuringConcurrentInserts`, `TestScheduleFiresOnceAtConfiguredTime` (fake clock; first wait 30m, one run), `TestDestinationNotWritableReturnsError`, `TestVerifyRejectsFailedIntegrityCheck` (index root page replaced by an empty leaf: file opens, integrity_check fails; kills x12).
- Coverage: internal/backup 81.7%.
- Mutations (`specs-backup.json`) 6/6 KILLED: x12, x13, h16-foreign, h16-failed, h15-rwpath, backup-schedule-twice. Shared `specs.json` at `fcef416`: 112 KILLED, 1 NOMATCH (`h6-classify-media`, store.go; its match string is not in this branch's base).
- Probe (scratch copy): `TestProbeRetentionDeletesForeignDirs` now leaves `0-keep-me`, `2023-family-photos`, `media` and the fresh snapshot; `TestProbeFailedSnapshotLeavesJunkFolder` leaves 0 folders.

- H16 fix round 1: test `TestRetentionFailureKeepsNewSnapshot` (old snapshot folder chmod 0500 makes `RemoveAll` fail; skipped as root) then fix. A verified snapshot is kept when retention fails; the wrapped retention error is returned with the report. Only failures before verification remove the folder. Mutation backup-keep-on-retain-fail KILLED.
## Phase 0 fixes — H12 (CON-1 guard by prefix with an explicit allow-list)

- Test commits `e62cd20`, `17fb8db` (lint-only fix to the helper: no deprecated `runtime.GOROOT`/`parser.ParseDir`), `95d8998` (pins the explicit `SendMediaRetryReceipt` name); fix commit `221d677`.
- archtest `Rules`: `BannedCalls` (explicit names, any receiver, now also `SendMediaRetryReceipt`), `BannedPrefixes` (the 13 CON-1 prefixes, word-boundary match so fields like `Sender` are not hits), `AllowedCalls` (SetProxy, SetProxyAddress, SetSOCKSProxy, SetMediaHTTPClient, SetWebsocketHTTPClient, SetPreLoginHTTPClient, SetMaxParallelRetryReceiptHandling) and `FileAllowedCalls` (`internal/wa/rerequest.go` exempt for `SendMediaRetryReceipt` only; the old whole-file exemption is gone). The prefix rule applies to selectors whose receiver is not an imported package name, in files importing `go.mau.fi/whatsmeow`.
- forbidigo (type-aware): `^whatsmeow\.Client\.(prefixes)([A-Z]\w*)?$` (forbidigo with analyze-types matches the pointer-stripped `whatsmeow.Client.Method` form, checked empirically). golangci exclusions: the allow-list by message text, and `SendMediaRetryReceipt` by text only for `rerequest.go`; the rerequest.go blanket forbidigo exclusion is removed.
- Tests: `TestScanFlagsEveryBannedPrefixMethod` (enumerates the pinned whatsmeow `(*Client)` methods via `go list -m`; fails if not found), `TestAllowListedFileStillRejectsSendMessage`, `TestRetryReceiptThroughInterfaceOutsideRerequestIsFlagged`. All red first.
- Mutations: h12-prefix, h12-allowfile, h12-allowlist-extra, h12-boundary, h12-nosendmedia (5/5 KILLED, `mutations/specs-h12.json`). Shared specs.json: 111 KILLED, 7 NOMATCH (code in other agents' worktrees: media/store, not present in this branch).
- Canary (not committed): `SetStatusMessage`, `SetGroupName`, `SubscribePresence` in `internal/wa/sneaky.go` and `SendMessage` in `rerequest.go` fail both `make lint` and `TestRepoHasNoViolations`; `SetProxyAddress` and `SendMediaRetryReceipt` in `rerequest.go` pass.

## Phase 0 fixes — H12 fix round 1 (wrappers and client escape)

- Test commits `test: H12 round 1 ...` (two), fix commit follows them. In `internal/wa` non-test files archtest now (a) applies the prefix rule whether or not the file imports whatsmeow, (b) flags every interface or type-parameter-constraint method with a banned name (`interface-method`; allow-list excepted, `SendMediaRetryReceipt` excepted in rerequest.go), and (c) flags the raw client leaving the package (`client-escape`): embedded `whatsmeow.Client`/`*whatsmeow.Client`, type aliases of it, exported func/method results, exported struct fields and exported vars whose type is or contains it. The unexported `client` field and unexported helpers stay legal. `TestRepoHasNoViolations` still passes.
- Tests: `TestWAWrapperAndEscapeBypassesAreFlagged` (11 fixtures: interface wrapper, interface declaration, generic constraint, embedded pointer and value, alias, exported func and tuple result, exported field, exported var, slice var), `TestWAUnexportedClientFieldAndAllowedInterfaceMethodsPass`. `TestRetryReceiptThroughInterfaceOutsideRerequestIsFlagged` (my own H12 test) was updated: its fixture declares an interface method, which now yields two violations (interface-method and banned-call) instead of one; it still requires the call to be flagged.
- Minors: `.golangci.yml` rerequest `path:` is anchored; `SendMediaRetryReceipt` is in forbidigo's explicit list (excluded only for rerequest.go); AGENTS.md CON-1 notes the reflection limit.
- Deviation: the prefix rule requires a word boundary (name equals the prefix or the next letter is upper case), not a literal "starts with", because fields such as `info.Sender` would otherwise be hits. `TestScanFlagsEveryBannedPrefixMethod` enumerates the pinned whatsmeow methods, so a future name that breaks the boundary fails that test.
- Known, unguarded limit: reflection (`reflect.ValueOf(client).MethodByName("SetStatusMessage")`) is not caught by archtest or forbidigo. A prefix hit on a non-client receiver in wa (a field named `Sender...`/`Update...`) fails safe: rename it.

## Phase 0 fixes — ingest follow-ups

### Cycle A: the exclusion filter fails closed (privacy)

- Test commit `fead636`; fix commit `6ce35a4`. The alias func is now `func(string) ([]string, error)` and `Filter.Keep` returns `(bool, error)`; a failed alias lookup is an error, never "no aliases". `Pipeline.keep` evaluates the filter under `persist`, so `Ingest` (after the mapping writes, RF8 unchanged) and both `ImportHistory` gates (chat gate and per-message gate) retry until the lookup answers, and alert once through the existing `WriteFailed` path (chosen over a new sibling: least code, same retry-forever semantics as every other write). An excluded chat is never stored and a kept one is never dropped. `Store.Aliases` wraps its errors (`query aliases of <jid>: ...`); `cmd/archiver/main.go` passes the error through instead of `v, _ :=`.
- Tests: `TestAliasLookupErrorNeverLeaksExcludedChat` (7 failures then an excluded alias / no alias, live and history: nothing stored vs one stored, 8 lookups, one `WriteFailed`), `TestScopeReportsAliasLookupError`, `TestAliasesErrorIsWrappedWithContext` (red); `TestAliasesUnionForPhoneWithSeveralLIDs` pins the existing H1 behaviour (green on arrival; kills h1-union). Existing `scope_test.go` cases only changed signature (`keep` helper, two-value `Keep`, error-returning alias func). Red evidence: the ingest test package failed to build on the old signatures (`assignment mismatch: 2 variables but f.Keep returns 1 value`), and `TestAliasesErrorIsWrappedWithContext` failed with `err=sql: database is closed`.
- Behaviour note: with the retry-forever policy a permanently failing alias lookup blocks the handler (as any failing write does) rather than dropping or leaking the event.
- Mutations (`specs-ingest.json`): ff-open, ff-noretry, h1-union, al-wrap, all KILLED.

### Cycle B: other secret-encrypted messages are events, not empty message rows

- Test commit `8123e24`; fix commit `e9de0fb`. `SecretEncryptedMessage` of any type other than MESSAGE_EDIT (pinned enum: UNKNOWN, EVENT_EDIT, MESSAGE_SCHEDULE, POLL_EDIT, POLL_ADD_OPTION, and an absent type, which reads as UNKNOWN) was stored as an empty message row. It is now `Kind` `secret_encrypted` (`KindSecretEncrypted`): one message event with `Value` = the type name, target = `TargetMessageKey.ID`, raw proto kept, `NewText` empty, no message row, no media. It reuses the MESSAGE_EDIT branch (same `event()` helper, same `Message`/`Media` clearing). The MESSAGE_EDIT path is unchanged.
- Test: `TestSecretEncryptedNonEditIsAnEventNotAMessage` iterates the pinned enum map (so a whatsmeow bump that adds a type is covered automatically), checks the normalized Result and the stored rows (0 messages; `secret_encrypted|TARGET-<type>|<type>||1` per type). Red: `EVENT_EDIT: kind=message message=&{...} event=<nil>`.
- Mutations (`specs-ingest.json`): se-asmessage, se-keepmessage, se-value, se-target, se-newtext, se-edit-regress, all KILLED.
- No replay fixture or `docs/prior-art.md` Table 1 row added: prior art does not mention secret-encrypted messages and the brief did not ask for a fixture.
- Shared `specs.json` entries that target the changed files (47) were re-run against the final HEAD: 47 KILLED. `h6-excluded` was a NOMATCH because the history chat gate moved from `p.filter.Keep` to `p.keep`; its spec now mutates `if keepChat {` to `if chat != "" || keepChat {` (KILLED by `TestExcludedChatNameNotStoredFromHistory` and the new alias test).
## Phase 0 fixes — H17 (`status` / `chats`: read-only open, time zone, planned fields, golden tests)

- Test commit `d7f3bc2`; fix commit `4dcd391`; goldens `769a110`; two further tests `9d8ad3a` (added after the first mutation run left `h17-mode-rw` and `h17-zero-time` alive).
- `store.OpenReadOnly(path)`: missing file → `no archive at <path>: run archiver first` (nothing created, not even the folder); no migration, no WAL pragma, no write pool (the one `sql.DB` is opened `mode=ro` and serves reads and the write methods, which therefore fail with SQLite's read-only error). Deviation from ruling 1: `immutable=1` is added **only** when neither `archive.db-wal` nor `-shm` exists. A plain `mode=ro` open of a WAL database creates both files and leaves them behind (checked empirically with modernc), which `TestStatusDoesNotWriteArchive` forbids; with no `-wal` nobody has the archive open, so reading the file as is cannot see a half-written state. While the service runs (`-wal` present) the reader joins normally (`TestOpenReadOnlyReadsWhileTheWriterIsOpen`, `...RejectsWritesWhileTheWriterIsOpen`).
- `ChatStat` gains `MediaExpired` and `MediaPending`; `MediaFailed` now counts `failed` only (it used to include `expired`). Expired = `expired` + `retry_requested` (a re-upload request waiting for the phone), pending = `pending` + `downloading`; `done`/`skipped` are not counted. `Coverage` gains `Complete` (some chunk has progress >= 100).
- Health snapshot gains `version` and `version_reason` (set in `writeLocked` from the monitor's version func and the new `WithVersionReason`); `serve` wires `client.VersionReason`. `NewMonitor` keeps its signature.
- `status` (new `cmd/archiver/status.go`; `printStatus`/`printChats` moved out of `main.go`): lines `state` (`unknown (health.json not found)` / `unknown (health.json unreadable: ...)`; detail in brackets), `client version: <v> (<reason>)`, `last event`, `phone last active`, `history: ...` (complete back to <date of the oldest history message> / in progress (<n>%) / none received yet, plus `chunks <n>, last chunk <time>`), a header, one tab-separated row per chat (name, canonical JID, messages, oldest, newest, media failed, expired, pending, open gaps), `total\t<n> chats\t<sum>...`, `recovered panics`, `data folder: <size>`. Times are `2006-01-02 15:04 MST` in `cfg.TimeZone` (empty → UTC, invalid → error from `status` and `chats`; unset config means the default `Local`); zero or unknown times print `-`. `time/tzdata` is linked in so a zone name resolves on hosts without a zoneinfo database (Windows build). `humanSize` prints one decimal for every unit (`0.0 B`, `85.8 KB`), as the ruling says; `data folder` sums regular files under `--data` (a file that vanishes mid-walk is skipped).
- `chats`: `excluded=yes|no`, yes when the canonical JID or any alias (`store.Aliases`) is in `cfg.Exclude` (fixes L2).
- Tests: `TestStatusGolden`, `TestChatsGolden`, `TestStatusDoesNotWriteArchive` (WAL and journal-mode DELETE snapshot; whole data folder compared by name, size, mtime, archive.db mtime set to 2020 first), plus `TestStatusPrintsHealthFieldsInTheConfiguredZone`, `TestStatusChatRowsAndTotals`, `TestStatusWithoutHealthJSON`, `TestStatusPrintsDashForUnknownTimesAndVersion`, `TestStatusEmptyTimeZoneIsUTCAndInvalidIsAnError`, `TestStatusHistoryLines` (none / in progress / complete), `TestStatusDataFolderSize` (nested sparse 3 MiB file), `TestChatsExcludedByAliasOrCanonicalJID`, `TestStatusReadsWhileTheServiceHasTheArchiveOpen`, `TestStatusAndChatsOnAMissingArchiveFailAndCreateNothing`, `TestStatusOutputHasNoTimestampInUnixSeconds`, `TestHumanSize`; store `readonly_test.go`; health `TestSnapshotRecordsClientVersionAndReason`. Red: the cmd tests failed behaviourally against the old code (golden missing, missing lines, folder changed, typo `--data` accepted); `humansize_test.go`, the store and the health tests were compile-red (new symbols).
- Golden workflow: the seeded folder is built in `status_test.go` (3 chats incl. LID/PN pair `204871@lid` / `966550100123@s.whatsapp.net`, 10 messages, one open and one closed gap, media in failed/expired/retry_requested/pending/downloading/done, 3 history chunks, `health.json`, `time_zone: Asia/Riyadh`, two excluded JIDs). Because `make check` has to pass in green, the goldens were generated with `-update` once, uncommitted, during green (the guard hook locks only `_test.go`), read in full, then committed in `769a110` after the lock was removed. Review, line by line (Riyadh = UTC+3):
  - `status.golden` 1 `state: connected` ← health.json state; 2 `client version: 2.3000.1031 (latest)` ← version + version_reason; 3 `last event: 2026-10-12 12:15 +03` ← 09:15:30Z, seconds dropped, zone applied; 4 `phone last active: 2026-10-12 09:00 +03` ← 06:00Z; 5 `history: complete back to 2024-03-02, chunks 3, last chunk 2026-10-10 12:00 +03` ← chunk h3 has progress 100; oldest history message is Family `H1` 2024-03-01T21:30Z = 2024-03-02 00:30 +03 (the zone moves the date); last chunk 09:00Z; 6 header; 7 Family: 5 messages (H1, H2, L1, L2, L3), oldest 2024-03-02 00:30, newest L3 12:15, failed 1 (`f1`), expired 2 (`e1`, `r1` retry_requested), pending 2 (`p1`, `d1` downloading), `ok` (done) not counted, gaps 0 (the `L2` gap is closed because L2 exists); 8 Work: 1 message at 2026-01-01T00:00Z = 03:00 +03 as oldest and newest, no media; 9 Layla under canonical `204871@lid`: 4 messages (`P1`,`P2` stored under the phone number, `Q1`,`Q2` under the lid), oldest P1 2025-01-05 15:00, newest Q2 2026-10-12 09:00, failed 1 (media row stored under the phone number is counted via the alias), gaps 1 (`GAP1`); 10 `total 3 chats 10 - - 2 2 2 1` = 5+1+4 messages, failed 1+0+1, expired 2, pending 2, gaps 1; 11 `recovered panics: 2` ← health.json; 12 `data folder: 85.8 KB` = archive.db 86016 + config.yaml + health.json + the 1500-byte media file (checked stable over 4 runs).
  - `chats.golden`: Family `excluded=no`; Work `excluded=yes` (canonical JID in the config); Layla `204871@lid` `excluded=yes` although only her phone-number alias is in the config (L2). Order is by canonical JID.
- Mutations (`specs-h17.json`, run with `MUT_REPO`): h17-rw, h17-tz, h17-version, h17-alias-excluded (the four named in the brief) plus h17-immutable, h17-mode-rw, h17-expired-retry, h17-pending-downloading, h17-failed-incl-expired, h17-complete-last, h17-history-newest, h17-total-messages, h17-nohealth-text, h17-zero-time, h17-size-walk, h17-humansize-cap, h17-health-version, h17-health-reason, h17-noexist-error: 19/19 KILLED. Not mutation-tested: the `serve` wiring `.WithVersionReason(client.VersionReason)` (serve connects to WhatsApp and has no test).
- Probe `TestProbeStatusWritesArchive` (scratch copy): before `archive.db mtime changed by status: true`, `status --data <typo>` created `archive.db: true`; after `mtime changed: false ; files in folder: [archive.db]`, `created a new archive.db: false`.
- Open: the monitor now reads `client.VersionInUse()` / `VersionReason()` on every health.json write, while `refreshVersion` (the reconnect loop's goroutine) writes those fields without a lock; before, only the `client_outdated` alert read them. Strings are two words, so a torn read is theoretically possible (the race detector does not see it because no test refreshes while the monitor writes). A mutex in `wa.Client` would fix it; left out of H17's scope.

## Phase 0 fixes — H17 fix round 1 (client version race)

- Review finding: `health.writeLocked` reads `client.VersionInUse()`/`VersionReason()` from monitor, event and worker goroutines while `refreshVersion` wrote both fields unlocked (the Open item recorded above). Test commit `test: H17 round 1 ...` (`TestVersionAccessorsAreSafeDuringRefresh`, DATA RACE under `-race` on the old code); fix commit `fix: H17 round 1 ...`: `Client` holds the version and its reason as one `atomic.Pointer[clientVersion]`, `refreshVersion` stores a new pair, the accessors keep their signatures. `make check` green. No existing mutation spec targets the changed text.
## Phase 0 fixes — H14 (record / replay / scrub, DumpRaw and the synthetic golden)

### Golden review: `internal/ingest/testdata/golden/synthetic.sql` (reviewed before `-update`)

Fixtures: `internal/ingest/testdata/events/synthetic/` (17 files) were written by `TestSyntheticEventsMatchGenerator -update` from `syntheticEvents()` (Go-constructed, fake numbers only: group `10000000100@g.us`; Alice `20000000001@lid`/`10000000001@s.whatsapp.net`, Bob `...02`, Carol `...03`, Dave `20000000004@lid`). Every file and every decoded protobuf was read: 01 joined_group (owner Alice, name "Synthetic Group", 2 participants), 02 text, 03 reply (stanza SYN-TEXT-1, quoted text), 04 edit (EditedMessage → MESSAGE_EDIT of SYN-TEXT-1, "Hello, edited"), 05 revoke of SYN-REPLY-1, 06 reaction 👍 on SYN-TEXT-1, 07 image (jpeg, caption, 2048 bytes, no keys), 08 undecryptable SYN-GAP-1, 09 SYN-GAP-1 resent, 10 history chunk (INITIAL_BOOTSTRAP, order 1, progress 100, one mapping Carol PN→LID, one DM message SYN-HIST-1 a day earlier, push name Carol), 11 group rename, 12 Dave added, 13/14 archive on/off, 15 mute (end 1760004400000), 16 pin, 17 media retry with error code 2 for unknown SYN-UNKNOWN-1. No real number, no key bytes.

The dump was produced with `archiver replay internal/ingest/testdata/events/synthetic --data <tmp> --dump` (same pipeline as the test; "replayed 17 events", one history INFO log line, no errors) and read line by line:

1. `chats` `10000000100@g.us` name '' — the group row; the joined/rename names live in `chat_events` (FromChatEvent sets only the JID; existing behaviour, not changed here).
2. `chats` `20000000003@lid` 'Carol' — the history conversation name.
3–5. `participants` Alice, Bob, Carol with their push names (H14 d: push name survives replay).
6–7. `id_mappings` Alice and Bob LID↔PN, source `message_alt` (H14 d: alt JIDs survive replay); `first_seen` (a receive time) omitted.
8. `id_mappings` Carol, source `history` (the chunk's mapping list).
9. `messages` SYN-GAP-1, Bob, ts 1760000420, the resent text, raw = conversation proto.
10. `messages` SYN-IMAGE-1, Alice, ts 1760000360, caption as text, media_type image, raw = the image proto (media row not dumped: replay's media queue is a no-op).
11. `messages` SYN-REPLY-1, Bob, quoted_id SYN-TEXT-1 (stays after its revoke: raw rows are insert-only).
12. `messages` SYN-TEXT-1, Alice, ts 1760000060 (H14 d: the WhatsApp timestamp, not year 1).
13. `messages` `20000000003@lid` SYN-HIST-1, sender = the DM chat (not from me), ts 1759913600, source history.
14. `message_events` undecryptable SYN-GAP-1 (closed by line 9 via `open_gaps`), value '' (DecryptFailShow).
15. `message_events` revoke of SYN-REPLY-1 by Bob, raw = the protocol message.
16. `message_events` edit of SYN-TEXT-1, new_text and value 'Hello, edited' (as `event()` writes both), raw = the EditedMessage wrapper.
17. `message_events` reaction 👍 on SYN-TEXT-1 by Bob.
18. `chat_events` mute, ts 1760000800, value with mute_end, raw `0801 10..`.
19–20. `chat_events` archive off (1760000760, raw `0800`) and on (1760000700, raw `0801`): two distinct rows.
21. `chat_events` pin, ts 1760000900.
22. `chat_events` group_info rename by Alice, ts 1760000600.
23. `chat_events` group_info joined group, ts 0 (JoinedGroup has no WhatsApp timestamp), value notify/type/name/created.
24. `chat_events` participants: Dave joined, ts 1760000660.
25. `history_sync_chunks` content-hash id, INITIAL_BOOTSTRAP, order 1, progress 100, 1 conversation, 1 message, oldest = newest = 1759913600; `received_at` omitted.

Rows are ordered by table (chats, participants, id_mappings, messages, message_events, chat_events, history_sync_chunks) then by every column. The media retry (17) writes nothing (no-op queue). Verdict: every line matches the scenario; the golden may be created.

### Implementation, tests and mutations

- Cycle A: test commits `cf5b911`, `c4a66d3` (gofmt only); fix `6eaea75`. Recorded file `<seq>-<kind>.json` = `{"kind","info","proto"}` (ruling 1). Kinds: message (info = `types.MessageInfo`, proto = raw message), undecryptable (info = the whole `events.UndecryptableMessage`, so `DecryptFailMode`/`IsUnavailable`/`UnavailableType` survive — deviation from "info = MessageInfo", needed for the round trip), history_sync (proto only), group_info / joined_group (whatsmeow struct JSON; round trip verified with every field set, `UnknownChanges` via whatsmeow's own `Node` JSON), archive/mute/pin (`{jid,timestamp,from_full_sync}` + Action proto; a nil Action stays nil), media_retry (struct JSON). `Load` rebuilds the event types `Handle` routes; a message goes through whatsmeow's `UnwrapRaw` as live dispatch does. Write errors are logged (`WithLogger`), the event is always passed on unchanged. A second recorder on the same folder continues the numbering (pair then run with `--record ~/rec` used to overwrite).
- Cycle B: test `f5faa62`; fix `deec544`. `Scrub` decodes each JSON file, replaces identifiers in every string value, base64-decodes `proto`, walks every protobuf field with `protoreflect` (nested, repeated, map; unknown fields dropped as they cannot be inspected) and re-encodes deterministically. Identifiers: `user[.agent][:device]@{s.whatsapp.net,lid,g.us,c.us,hosted,hosted.lid,broadcast}` (old `creator-ts` groups included) and whole-string numbers (≥ 8 digits, optional `+`) in fields named *jid/participant/phone/number*. Fake = `0` + 14 digits of SHA-256(user part): stable per run and across runs, suffix and server kept. Bytes fields ending in `key`, `keydata`, `secret`, `token` or containing `senderkeydistribution` are cleared (ruling: "ends in key (media keys, key data)"; secrets/tokens/sender keys added — flagged). Recorder and loader marshal with `AllowPartial` (a chunk without the required `syncType` was not recorded at all). CLI: `archiver scrub <in> <out>`; `archiver replay <dir> [--data d] [--dump] [--own jid] [--own-lid jid]` (Load → fresh pipeline on `<data>/archive.db`, filter from config, no-op media queue and health sink, `wa.NewHistoryParser` with placeholder own IDs `10000000000@s.whatsapp.net`/`@lid`; refuses an existing `archive.db` so a replay can never write into a real archive). main.go change: usage line plus a 3-line dispatch before flag parsing; the tool lives in `cmd/archiver/replay.go`.
- Cycle C: test `91b2845`; fixtures/golden `3bf4e88` after review `dcdd161`; fix `cb01499`. `store.(*Store).DumpRaw` (new `internal/store/dump.go`, read connection) and `ingest.DumpRaw(st)`: tables chats, participants, id_mappings, messages, message_events, chat_events, history_sync_chunks (fixed order; media is the queue's table and is empty in replay), all columns from `pragma_table_info` except `received_at` and `id_mappings.first_seen` (a receive time), `ORDER BY` every column, `INSERT INTO t(cols) VALUES(...)` lines, blobs `X'HEX'`, quotes doubled. chats and participants are included because push names and chat names live only there (H14 d). `replay --dump` prints the dump (count to stderr); otherwise it writes `<data>/raw.sql` and prints its path. Order deviation: the golden needs the uncommitted DumpRaw code, so the sequence was red → implement → unlock → review (`dcdd161`) → `-update` → fixtures commit → `make check` → fix commit.
- Tests: `TestRecordLoadRoundTripForEachKind`, `TestRecorderContinuesAfterExistingFiles`, `TestRecorderWriteFailureIsLoggedAndEventPassedOn`, `TestScrubRemovesNumbersInsideProtobuf`, `TestScrubDeviceSuffixedJID`, `TestScrubClearsKeyMaterial` (green on arrival: the clearing was written in cycle B), `TestScrubCommandWritesScrubbedCopy`, `TestReplayCommandStoresEventsWithoutConnecting`, `TestDumpRawIsSortedAndOmitsReceiveTimes`, `TestSyntheticEventsMatchGenerator`, `TestReplayMatchesGolden`, `TestReplayCommandDumpMatchesGolden`. Red shown per cycle (compile failure, and behavioural failure against a scratch stub for A and C).
- Coverage: internal/ingest 86.7%, cmd/archiver 45.9%, internal/store 43.4% (DumpRaw is exercised from ingest tests).
- Mutations (`specs-h14.json`) 16/16 KILLED: h14-device, h14-proto, h14-kind, h14-ts, h14-dump, h14-order, h14-bare, h14-keys, h14-group, h14-seq, h14-partial, h14-unwrap, h14-logerr, h14-fresh, h14-dumpcli, h14-history-cli. Shared `specs.json` at `8428d85`: 146 KILLED, 0 SURVIVED, 0 NOMATCH (x10, h18-exit, h18-ntfy target `cmd/archiver/main.go`; no spec needed repair).
- Probes (scratch copy of HEAD): `TestProbeScrubMissesNumbersInsideProtobufAndDeviceJIDs` → number in JSON text false, in decoded protobuf false, mentioned number false (the probe's `rec["sender"]` now prints `<nil>`: the sender lives under `info`). `TestProbeReplayRoundTripLosesFields` → replayed `ts=1700000123 push="Alice" senderAlt=4915123456789@s.whatsapp.net isGroup=true` (= original); the recorder keeps undecryptable, group info, archive, history sync and media retry.
- Open: the chats row of a group has an empty name (FromChatEvent writes only the JID; names are in chat_events) — existing behaviour, visible in the golden. Push names and group names in a recorded corpus are kept by Scrub (text is kept). An unsalted hash of a phone number can be brute-forced; the fakes are stable by ruling, so scrubbed fixtures must still stay in the private repo.

### Fix round 1 (I1 payload bytes, I2 numbers in text, I3 keyed fakes)

- I1+I2: test commit `c0698e6`, fix `5621cb8`. Scrub also clears protobuf bytes fields whose name contains `payload` or ends in `bytes` (inline history bootstrap, placeholder-resend `webMessageInfoBytes`, LID migration `encodedMappingPayload`), and in the JSON the verified-name `Certificate` (set to null) and every string `Content` (unknown group-change node content, set to ""). In every string field it replaces `@<8+ digits>` mention tokens, `waid=<digits>` and vCard `TEL...:` values (digits only; a leading `+` is kept) with the same fake as the JID of that number, so a text mention matches the rewritten `mentionedJID`. Tests `TestScrubClearsPayloadBytes` (number absent raw, inside embedded zlib streams and inside base64 strings), `TestScrubRewritesMentionsAndVCards`; red: files 1–5 kept the numbers; text kept `@10000000001`; vCard unchanged.
- I3: test commit `577ca91`, fix `1c806de`. Fake = `0` + 14 digits of HMAC-SHA256(key, user part); `Scrub(in, out, key)` refuses keys under 32 bytes. `ingest.LoadScrubKey(path)` creates 32 random bytes with mode 0600 (`O_EXCL`, parent 0700) on first use and reuses it; a short file is refused. CLI: `archiver scrub [--key-file <path>] <in> <out>`, default `os.UserConfigDir()/archiver/scrub.key` (flag chosen over the env var). Existing Scrub tests pass a fixed `testKey`; the CLI scrub tests point `os.UserConfigDir` at a temp folder. Tests `TestScrubFakeIsKeyed` (same key → same fake, other key → other fake, differs from the old unkeyed SHA-256 fake, short key refused), `TestScrubKeyFileCreatedPrivateAndReused`, `TestScrubCommandKeyFile` (default path created 0600 and reused; `--key-file` overrides). Red shown against a scratch stub that ignores the key.
- The synthetic fixtures and golden are unchanged (generated, not scrubbed). SKILL.md: key location, never commit it, "nothing identifying remains" only while the key is private, avoid mentions/contact cards/typed numbers in the test group.
- Coverage: internal/ingest 87.0%, cmd/archiver 49.0%.
- Mutations `specs-h14.json`: 30/30 KILLED (16 earlier + h14r1-bytes-suffix, -payload, -cert, -content, -mention, -waid, -tel, -barehash, -nokey, -keymode, -keyreuse, -shortkey, -shortfile, -defaultkey). `h14-proto` match string updated for the `sc.` receiver (my change moved it). Shared specs.json has no entries on scrub.go or replay.go; x10, h18-exit, h18-ntfy (main.go) re-run: KILLED.

## H13 and M5 (branch fix/h13, base 2799fe0)

Two cycles. A: every planned, Table 1 and Review Focus test exists. B: goleak, `serve` joins its goroutines, shutdown context. No behaviour bug turned up in cycle A (every new test passed on arrival and was proven with a mutant); cycle B has one red/green pair.

### Cycle A: gap list (mechanical)

Script (run from the repo root): collect every backticked `Test*` name from `docs/prior-art.md` lines 8-26 (Table 1), review section 4, section 7 (item 15 marked `S7-15`), then `grep -rl "^func <Name>(" --include='*_test.go' cmd internal`. 115 names: 104 found before H13, 9 missing before plus 2 name variants (below); after H13 113 found. `T1` = Table 1, `S4` = section 4, `S7` = section 7 fix plan.

Still not found by name, with the reason:
- `TestSessionNeverBackedUp` (S4 "✓ (merged)"): merged into `TestSnapshotVerifiesAndNeverIncludesSession`, which plants a `session.db` and asserts the snapshot folder lacks it. Not duplicated.
- `TestUnknownEventIgnored` (S4): spelling variant of `TestUnknownEventIsIgnored` (S7 item 15), which exists now.

"Needs behaviour, deferred": audio voice-note detection (section 4, Task 3 "audio voice note"). `normalizeContent` assigns `"audio"` in both branches of the PTT check (the dead branch of review mutant `y7`), the media rules know no separate voice-note type, so there is nothing to pin beyond `TestDocumentAudioAndVideoMedia` (voice note and plain audio both yield an `audio` media row). Not built.

Section 4 rows without a test name, mapped to tests (new ones marked *):
- Task 3 document / audio / video: `TestDocumentAudioAndVideoMedia`*. Ephemeral / view-once unwrap: `TestHistoryEphemeralAndDocumentCaptionAreUnwrapped`, `TestHistoryViewOnceIsUnwrapped`*, and the "new unwrapped" case of `TestProtocolEditOldAndNewFormats`. Edit with unknown target: `TestEventsForUnknownTargetAreStored`* (edit, revoke, reaction; no message row invented). Group info, participants, archive, mute: the `chatevents_test.go` tests (H7). Scope for every kind: `TestScopeDropsExcludedChatForEveryKind`*. Scope other direction: `TestExcludeLIDMatchesPhoneForm`*.
- Task 5: `TestHandleRecoversPanic`*, `TestMappingFromAltJIDStoredBeforeFilter`*, `TestSameMessageUnderLIDAndPhoneStoredOnce`*, `TestFromMeUpdatesPhoneActivity`*, `TestUnknownEventIsIgnored`*, plus `TestHandleRoutesMediaRetryToTheQueue`* (review mutant `y4`, survived at the review and again at H13 start: `h13-route-mediaretry`). Goleak: cycle B.
- Task 6 two conversations (3 kept, 2 excluded): `TestHistoryChunkSplitsKeptAndExcludedConversations`*. Late chunk: `TestHistoryMessagesOfLateChunkAreStored`*.
- Task 7 sticker skipped, 150 MB video skipped with no download, Enqueue returns immediately: `TestEnqueueRecordsDecisionWithoutDownloading`* (the rule-level `decide` tests could not see an Enqueue that ignored them). Fails twice then succeeds: `TestAttemptsCountedAcrossRetries`. Always fails: `TestAlwaysFailingDownloadFailsAfterTwentyAttempts` (the M4 ruling replaced the plan's 5). Others exist (H3, H4, H11).
- Task 2 `TestNoUpdateOrDeleteOnRawTables`* and `TestOnlyOneWriteConnection`* (store) with `TestServeOpensTheArchiveOnce`* (cmd). Tasks 8-11 rows exist (H10, H14, H15, H16, H17).

Full table:

| Test | Listed in | Before H13 | After | File |
|---|---|---|---|---|
| `TestAliasLearnedLaterCollapsesInView` | S4 | found | found | internal/store/store_test.go |
| `TestAllowListedFileStillRejectsSendMessage` | S7 | found | found | internal/archtest/archtest_test.go |
| `TestAlternateJIDCreatesMapping` | S4 | found | found | internal/ingest/normalize_test.go |
| `TestArchiveOnAndOffAreDistinct` | S7 | found | found | internal/ingest/chatevents_test.go |
| `TestAttemptsCountedAcrossRetries` | S7 | found | found | internal/media/backoff_test.go |
| `TestChatEventsStored` | T1 | found | found | internal/ingest/chatevents_test.go |
| `TestChatStatsOldestNewest` | S4 | found | found | internal/store/store_test.go |
| `TestChatsGolden` | S7 | found | found | cmd/archiver/status_test.go |
| `TestClassifyDownloadErr` | S4 | found | found | internal/wa/wa_test.go |
| `TestClientOutdatedSameVersionWaits30Minutes` | S7 | found | found | internal/wa/reconnect_test.go |
| `TestCoverageReportsOldestPerChat` | S4 | found | found | internal/store/store_test.go |
| `TestDecideVersion` | S4 | found | found | internal/wa/wa_test.go |
| `TestDisconnectedLongerThanThresholdAlerts` | S7 | found | found | internal/health/alerts_test.go |
| `TestDocumentDownloads` | S4 | found | found | internal/media/queue_test.go |
| `TestDuplicateEventIsNotARetryableError` | S7 | found | found | internal/ingest/backoff_test.go |
| `TestEmptyOrPartialChunkDoesNotPanic` | T1 | found | found | internal/ingest/history_test.go |
| `TestEnqueueFailureIsRetried` | S7 | found | found | internal/ingest/writeretry_test.go |
| `TestEventForUnknownMessageIsStored` | S4 | found | found | internal/store/store_test.go |
| `TestExcludeLIDMatchesPhoneForm` | S7-15 | missing | found | internal/ingest/gaps_test.go |
| `TestExcludedChatNameNotStoredFromHistory` | S7 | found | found | internal/ingest/history_test.go |
| `TestExpiredMediaRetryCompletes` | T1 | found | found | internal/media/rerequest_test.go |
| `TestExpiredRequestsAreRateLimited` | T1 | found | found | internal/media/ratelimit_test.go |
| `TestFailedSnapshotLeavesNoFolder` | S7 | found | found | internal/backup/backup_test.go |
| `TestFromMeUpdatesPhoneActivity` | S7-15 | missing | found | internal/ingest/gaps_test.go |
| `TestGuardRecoversPanic` | S4 | found | found | internal/guard/guard_test.go |
| `TestHandleDropsExcludedChat` | S4 | found | found | internal/ingest/pipeline_test.go |
| `TestHandleRecoversPanic` | T1 | missing | found | internal/ingest/gaps_test.go |
| `TestHandleStoresKeptMessage` | S4 | found | found | internal/ingest/pipeline_test.go |
| `TestHandleWriteFailureRetriesUntilStored` | S4 | found | found | internal/ingest/writeretry_test.go |
| `TestHealthJSONRoundTrip` | S7 | found | found | internal/health/alerts_test.go |
| `TestHistoryEditIsStillAnEvent` | S7 | found | found | internal/ingest/history_test.go |
| `TestHistoryEphemeralAndDocumentCaptionAreUnwrapped` | S7 | found | found | internal/ingest/history_test.go |
| `TestHistoryFromMeMessageHasOwnJIDAsSender` | S7 | found | found | internal/ingest/history_test.go |
| `TestHistoryMessagesOfLateChunkAreStored` | S7-15 | missing | found | internal/ingest/gaps_test.go |
| `TestHistoryPropsUseConfiguredLimits` | S4 | found | found | internal/wa/wa_test.go |
| `TestHistoryPushNameIsStored` | S7 | found | found | internal/ingest/history_test.go |
| `TestHistoryThenLiveNoDuplicate` | T1 | found | found | internal/ingest/history_test.go |
| `TestIDMappingIsIdempotent` | S4 | found | found | internal/store/store_test.go |
| `TestImageAndStickerMedia` | S4 | found | found | internal/ingest/normalize_test.go |
| `TestImportHistoryRetriesLedgerWrite` | S7 | found | found | internal/ingest/writeretry_test.go |
| `TestInsertMessageCostDoesNotGrowWithTableSize` | S7 | found | found | internal/store/insert_cost_test.go |
| `TestInsertMessageIsIdempotent` | S4 | found | found | internal/store/store_test.go |
| `TestInsertMessageUnderKnownAliasIsDuplicate` | S4 | found | found | internal/store/store_test.go |
| `TestKeepTwoLeavesTwoSnapshots` | S7 | found | found | internal/backup/backup_test.go |
| `TestLateChunkAfterCompleteIsStored` | T1 | found | found | internal/ingest/history_test.go |
| `TestLiveMediaBeforeHistoryBacklog` | S7 | found | found | internal/media/worker_test.go |
| `TestLiveMessageKeepsKnownChatName` | S7 | found | found | internal/ingest/history_test.go |
| `TestLoadDefaults` | S4 | found | found | internal/config/config_test.go |
| `TestLoadKeepsDefaultsForMissingFields` | S4 | found | found | internal/config/config_test.go |
| `TestLoadRejectsBadVersionOverride` | S4 | found | found | internal/config/config_test.go |
| `TestLoadRejectsUnknownKey` | S4 | found | found | internal/config/config_test.go |
| `TestLoggedOutStopsTheReconnectLoop` | S7 | found | found | internal/wa/reconnect_test.go |
| `TestMappingFromAltJIDStoredBeforeFilter` | S7-15 | missing | found | internal/ingest/gaps_test.go |
| `TestMappingWriteFailureIsRetried` | S7 | found | found | internal/ingest/writeretry_test.go |
| `TestMappingsInChunkApplyToItsMessages` | T1 | found | found | internal/ingest/history_test.go |
| `TestMediaMetadataRecorded` | S7-15 | found | found | internal/media/worker_test.go |
| `TestNoUpdateOrDeleteOnRawTables` | S7-15 | missing | found | internal/store/rawtables_test.go |
| `TestNonRevokeProtocolMessageIsNotARevoke` | S7 | found | found | internal/ingest/protocol_test.go |
| `TestNonsensicalHistoryTimestampStoredAsGiven` | T1 | found | found | internal/ingest/history_test.go |
| `TestOnlyOneWriteConnection` | S7-15 | missing | found | internal/store/rawtables_test.go |
| `TestOpenGapClosesWhenMessageArrives` | S4 | found | found | internal/store/store_test.go |
| `TestOutdatedAfterConnectedTriggersRefresh` | S7 | found | found | internal/wa/reconnect_test.go |
| `TestOutdatedAlertNamesVersion` | S7 | found | found | internal/health/alerts_test.go |
| `TestPendingAndDownloadingRowsRecoverAfterRestart` | S7 | found | found | internal/media/worker_test.go |
| `TestPhoneQuietAlert` | S7 | found | found | internal/health/alerts_test.go |
| `TestPlainTextNormalizes` | S4 | found | found | internal/ingest/normalize_test.go |
| `TestPollVoteCurrentAndNewVersions` | T1 | found | found | internal/ingest/protocol_test.go |
| `TestProtocolEditOldAndNewFormats` | T1 | found | found | internal/ingest/protocol_test.go |
| `TestRecordLoadRoundTripForEachKind` | S7 | found | found | internal/ingest/record_test.go |
| `TestRecoveredMessageAfterEventsResume` | S7 | found | found | internal/health/alerts_test.go |
| `TestReopenRunsMigrationsOnce` | S4 | found | found | internal/store/store_test.go |
| `TestReplayMatchesGolden` | S4 | found | found | internal/ingest/golden_test.go |
| `TestReplyAndForwarded` | S4 | found | found | internal/ingest/normalize_test.go |
| `TestRepoHasNoViolations` | S4 | found | found | internal/archtest/archtest_test.go |
| `TestRequestMediaRetryReceivesMessageInfoAndKey` | S7 | found | found | internal/wa/rerequest_test.go |
| `TestRetentionOnlyRemovesSnapshotFolders` | S7 | found | found | internal/backup/backup_test.go |
| `TestRetryGivesUpAtMax` | S4 | found | found | internal/retry/retry_test.go |
| `TestRetryStopsOnSuccess` | S4 | found | found | internal/retry/retry_test.go |
| `TestRevokeNormalizes` | S7 | found | found | internal/ingest/protocol_test.go |
| `TestRunWithUnpairedSessionFileSaysPairFirst` | S7 | found | found | cmd/archiver/main_test.go |
| `TestRunWithoutSessionSaysPairFirst` | S4 | found | found | cmd/archiver/main_test.go |
| `TestSameGroupInfoTwiceIsOneRow` | S7 | found | found | internal/ingest/chatevents_test.go |
| `TestSameMessageUnderLIDAndPhoneStoredOnce` | T1 | missing | found | internal/ingest/gaps_test.go |
| `TestScanAllowsRetryReceiptInAllowListedFile` | S4 | found | found | internal/archtest/archtest_test.go |
| `TestScanFlagsBannedCall` | S4 | found | found | internal/archtest/archtest_test.go |
| `TestScanFlagsClientImportOutsideWA` | S4 | found | found | internal/archtest/archtest_test.go |
| `TestScanFlagsEveryBannedPrefixMethod` | S7 | found | found | internal/archtest/archtest_test.go |
| `TestScheduleFiresOnceAtConfiguredTime` | S7 | found | found | internal/backup/backup_test.go |
| `TestScopeAlwaysSkipsNoise` | S4 | found | found | internal/ingest/scope_test.go |
| `TestScopeExcludesDirectAndAlias` | S4 | found | found | internal/ingest/scope_test.go |
| `TestScrubDeviceSuffixedJID` | S7 | found | found | internal/ingest/scrub_test.go |
| `TestScrubRemovesIdentifiers` | S4 | found | found | internal/ingest/replay_test.go |
| `TestScrubRemovesNumbersInsideProtobuf` | S7 | found | found | internal/ingest/scrub_test.go |
| `TestSessionDSNEnablesForeignKeys` | S4 | found | found | internal/wa/wa_test.go |
| `TestSessionNeverBackedUp` | S4 | missing | MISSING |  |
| `TestSnapshotDuringConcurrentInserts` | S7 | found | found | internal/backup/backup_test.go |
| `TestSnapshotRowCountMatchesSource` | S7 | found | found | internal/backup/backup_test.go |
| `TestSnapshotVerifiesAndNeverIncludesSession` | S4 | found | found | internal/backup/backup_test.go |
| `TestStatusDoesNotWriteArchive` | S7 | found | found | cmd/archiver/status_test.go |
| `TestStatusGolden` | S7 | found | found | cmd/archiver/status_test.go |
| `TestStreamReplacedBacksOff` | S7 | found | found | internal/wa/reconnect_test.go |
| `TestTempBanAlertIncludesExpiry` | S7 | found | found | internal/health/alerts_test.go |
| `TestTemporaryBanPausesCapture` | S7 | found | found | internal/wa/reconnect_test.go |
| `TestTimestampStoredAsGiven` | S4 | found | found | internal/store/store_test.go |
| `TestTransientFailureIsRetriedAfterBackoff` | S7 | found | found | internal/media/backoff_test.go |
| `TestTwoDistinctChunksWithSameProgressAreBothLedgered` | S7 | found | found | internal/ingest/history_test.go |
| `TestUndecryptableIsDeterministic` | S4 | found | found | internal/ingest/normalize_test.go |
| `TestUndecryptableThenResentClosesGap` | T1 | found | found | internal/ingest/pipeline_test.go |
| `TestUnknownEventIgnored` | S4 | missing | MISSING |  |
| `TestUnknownEventIsIgnored` | S7-15 | missing | found | internal/ingest/gaps_test.go |
| `TestVerifyRejectsTruncated` | S4 | found | found | internal/backup/backup_test.go |
| `TestVersionPrints` | S4 | found | found | cmd/archiver/main_test.go |
| `TestWorkerPanicMarksRowFailedAndIsCounted` | S7 | found | found | internal/media/worker_test.go |
| `TestWriteErrorIsReturned` | S4 | found | found | internal/store/store_test.go |
| `TestWriteRetryBacksOff` | S7 | found | found | internal/ingest/backoff_test.go |

### Cycle A: tests and mutations

- Test commit `00ce8be`; every new test passed on first run (nothing red), so each has a mutant: `specs-h13.json` `h13-guard`, `h13-panic-unreported` (RF10a), `h13-mapping-after-filter` (RF8b), `h13-nomap`, `h13-fromme` (y2), `h13-unknown-state`, `h13-route-mediaretry` (y4), `h13-route-history` (y5), `h13-scope-message-only`, `h13-alias-oneway`, `h13-late-skip`, `h13-skip-count`, `h13-gif`, `h13-doc-type`, `h13-audio-nomedia`, `h13-viewonce`, `h13-revoke-event-dropped`, `h13-x2-four-writers` and `h13-x2-readpool-writable` (x2), `h13-x8-replace`, `h13-x8-upsert`, `h13-x8-delete`, `h13-x8-migration-update`, `h13-x8-other-package`, `h13-x8-cmd` (x8, six forms), `h13-enqueue-decide`, `h13-serve-open2`, `h13-open-elsewhere`. In every case the new test is among the killers. `h13-route-mediaretry` survived the first run (no test routed `MediaRetry`), so `TestHandleRoutesMediaRetryToTheQueue` was added; `h13-audio-nomedia` first did not compile and was corrected.
- `TestNoUpdateOrDeleteOnRawTables` scans the string literals of non-test Go files (go/scanner, so comments do not count) and the `.sql` files under `internal/` and `cmd/` for UPDATE, DELETE, REPLACE INTO (so INSERT OR REPLACE) and `INSERT INTO <raw> ... DO UPDATE` on the five raw tables, and fails if it scanned fewer than 20 files or missed `store.go` or the migration; `TestRawWriteDetectorRecognisesTheForbiddenForms` pins the matcher on good and bad statements. Limit: a statement assembled from separate string fragments is not seen.
- `internal/backup/backup.go` opens `archive.db` read-write through its own `sql.Open` (the known deferral in the ledger: use `mode=ro`); `TestServeOpensTheArchiveOnce` covers `store.Open` only.

### Cycle B: goleak, shutdown context, serve lifecycle

- Test commit `da5800b`: `TestMain` with `goleak.VerifyTestMain(m)` in `internal/ingest`, `internal/media`, `internal/health` (health keeps its in-test `TestRunLeavesNoGoroutines`). No leaks found, no ignores added. `TestDocumentDownloads` no longer sleeps 50 ms: it waits until the row is `done`, then `stop()` waits for `Run` to return, so reading the fake afterwards is race-free (assertions unchanged). goleak alone would not have flagged it (it retries), so this is the M5 finding about the test, not a goleak requirement: noted as a judgment call.
- Red `838cd5b` (compile errors): `Pipeline.WithContext` and `runService` undefined. Tests: `TestHandleStopsRetryingWhenTheServeContextEnds`, `TestRunServiceJoinsLoopsBeforeClosingClientThenStore`, `TestRunServiceStopsLoopsWhenRunReturnsOnItsOwn`, `TestRunServiceClosesEverythingAndReportsCloseErrors`, `TestServeOwnsItsGoroutinesAndGivesThePipelineItsContext`.
- Green `f73e9b7`: `Pipeline.WithContext(ctx)` (default `context.Background()`), `Handle` uses `p.ctx`. `cmd/archiver/lifecycle.go` `runService(ctx, cancel, main, loops, closers...)`: loops (queue, monitor, backup scheduler) run under one `sync.WaitGroup`; when `main` (`client.Run`) returns it cancels, waits for the group, then calls the closers in order (client, store) and joins their errors with `main`'s. `serve` runs under a cancellable child of the signal context, passes it to the pipeline, starts no goroutine itself, and no longer defers the closes (the one early-return path after `store.Open` closes the store explicitly). Behaviour change: a failing `client.Close` or `st.Close` is now returned (exit 1) instead of ignored.
- Mutations (`specs-h13.json`): `h13-leak-ingest`, `h13-leak-media`, `h13-leak-health` (a leaked goroutine in a constructor, caught by `TestMain`), `h13-handle-background`, `h13-withcontext-ignored`, `h13-svc-nowait`, `h13-svc-nocancel`, `h13-svc-closers-before-join`, `h13-svc-closer-order`, `h13-svc-stop-on-close-error`, `h13-serve-stray-go`, `h13-serve-noctx`.
- Concern (not worked around): the wa client registers the handler with `AddEventHandler`, which always reports success. A handler that returns after the serve context ended because it gave up a write therefore still counts as handled: for encrypted messages whatsmeow (`message.go` `decryptMessages`, `backgroundIfAsyncAck` with `SynchronousAck`) then sends the message receipt/ack, so the event is acked although not stored, if the socket is still up. `AddEventHandlerWithSuccessStatus` returning false would skip the ack (`Handler for <id> failed`). Left as is per the ruling.
- Not done: goleak in `internal/wa`, `internal/backup`, `internal/store` (the ruling named three packages).
- Specs and results: `specs-h13.json` (mutation specs commit `46252cb`; `h13-serve-open2` retargeted in the next commit because the `serve` rewrite moved its anchor text, now `354663b`) holds 40 mutants: 40 KILLED, 0 SURVIVED, 0 INVALID, 0 NOMATCH, run with `MUT_REPO=<worktree>` at the final code. Shared `specs.json` entries on the two non-test files this task changed (`internal/ingest/pipeline.go`, `cmd/archiver/main.go`): rf5a, rf5b, b1-map, b1-enq, h2-sleep, h2-perm, x10, h18-exit, h18-ntfy, cf-ingest, h6-loop-perm, y3, rf4c, ff-noretry: 14 KILLED, 0 SURVIVED, 0 NOMATCH (no spec needed repair).
- Gate: `make check` green at the fix commit (race tests, lint 0 issues, five builds).
- Commits: `00ce8be` test (cycle A), `da5800b` test (goleak), `838cd5b` test (red), `f73e9b7` fix, `46252cb` and `354663b` test (specs), this entry `docs`.

### H13/M5 fix round 1 (I1: the serve context must not cancel a healthy write)

- Finding: `Handle` ran every write under the serve context, so after SIGTERM `BeginTx` failed at once: a healthy event was not stored, a spurious write-failure alert fired, and whatsmeow could still ack it. Ruling: the serve context only ends the retry loop; each write attempt runs under `context.WithoutCancel`.
- Red `d3bc424` (`TestHandleStoresHealthyEventAfterTheServeContextEnded`: already-cancelled context, healthy store: `stored=[] writeFailed=1 seen=0 media=0`). Fix `47e546f`: `Handle` passes `context.WithoutCancel(p.ctx)` to the writes; `persist` stops its retry loop when the caller's context or `p.ctx` ends (`stopped()`). A back-off sleep (at most 160 ms) is not interrupted by the serve context; `TestHandleStopsRetryingWhenTheServeContextEnds` passes unchanged. A history chunk imported through `Handle` now also completes at SIGTERM (same helper).
- Mutations: `h13r1-writes-cancellable` KILLED; `h13-handle-background` retargeted to `persist` ignoring the serve context (its old anchor moved), KILLED; serve/ctx mutants and the 9 re-run h13 mutants KILLED; shared `specs.json` `rf5a` anchor repaired (`for !stopped() {`), 14/14 entries on the changed file KILLED. `make check` green.
- Not touched: the whatsmeow ack/redelivery decision (`AddEventHandlerWithSuccessStatus`, `EnableDecryptedEventBuffer`) stays with the owner.
