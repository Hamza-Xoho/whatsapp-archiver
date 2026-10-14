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
