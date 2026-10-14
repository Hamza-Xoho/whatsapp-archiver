# Phase 0 review — WhatsApp Archiver (built by Codex)

Reviewer: Claude Code (evaluation pass; nothing in the product was fixed).
Reviewed: `archiver/` at commit `25dbc6a` (25 commits). Standard: `docs/superpowers/plans/2026-10-14-phase0-capture-logger.md` and charter v1.1.
Evidence for every probe and mutation: `docs/reviews/2026-10-14-phase0-review-evidence/` (probe tests, mutation harness).

**Layout notes.** The brief says the planning docs are in `project-docs/` and the report goes to `project-docs/reviews/`. Neither folder exists. The docs are in `docs/` next to `archiver/`, so `AGENTS.md`'s `../docs/…` links still resolve (I opened all three). This report is therefore in `docs/reviews/`. All eight hidden entries (`.git`, `.gitignore`, `.golangci.yml`, `.github/`, `.claude/`, `.githooks/`, `.tools/`, `.cache/`) are present inside `archiver/`, so the "stop and ask" condition did not apply.

**Repo state.** `archiver/` is byte-identical to how I found it (HEAD `25dbc6a`, `git status` unchanged, every tracked and untracked file hashes the same) apart from `.tools/` and `.cache/`. Experiments ran in scratch copies. Nothing connected to WhatsApp.

---

## 1. Verdict

**Not ready for Task 12.** The reported gates are real: `make check` passes, lint reports 0 issues and the race runs are clean. They are green mainly because 55 of the 107 planned test cases and 9 of the 13 prior-art tests were never written, and 42 of 61 mutations of the Review Focus behaviours survived. Two defects can lose data or put the real account at risk: a single transient database error silently drops an event (B1), and the reconnect loop re-dials immediately after `client_outdated` (exactly what Task 12 step 5 triggers) and every 5 seconds after a temporary ban or stream replacement (B2). Beyond those, history import records wrong senders and empty text/no media for wrapped messages, media re-upload requests carry an empty reference, and inserts slow down quadratically (404 inserts/s at 30k rows). Do a fix round on the "before Task 12" list in section 7, each fix starting from a failing test, and re-run this review's probes and mutations.

---

## 2. Gate results

### 2.1 Setup friction (a finding in itself, see M1)

On this Mac (arm64, Apple Make 3.81) `make check` could not run as shipped:

1. `go` is not installed. The Makefile exports `.tools/go/bin` on `PATH`, but **Make 3.81 looks up simple recipe commands with its own start-up `PATH`**, so `make test` fails with `make: go: No such file or directory` even after Go is in `.tools/go`. Recipes that contain a shell metacharacter do find it (proved with a probe makefile). Workaround used: `PATH=.tools/bin:.tools/go/bin:$PATH make check`.
2. `.tools/bin/golangci-lint` is a **Linux x86-64 ELF** (`exec format error`). The Makefile's `tools` target only checks that the file exists, so it counts as installed. I installed the pinned v2.14.0 for darwin-arm64 from the GitHub release (checksum verified against the published checksums file) and kept the Linux file as `golangci-lint.linux-amd64.orig`.
3. Go 1.26.9 darwin-arm64 downloaded from go.dev into `.tools/go` (checksum verified), as the brief instructs.

### 2.2 `make check`

| Gate | Result |
| --- | --- |
| `go test -race ./...` | **pass** — 11 packages with tests ok, `internal/testkit` has none |
| `golangci-lint run` (v2.14.0: depguard, errcheck, forbidigo, govet, staticcheck, gofmt) | **pass** — `0 issues.` |
| Cross-build linux/amd64, darwin/amd64, windows/amd64 | **pass** — three real binaries produced (26.5 to 26.9 MB; `file` confirms ELF, Mach-O x86_64, PE32+). The Makefile hides compile errors with `2>/dev/null \|\|` fallbacks (M1). darwin is amd64 only; this Mac is arm64 |

### 2.3 Coverage (`go test -count=1 -cover ./...`)

| Package | Coverage | Package | Coverage |
| --- | --- | --- | --- |
| `cmd/archiver` | 16.2% | `internal/ingest` | 60.1% |
| `internal/archtest` | 92.5% | `internal/media` | 69.7% |
| `internal/backup` | 56.9% | `internal/retry` | 66.7% |
| `internal/config` | 92.9% | `internal/store` | **39.7%** |
| `internal/guard` | 100% | `internal/testkit` | 0% (helper) |
| `internal/health` | 50.6% | `internal/wa` | 13.7% |

`PROGRESS.md` recorded store at 57.4% (Task 2) and 48.3% (Task 6); it is now 39.7% because later tasks added store code without tests. Task 7's percentage was "not recorded".

### 2.4 Lint configuration and the Task 1 canary

- `depguard` (`.golangci.yml:15-22`) bans the root `go.mau.fi/whatsmeow` outside `internal/wa/*.go` while allowing the data sub-packages. **Works.**
- `forbidigo` bans **16 hand-picked method names** (`.golangci.yml:27`), with `internal/wa/rerequest.go` exempted wholesale (`:31`). The plan and charter CON-1 require a ban on every method starting with `Send`, `Set`, `Update`, `Join`, `Leave`, `Revoke`, `Build`, `Mark`, `Subscribe`, `Create`, `Reject`, `Upload` or `Logout`, with the single allowed exception `SendMediaRetryReceipt`. **Does not match, see H12.**
- **Canary reproduced in a scratch copy** (file importing the client and calling `SendMessage`): lint failed with `depguard` (`import 'go.mau.fi/whatsmeow' is not allowed…`) and `forbidigo` (`use of c.SendMessage forbidden…`); `go test ./internal/archtest` failed `TestRepoHasNoViolations` with `client-import` and `banned-call`. Matches what `PROGRESS.md` claims.
- **Hole probes (scratch copy):** adding `SetStatusMessage`, `SetGroupName` and `SubscribePresence` in `internal/wa/sneaky.go`, and a `SendMessage` call in `internal/wa/rerequest.go`, gave `golangci-lint: 0 issues` and a passing `internal/archtest`.

### 2.5 Race and repeat runs

`go test -race -count=20 ./internal/media/ ./internal/ingest/ ./internal/health/`: **ok** (media 4.0 s, ingest 11.9 s, health 2.5 s). No flake or race detected, but these suites barely exercise concurrency (one media test, with a `time.Sleep`).

### 2.6 Dependencies

`go mod tidy` is **not** a no-op: it moves `github.com/mdp/qrterminal/v3` and `golang.org/x/time` from `// indirect` to direct (`go.mod:21`, `:36`). Both are really used by our code (`internal/wa/wa.go:13`, `internal/media/queue.go:10`), so the dependencies are justified but mislabelled. `go.uber.org/goleak`, which the plan requires, is not in `go.mod` and no test uses it. `testify` is absent too (the plan snippets use it; the tests use plain `testing`, which is fine).

### 2.7 Git history

- Every task has its `test:` commit before the `feat:` commit; **no implementation commit touches a `_test.go` file** (checked commit by commit). Task 0 is a single docs commit as planned.
- All hashes in `PROGRESS.md` exist.
- `.githooks/pre-commit` works (tested in an isolated copy): with `.claude/green.lock` present a staged test edit is rejected; deleting or renaming away a test file is rejected with or without the lock; an implementation-only change passes; `--no-verify` bypasses it (as git always allows). `.claude/green.lock` is gitignored and absent.
- Nits (L3): commit bodies contain a literal `\n\n` instead of newlines; the Task 1 "canary output" is a one-line summary, not the pasted output; author identity on all 25 commits is `Shakeel Ahmad <openai@shakeelahmad.net>`, not Hamza's; no `Co-Authored-By` trailers (good).

---

## 3. Findings

Severity: **Blocker** = breaks CON-1/2, can lose data, or stops pairing/capture. **High** = a Review Focus behaviour is untested or wrong, a planned test is missing, or a whatsmeow misuse. **Medium** = unjustified deviation, weak test, missing doc step. **Low** = style.
"Executed" means I ran a probe or mutation; "read" means I traced it in code and whatsmeow's source but could not run it (live-only).

| ID | Sev | Area | What is wrong | Evidence | Plan ref | Suggested fix |
| --- | --- | --- | --- | --- | --- | --- |
| B1 | **Blocker** | ingest / data loss | Not every write is inside the retry-forever loop, and `Handle` throws away every error from `Ingest` and `ImportHistory`. The ID-mapping insert, the media `Enqueue` and all history ledger/mapping/chat writes run exactly once. One transient error (`database is locked`) therefore drops the event; the handler returns, so whatsmeow acks it (`SynchronousAck`), and no alert fires. | `internal/ingest/pipeline.go:55,57,59,62` (`_ =`), `:85-89` (mapping insert, unretried), `:128-132` (Enqueue), `history.go:49-69` (unretried). **Executed**: `TestProbeMappingWriteFailureDropsEvent`: one failed mapping insert → `messages stored=0, WriteFailed alerts=0`. | Global constraints "no silent drops"; Task 5 "Write failures never drop an event"; RF5 | Put mapping insert and enqueue inside the same retry-until-success closure; make `ImportHistory` use the same helper; send every error to `health.WriteFailed`. Failing tests first: `TestHandleWriteFailureRetriesUntilStored`, `TestMappingWriteFailureIsRetried`, `TestEnqueueFailureIsRetried`, `TestImportHistoryRetriesLedgerWrite`. |
| B2 | **Blocker** | wa.Run / account safety | The reconnect loop has no backoff and ignores account states. (a) After `ClientOutdated` it calls `Disconnect`, re-decides the version and **re-dials immediately**; with the Task 12 drill's `version_override` and the check off it re-uses the same rejected version, so it hammers the real account's handshake in a tight loop for the whole 5-minute drill. The plan's 30-minute wait exists only if the *version refresh itself errors*. (b) whatsmeow marks 405, `TemporaryBan`, `LoggedOut` and `StreamReplaced` as expected disconnects so it will not reconnect on its own; our outer loop re-dials every 5 s anyway. After `TemporaryBan` and `StreamReplaced` that is a live re-dial every 5 s, contradicting RISK-1 "alert and pause capture" and risking a ban escalation or a tug-of-war with another client. After `LoggedOut` whatsmeow has deleted the device (`Store.Deleted`), so each attempt fails instantly with `ErrDeviceDeleted`: no network traffic, but an error is logged every 5 s forever and the loop never settles. (c) After the first successful 5 s tick the code blocks in `<-ctx.Done()`, so a later `ClientOutdated` is never read: SC-9's "reconnects without a new build" cannot happen while running. (d) `go printQR` is spawned on every loop pass. | `internal/wa/wa.go:115-143` (re-dial at `:116`, `outdated` branch `:126-135`, 5 s tick `:136-141`, blocking wait `:138`, spawn `:119-121`). whatsmeow `connectionevents.go:107-111,126-141` and stream errors `:41-51` (`expectDisconnect`); `store/store.go:286-298` and `client.go:563-566` (`ErrDeviceDeleted` after logout). **Read, not executed** (needs the real service). | Task 4 `Run` text ("otherwise waits 30 minutes"), RF4, RF6, charter RISK-1, SC-9; Task 12 steps 5 and 7 | Extract the reconnect policy into a small pure state machine (event in, action out) and unit-test it with an injected sleep: 30-minute wait when the version did not change; stop re-dialling after `LoggedOut`/`TemporaryBan` until expiry; back off on `StreamReplaced`; keep listening for `ClientOutdated` at all times. Failing tests first: `TestClientOutdatedSameVersionWaits30Minutes`, `TestOutdatedAfterConnectedTriggersRefresh`, `TestTemporaryBanPausesCapture`, `TestLoggedOutStopsTheReconnectLoop`, `TestStreamReplacedBacksOff`. **Do not run Task 12 step 5 before this is fixed.** |
| H1 | High | store / scalability | Every `InsertMessage` materialises the whole `chat_aliases` view (a full scan of `messages`) because the alias check filters on `x.id`, which is not the primary-key prefix. Insert cost grows with table size, so a long history import is quadratic and live messages slow down as the archive grows. Extrapolating the measured curve: roughly 3 h for 500k messages and 12 h for 1M on this Mac. A restart during such an import loses the rest of the chunk (the ledger row is written first). | `store.go:142` and `migrations/0001_raw.sql:94-98`. **Executed**: 5k-row batches ran at 2,250, 1,165, 808, 579, 438, 404 inserts/s (30k rows); `EXPLAIN QUERY PLAN` shows `MATERIALIZE chat_aliases` / `SCAN messages`. (Mac numbers; the server will differ, the shape will not.) | SC-1, SC-3; Task 2 "alias check reuses the view"; Task 6 history target 3,650 days | Look up aliases of the one chat first (tiny), then probe `messages` by `(chat_jid IN (…), id)` on the primary key. Failing test first: `TestInsertMessageCostDoesNotGrowWithTableSize` (e.g. 20k inserts under a time budget, or assert the plan contains no `MATERIALIZE`). |
| H2 | High | ingest / retry | After 5 attempts `Ingest` retries with `retry.Policy{Max: 1, …}`; with `Max: 1` `retry.Do` never sleeps, so the "160 ms" interval is a no-op and the loop spins. A permanent error (e.g. a primary-key violation) spins forever too. Combined with whatsmeow letting a stuck handler run on in the background after 5 minutes (`client.go` handler queue), several goroutines can spin. | `pipeline.go:118-124`; `retry/retry.go:22-26`. **Executed**: `TestProbeWriteFailureRetryRate`: **35,760 insert attempts in one second** during a simulated disk failure; exactly one `WriteFailed` alert. Mutation `rf1c` (drop `ON CONFLICT` on events) survives: a duplicate event would spin. | RF5; Task 5 "keep retrying at the maximum interval" | Sleep between rounds (real or injected). Failing test first: `TestWriteRetryBacksOff` (assert attempts per fake-clock minute) and `TestDuplicateEventIsNotARetryableError`. |
| H3 | High | media / re-upload | `RequestMediaRetry` is always called with an empty `wa.MediaRef{}` (no message ID, chat, sender, `FromMe`, or media key) and the raw-proto argument is ignored, so the re-upload request cannot be valid. If whatsmeow does not reject it, the row is marked `retry_requested` and then expires to `failed` after 24 h. Expired history media is exactly what this path is for. | `media/queue.go:105`; `wa/rerequest.go:19-21`. **Executed**: `TestProbeExpiredRateLimit` shows `Info.ID="" Info.Chat="" MediaKey=0 bytes`. The `events.MediaRetry` struct carries `MessageID, ChatID, SenderID, FromMe`, i.e. what is needed. | Task 7 download flow; RF3; Table 1 "expired media" | Store `from_me` and build `types.MessageInfo` plus the key from the row's raw proto inside `wa`. Failing tests first: `TestRequestMediaRetryReceivesMessageInfoAndKey`, `TestExpiredMediaRetryCompletes`. |
| H4 | High | media / rate limit | Re-uploads beyond the limiter are never sent: `if q.limiter.Allow()` simply skips, the row stays `expired`, and nothing ever revisits it (not even after a restart). The plan says the rest "are sent as the limiter refills". The limiter is also wall-clock, so it cannot be tested with the fake clock the plan specifies. | `media/queue.go:104-109`; `PROGRESS.md:66` admits wall-clock. **Executed**: 30 expired rows → 10 requests sent, **20 rows left `expired` forever**, 0 downloads attempted on a later restart. Mutations `rf3b`, `rf3c` survive. | Task 7 "30 expired items … exactly 10 … rest as the limiter refills"; Table 1 `TestExpiredRequestsAreRateLimited` | Make `expired` rows eligible for a rate-limited retry pass; use `limiter.AllowN(clock.Now(), 1)` with the injected clock. Failing test first: `TestExpiredRequestsAreRateLimited` (10 now, the other 20 after the fake clock advances). |
| H5 | High | history import | The hand-built `events.Message` skips what `Client.ParseWebMessage` does (Table 2 lists it; the plan's Task 6 says to use it). Lost or wrong: **(a)** wrapped messages (ephemeral, view-once, document-with-caption) are not unwrapped, so `text` is empty and **no media row is queued** for them (live path unwraps via `UnwrapRaw`); **(b)** sender of *own* messages is the other party in a DM, and the **group JID** in a group (and the group JID is inserted into `participants`); **(c)** `PushName` is dropped; **(d)** `key.participant`-only fallbacks and thread metadata are ignored. Note `ParseWebMessage` rewrites edits to the original ID, so naive use would lose edits: handle `MESSAGE_EDIT` before calling it. | `ingest/history.go:95-113` (`sender := chatJID` at `:104`, no unwrap at `:112`); whatsmeow `client.go:1013-1065` and `types/events/events.go` `UnwrapRaw`. **Executed**: `TestProbeHistoryWrappedMessagesLoseContent` (history: `EPH\|\|`, `DOC\|\|`, 0 media enqueued; live path on the same input: "disappearing hello"); `TestProbeHistoryOwnMessageSender` (`DM_FROM_ME` sender = the contact; `GRP_FROM_ME` sender = the group JID; participants `120363@g.us` with empty name). `PROGRESS.md:58` justifies this with "no parser dependency". | Task 6 "Consumes … its per-message parse helper"; Table 2 row "Parse a history message"; brief §5 item 5 | Expose a small parse function from `internal/wa` (it needs the client only for the own JID) and inject it into `ImportHistory` as an external-boundary fake. Failing tests first: `TestHistoryEphemeralAndDocumentCaptionAreUnwrapped`, `TestHistoryFromMeMessageHasOwnJIDAsSender`, `TestHistoryPushNameIsStored`, `TestHistoryEditIsStillAnEvent`. |
| H6 | High | chats table / privacy | **(a)** `UpsertChat` overwrites the name with the empty string on every message (the normaliser sets no name), so a name learned from history is wiped within the same import. **(b)** `ImportHistory` upserts the chat and its name **before** the exclusion filter, so an excluded chat's JID and name are written to `chats`. `status`/`chats` therefore show blank names, and a private chat's title is stored although the setup guide implies exclusion means "never stored". | `store.go:124`, `pipeline.go:95`, `history.go:66-69`. **Executed**: `TestProbeChatNameWipedByLiveMessage` (`chats = [g@g.us\|]` right after importing a chat named "Study Group"); `TestProbeExcludedChatNameStoredFromHistory` (`chats = [secret@g.us\|Private Therapy Group]`, no messages stored). | Task 6 "upserts chat names"; SC-2; ADR-13; setup guide "must never be stored" | Upsert name only when non-empty (`COALESCE(NULLIF(excluded.name,''), name)`); move the history chat upsert behind the filter. Failing tests first: `TestLiveMessageKeepsKnownChatName`, `TestExcludedChatNameNotStoredFromHistory`. |
| H7 | High | chat events | Group, archive, mute and pin events keep almost nothing and are not idempotent. `Value` is only `Notify`/`Reason`/the constant "archive"/"mute"/"pin": the new group name, topic, joins, leaves, promotions, `Sender`, and whether the chat is now archived/muted/pinned are all dropped; the WhatsApp timestamp is not stored; no raw payload is kept; `KindParticipants` is never produced. The `EventID` hashes `fmt.Sprintf("%v", evt)`, which prints pointer addresses, so the same event gets a different ID each time and redelivery duplicates. | `normalize.go:145-174` (`:171`). **Executed**: `TestProbeChatEventShape`: identical `GroupInfo` twice → different `EventID`s; `Value=""`, `Timestamp=0`, `RawProto=0 bytes`; archive true and false both give `Value="archive"`. Mutation `y3` (chat events not routed) survives. | Task 3 (group info, participants, archive, mute); Global "raw protobuf"/"timestamps as given"; Task 12 step 3 | Marshal the event payload (JSON, or the `Action` proto for app-state events) into `Value`/`RawProto`; take the timestamp from the event; derive the ID from stable fields. Failing tests first: `TestChatEventsStored`, `TestSameGroupInfoTwiceIsOneRow`, `TestArchiveOnAndOffAreDistinct`. |
| H8 | High | normaliser | Any `ProtocolMessage` that is not an edit is recorded as a **revoke**, including `EPHEMERAL_SETTING` and the other protocol messages WhatsApp sends in volume at pairing. An archive that records bogus "deleted" events is worse than one that records nothing. | `normalize.go:94-105` (no `GetType()` check). **Executed**: `TestProbeProtocolMessageMisclassified`: `EPHEMERAL_SETTING` → `Kind=revoke`, `TargetID=""`. Mutations `rf2a`, `rf2b` (edit/revoke logic removed) survive: no test covers it. | Table 1 `TestProtocolEditOldAndNewFormats`; Task 3 edit/revoke | Switch on `ProtocolMessage.Type` (`REVOKE`, `MESSAGE_EDIT`); also handle the top-level `EditedMessage` shape (`IsEdit`) that whatsmeow's `UnwrapRaw` produces; ignore or type other kinds. Failing tests first: `TestProtocolEditOldAndNewFormats`, `TestNonRevokeProtocolMessageIsNotARevoke`, `TestRevokeNormalizes`. |
| H9 | High | history ledger | The chunk ID is a hash of `(sync type, chunk order, progress, conversation count)`, so two different chunks with the same numbers (very likely across a re-pair, which Task 12 step 7 does) collide and the second ledger row is silently dropped by `ON CONFLICT DO NOTHING`. Task 12 step 9 measures chunk counts from this ledger. | `history.go:29`. **Executed**: `TestProbeLedgerCollision`: two distinct chunks at progress 100 → **1 ledger row**, 2 messages stored. `TestLateChunkAfterCompleteIsStored` only passes because its two chunks use different progress values. | RF9; Task 6 "write the ledger row first … received time" | Include `received_at` (or a counter) in the ID, or drop the unique ID and use an autoincrement key. Failing test first: `TestTwoDistinctChunksWithSameProgressAreBothLedgered`. |
| H10 | High | health / alerts | Most planned alert rules do not exist. Only `logged_out`, `temp_banned`, `stream_replaced`, `client_outdated`, write failure, panic, failed backup, disk-low and a generic "no events" are implemented. Missing: `DisconnectedAfter` (never read), `PhoneQuietAfter` (never read), "recovered" messages, the `alertGate` type. `PROGRESS.md` calls this "periodic recovered-alert coverage deferred", but the rules themselves are absent. Task 12 step 6 (20-minute outage must alert) and RISK-3 (phone-quiet alert at 10 days) cannot pass. | `health/health.go:47-59,88-104`. **Executed**: `TestProbeDisconnectedNeverAlerts` (20 days "disconnected", only the generic "Capture may be stalled" text, 79 times; no disconnect or phone-quiet alert); `TestProbeNoRecoveredMessage`. Mutations `rf4a`, `rf4b`, `rf4c`, `rf5b`, `rf5c`, `rf6a`, `rf6b`, `rf10d`, `rf10e` all survive. | Task 9 alert rules and 11-item test list; RF4, RF5, RF10; charter RISK-3, NFR-3 | Implement the missing rules and `alertGate` with recovery; add the planned tests (section 4). |
| H11 | High | media worker panic | A media-worker panic is recovered but passes `onPanic = nil`, so it is not counted and not alerted; the claimed row stays `downloading` until the next restart, then retries and can panic again. `PROGRESS.md` defers "panic-row failure marking" to Task 12, but a panicking fake downloader proves it offline. | `media/queue.go:80`. **Executed**: `TestProbeWorkerPanic`: poison row `state="downloading"`, sibling row `done`. Mutations `rf10a`, `rf10c` survive. | RF10 ("recovered, logged with stack, counted, alerted"); Task 7 "row marked failed reason panic" | Pass a callback that marks the active row `failed: panic` and calls `health.Panic("media")`. Failing test first: `TestWorkerPanicMarksRowFailedAndIsCounted`. |
| H12 | High | CON-1 guard | The lint/archtest ban is a 16-name list, not the prefix rule in the plan and charter CON-1 ("lint bans every send method outside one allow-listed file"). 43 of the 58 whatsmeow `Client` methods with the banned prefixes are not covered (a few are harmless or allowed, such as `SendMediaRetryReceipt` and the proxy/HTTP-client setters, which a prefix rule would allow-list explicitly), including user-visible ones: `SetStatusMessage`, `SetGroupName/Topic/Photo/Announce/Locked`, `SetPrivacySetting`, `SetDisappearingTimer`, `SubscribePresence`, `SendAppState`, `SendFBMessage`, `UpdateBlocklist`, `JoinGroupWithInvite`, `CreateNewsletter`, `BuildReaction`. And the allow-listed file is exempt from *all* bans, so `SendMessage` inside `rerequest.go` passes. `TestScanAllowsRetryReceiptInAllowListedFile` cannot catch this (see M7). The code itself is clean today (only `SendMediaRetryReceipt` is called). | `.golangci.yml:27,31`; `archtest/scan.go:26,31,68-70`. **Executed**: scratch probe, lint `0 issues` and archtest `ok` with `SetStatusMessage`/`SetGroupName`/`SubscribePresence` in `internal/wa/sneaky.go` and `SendMessage` in `rerequest.go`. The count comes from grepping the pinned `*.go` files for `func (cli *Client) (Send|Set|Update|Join|Leave|Revoke|Build|Mark|Subscribe|Create|Reject|Upload|Logout)…`: 58 methods, 15 on the list, 43 not. | Global CON-1; Task 1 Step 3; charter CON-1 | Use a prefix regex plus a short allow-list of harmless names (`SetProxy…`, `SetMediaHTTPClient`, …); narrow the file exemption to `SendMediaRetryReceipt`. Failing tests first: `TestScanFlagsEveryBannedPrefixMethod`, `TestAllowListedFileStillRejectsSendMessage`; re-run the canary with `SetStatusMessage`. |
| H13 | High | tests missing | **9 of the 13 tests named in `docs/prior-art.md` Table 1 do not exist**: `TestChatEventsStored`, `TestExpiredMediaRetryCompletes`, `TestExpiredRequestsAreRateLimited`, `TestHandleRecoversPanic`, `TestMappingsInChunkApplyToItsMessages`, `TestNonsensicalHistoryTimestampStoredAsGiven`, `TestPollVoteCurrentAndNewVersions`, `TestProtocolEditOldAndNewFormats`, `TestSameMessageUnderLIDAndPhoneStoredOnce`. The brief (§4 rule 4) says every one must exist and pass. Overall 55 of 107 planned test cases are missing (section 4), and 42 of 61 mutations survive (section 5). Review Focus items 2, 3, 4, 5, 6, 9 (messages), 10 are essentially untested at behaviour level. | Script: `grep -oE 'Test…' docs/prior-art.md` against `func Test…`. | Brief §4.4; plan Step 1 of Tasks 2-11 | Write the missing tests (section 7). |
| H14 | High | record / replay / scrub | **(a)** `Scrub` leaves phone numbers in place: the regex needs digits directly before `@`, so device-suffixed JIDs (`4915…:12@s.whatsapp.net`, the normal real-world shape) are untouched, and numbers inside the base64 protobuf (participant, mentioned JIDs) are never decoded. **(b)** There is no command to run `Scrub`, `Replay` or `Load`, yet Task 12 step 10 needs one. **(c)** The recorder keeps only `*events.Message`; undecryptable, history sync, group info, archive/mute/pin and media retry are dropped. **(d)** Replay loses the timestamp (year 1), push name, alt JIDs and group flag. **(e)** `DumpRaw`, `testdata/events`, `testdata/golden` and `TestReplayMatchesGolden` do not exist. | `ingest/scrub.go:11`; `record.go:45-56` (only `Message`), `:57-78` (no timestamp); `cmd/archiver/main.go:32-86` (subcommands). **Executed**: `TestProbeScrubMissesNumbersInsideProtobufAndDeviceJIDs` (number still present in JSON and in decoded protobuf); `TestProbeReplayRoundTripLosesFields` (`ts=-62135596800`, push/alt empty, 5 of 5 other kinds not recorded). | Task 10; Task 12 steps 3 and 10; CON-5 | Rebuild scrub as a proper tool (decode, rewrite, re-encode) with a CLI entry; record every listed kind including all `MessageInfo` fields; add `DumpRaw` and a synthetic golden scenario now. Failing tests first: `TestScrubRemovesNumbersInsideProtobuf`, `TestScrubDeviceSuffixedJID`, `TestRecordLoadRoundTripForEachKind`, `TestReplayMatchesGolden`. |
| H15 | High | service / backup | `deploy/archiver.service` sets `ProtectSystem=strict` with `ReadWritePaths` limited to the data folder, while the setup guide's config writes snapshots to `/srv/backups/whatsapp-archiver`. Under `strict` that path is read-only, so every nightly snapshot fails (alert every night) and Task 12 step 11 has nothing to restore. | `deploy/archiver.service:15-16`; `docs/phase0-setup.md:29`. **Read** (cannot run systemd here). | Task 8, Task 11 service, Task 12 step 11 | Add the backup path to `ReadWritePaths` (and say so in the guide), or put the destination under the data folder. Test: a unit check on the service file or a setup-guide dry-run on the server. |
| H16 | High | backup retention | Retention keeps the lexically last `Keep` **directories** in the destination and `RemoveAll`s the rest, whatever they are. Any sibling folder sorting after the timestamp names wipes snapshots, including the one just taken. | `backup/backup.go:75-97` (`:91`). **Executed**: `TestProbeRetentionDeletesForeignDirs`: destination with `0-keep-me`, `2023-family-photos`, `media`, `Keep: 1` → only `media` remains, **the fresh snapshot was deleted**. A failed snapshot also leaves an empty folder that counts toward `Keep`. Mutation `x13` (retention off) survives. | Task 8 ("delete snapshot folders beyond Keep") | Only consider directories whose names match the snapshot timestamp pattern; remove a failed snapshot's folder. Failing tests first: `TestRetentionOnlyRemovesSnapshotFolders`, `TestKeepTwoLeavesTwoSnapshots`. |
| H17 | High | CLI `status` / `chats` | Output is far below the plan: raw Unix integers (no time zone; `time_zone` config is never used), no client version and reason, no phone-last-active, no "complete back to <date>", expired/pending not shown separately, no totals, no recovered panics, no data-folder size, no `health.json` fields beyond state/last event. And both commands open the archive through `store.Open`, i.e. **with the write pool, WAL pragma and migrations**: `status` modified `archive.db` and a mistyped `--data` creates a new empty archive. In the Task 12 step 11 restore check this alters the snapshot copy. No golden test exists (`PROGRESS.md:94` says none was needed). | `cmd/archiver/main.go:125-164` (`store.Open` at `:126`, `:147`). **Executed**: `TestProbeStatusWritesArchive`: `archive.db` mtime changed by `status`, and `status --data <typo>` created `archive.db`. | Task 11 status/chats spec and Step 1 golden tests; charter FR-23/24; Global "time shown in the configured zone" | Add a read-only open (`mode=ro`, no migrate); format times in `cfg.TimeZone`; add the missing fields; add golden tests. Failing tests first: `TestStatusGolden`, `TestChatsGolden`, `TestStatusDoesNotWriteArchive`. |
| H18 | High | CLI / unpaired restart | `run` only checks that `session.db` exists. `pair` creates that file on start (before any scan), and a logout keeps it. `run` then fails with the bare message `not paired`, exits 1, and `Restart=always` retries every 10 s forever with no alert and no "run `archiver pair`" hint. This is the state after *any* logout followed by a restart (RF4 "stays up reporting logged_out instead of crash-looping"). | `main.go:57`; `wa/wa.go:94-96`; `deploy/archiver.service:11`. **Executed**: `TestProbeRunWithEmptySessionFile`: `exit=1 stderr="not paired"`. | RF4; Task 11 "refuses to start if not paired (run archiver pair first)" | Ask the client whether the device is paired; print the hint; use `RestartPreventExitStatus` for that exit code; send a one-off ntfy. Failing test first: `TestRunWithUnpairedSessionFileSaysPairFirst`. |
| M1 | Medium | Makefile / tooling | (a) Make 3.81 ignores the exported `PATH` for simple recipes, so the "`.tools/go`" bootstrap path never works on macOS (section 2.1). (b) Nothing bootstraps Go on a fresh machine; the guide does not mention it. (c) The `tools` target accepts a wrong-architecture binary. (d) `curl … \| sh` without a checksum. (e) Cross-build covers darwin/amd64 only (this Mac is arm64); the Linux server's architecture is assumed amd64. (f) `2>/dev/null \|\| …` hides cross-build errors. | `Makefile:7,27,33,37`. **Executed** (a, b, c). | Brief "builds a binary for Hamza's Linux server and for the Mac's architecture" | Set `SHELL`-level `PATH` via `export` plus `$(shell …)` lookups or require GNU Make 4; add a `go` bootstrap target with a checksum; check the binary runs (`--version`); build darwin/arm64 and linux/arm64 too; drop the fallbacks. |
| M2 | Medium | alerts blocking | `Notify` runs synchronously inside the event-handler path (`SetState`, `WriteFailed`, `Panic`) with a background context: with ntfy unreachable it blocks up to about 90 s (5 tries, 15 s HTTP timeout each) and cannot be cancelled by SIGTERM. | `health.go:116`; `ntfy.go:24-45`. **Read.** | Task 9; Global concurrency | Send alerts from a small owned goroutine with a bounded queue. Test: `TestSlowNotifierDoesNotBlockSetState`. |
| M3 | Medium | wa observability | `whatsmeow.NewClient(device, nil)` gives whatsmeow a no-op logger, so connection failures, 405/ban details and decrypt warnings never reach journald. During Task 12 you will want them. `GetLatestVersion(ctx, nil)` uses `http.DefaultClient` with no timeout, so a hung request blocks start-up. | `wa/wa.go:61`, `:72`; whatsmeow `update.go:44-47`. **Read.** | Task 4; Global "JSON logs to journald" | Adapt `slog` to `waLog.Logger`; pass an `http.Client{Timeout: 15s}`. |
| M4 | Medium | media robustness | Whole files are buffered in memory (documents up to 2 GB, two workers); the plan's `Download` signature mandates `[]byte`, whatsmeow has `DownloadToFile`. Five attempts spread over **153 ms** (10 ms base) end in a terminal `failed`, so a network blip of a few seconds loses the file for good. Workers poll the database every 100 ms forever, which makes the wake channel pointless. File extensions come from `mime.ExtensionsByType`: JPEG photos are named `.jfif`, MP3 `.m2a` on this Mac. | `wa/download.go:34`; `media/queue.go:98`, `:78`, `:161`. **Executed**: `TestProbeBackoffWindow` (5 attempts, 153 ms, terminal), `TestProbeFileExtensions`. | Task 7; RF3 ("network drop") | Seconds-scale backoff and a later re-queue for transient failures; stream to a file; fixed extension table. Tests: `TestTransientFailureIsRetriedAfterBackoff`. |
| M5 | Medium | goroutines / shutdown | No `goleak` anywhere although the plan requires it in every package that starts goroutines. `serve` does not wait for the queue, monitor or backup scheduler before closing the store. `printQR` goroutines multiply (B2d). The media test uses `time.Sleep(50ms)` and never waits for `Run` to return. | `go.mod`; `cmd/archiver/main.go:113-124`; `media/queue_test.go`. | Engineering Principles "Concurrency"; Tasks 5, 7, 9 | Add goleak to `TestMain` of `ingest`, `media`, `health`; own and join goroutines in `serve`. |
| M6 | Medium | dependencies | `go mod tidy` changes `go.mod` (section 2.6); the weekly bump workflow runs `go mod tidy`, so its first PR will contain this diff. | `go.mod:21,36`. **Executed** (tidy in scratch). | Brief §4.8 | Commit the tidy result. |
| M7 | Medium | weak tests | `TestScanAllowsRetryReceiptInAllowListedFile` is vacuous (its fixture calls `SendMediaRetryReceipt`, which is not on the banned list; emptying the allow-list leaves it green: `z1`). `TestIDMappingIsIdempotent` discards errors (`rf1d` survives). `TestDecideSkipsStickerAndLargeVideo` passes via the Download list, not the Skip list. `TestLateChunkAfterCompleteIsStored` never checks the late chunk's messages (`rf9c` survives). `TestVerifyRejectsTruncated` feeds a non-database, so the integrity-check branch is untested (`x12`). `TestRetryGivesUpAtMax` does not check the wrapped error. | Mutation table (section 5). **Executed.** | Engineering Principles "one behaviour per test … fails if behaviour breaks" | Strengthen each as named in the fix plan. |
| M8 | Medium | docs | `phase0-setup.md` is not followable by a Linux beginner: no expected output after any command (plan requires it); `deploy/archiver.service` is never copied to the server (the guide only copies the binary); `config.yaml` ownership/permissions unstated (a root-owned file makes the service user fail to read it); no SSH-from-Mac/Windows step, no ntfy test step, no update commands ("build the current revision, replace the binary" has none), no rsync timer unit (only the command, with `--delete`, which would mirror an accidental wipe into the backup), nothing about finding JIDs for the exclude list before first capture. `phase0-acceptance.md` is a bare checklist: no expected results, no spike numbers, no SQL for step 8, no per-group table for step 9, no "ntfy alert within 5 minutes" in step 5, and sticker/video-skipped and "nothing appears on anyone's phone" checks are missing from step 3. | `docs/phase0-setup.md:38,53`; `docs/phase0-acceptance.md`. | Task 11 guide; Task 12 steps 1-13 | Rewrite after the code fixes; have a non-expert follow it on a clean VM. |
| M9 | Medium | `version` command | `archiver version` hard-codes the whatsmeow pseudo-version, so the weekly bump will make it lie, and it omits the "client version last used" the plan asks for. | `main.go:25,38-40`. | Task 11 | Read `debug.ReadBuildInfo()`; print the version from `health.json`. |
| M10 | Medium | config validation | `exclude` entries are not validated: a typo or a bare phone number silently excludes nothing, and the chat is archived (and raw rows can't be deleted). `backup.time` is validated only when the scheduler starts. `time_zone` and `data_dir` are accepted but inert. | `config/config.go:71-89`; `main.go:54`. | Global "Scope"; Task 1 | Parse each exclude as a JID at load time; validate time zone and backup time; remove inert keys. Tests: `TestLoadRejectsMalformedExcludeJID`. |
| M11 | Medium | schema / migrations | One hard-coded file `0001_raw.sql`; no migration runner, so the first schema change in phase 1 has no path. `TestReopenRunsMigrationsOnce` only checks one `INSERT OR IGNORE`. | `store.go:81-93`. | Task 2 "runs embedded migrations once" | Add a numbered-migration loop now (it is ten lines) or note the deferral. |
| M12 | Medium | licence notice | `NOTICE` and `docs/prior-art.md` Table 3 say wacli-derived files carry the MIT header; no Go file has one (`grep` finds none). Either the files are not adapted (Table 3 is wrong) or the headers are missing. | `NOTICE:5`; `internal/**`. | Global "Licences" | Decide which is true and fix the other. |
| M13 | Medium | whatsmeow usage | (a) The wa wrapper never wires `PairError` or QR-timeout feedback: if pairing fails the user sees nothing. (b) `pair` prints "Paired. Capture is running." **after** `Run` returns, i.e. when you press Ctrl-C, even if pairing never succeeded. (c) `props.go` leaves `PlatformType` at `UNKNOWN`; wacli (the stated source) sets it explicitly (default `CHROME`). Whether that changes iOS history depth is **not verified** (live-only, spike 1/2). (d) whatsmeow itself sends `SetPassive(false)` on every connect (`connectionevents.go:200`); it is not on the plan's list of accepted traffic. | `main.go:119-122`; `wa/props.go`; wacli `cmd/wacli/main.go:30-50`. | Task 4, Task 11 `pair`, spike 4 | Print on `events.PairSuccess`; add (d) to the spike 4 receipt-audit checklist; record the platform type in the acceptance log. |
| L1 | Low | dead code | The audio branch `if x.GetPTT() { kind = "audio" }` is a no-op (and mutation `y7` survives); `var _ = waHistorySync.HistorySync_INITIAL_BOOTSTRAP` only keeps an import alive; `chooseVersion` always returns a nil error; `KindParticipants` is never produced; `mediaMessage` repeats the same five-line block per type. | `normalize.go:57-63`; `history.go:115`; `wa/wa.go:67-80`; `wa/download.go:38-70`. | Engineering Principles "no dead code" | Delete or implement. |
| L2 | Low | counters | `ImportHistory` returns `stored` for duplicates (a chunk whose only message already exists reports `stored=1`). `Seen` rewrites `health.json` on every event (about 0.3 ms each, I/O churn during history). `archiver chats` marks `excluded` only on a canonical-JID match. | `history.go:88`; `health.go:38-46`; `main.go:161`. **Executed** (counters). | Task 6, Task 9, Task 11 | Return the `inserted` flag from `Ingest`; write the snapshot on state change and every minute only. |
| L3 | Low | commits | Literal `\n\n` in commit bodies; canary output summarised not pasted; authorship `Shakeel Ahmad <openai@shakeelahmad.net>`. | `git log`. | Task 1 Step 5 | Cosmetic. |
| L4 | Low | CI | `ci.yml`/`bump-whatsmeow.yml` pin `actions/checkout@v6`, `actions/setup-go@v6`, `peter-evans/create-pull-request@v8`. **Not verified** (no network to GitHub's action registry from here). | `.github/workflows/*.yml`. | Task 1 | Confirm the versions exist and pin by SHA. |
| L5 | Low | PROGRESS accuracy | Several "complete" notes overstate: Task 9 ("all immediate critical states … implemented" while disconnect, phone-quiet and recovered alerts are absent), Task 10 ("goldens" deferred but `DumpRaw` and the planned synthetic golden were required now), Task 11 ("no golden needed"). | `PROGRESS.md:58,66,80,87,94`. | Brief §5 | Correct the log. |

---

## 4. Conformance matrix (Tasks 0 to 11)

Legend: **exists?** ✓ / ✗. **passes?** all existing tests pass. **would fail if behaviour broke?** *Yes* = a relevant mutation was killed; *Weak* = test exists but a relevant mutation survived or the assertion is thin; *No* = vacuous; *—* = missing. Counts: **107 planned cases, 52 exist in some form, 55 missing.**

### Task 0: prior-art intake

| Planned | Exists? | Notes |
| --- | --- | --- |
| `docs/prior-art.md` with Tables 1 to 3 | ✓ | Table 2 names spot-checked against the pinned source (`GetLatestVersion(ctx, *http.Client)`, `SynchronousAck`, `AutomaticMessageRerequestFromPhone`, event structs, `ParseWebMessage`, `SendMediaRetryReceipt`, `DecryptMediaRetryNotification`): all correct. Table 2 lists `ParseWebMessage` but the code does not use it (H5). No row for `SendHistorySyncServerErrorReceipt` (not evaluated). Table 1: **9 of 13 named tests missing** (H13). Table 3 claims adaptations with no MIT headers (M12). |
| Step 1 `git init` and commit existing docs | ✗ (documented deviation) | The brief made `../docs` read-only, so the repo starts at the prior-art commit. Accepted. |

### Task 1: skeleton, config, guardrails

| Planned test | Exists? | Passes? | Fails if broken? | Notes |
| --- | --- | --- | --- | --- |
| `TestLoadDefaults` | ✓ | ✓ | Yes (reads every default) | |
| `TestLoadKeepsDefaultsForMissingFields` | ✓ | ✓ | Yes | |
| `TestLoadRejectsUnknownKey` | ✓ | ✓ | **Yes** (`x11` killed) | |
| `TestLoadRejectsBadVersionOverride` | ✓ | ✓ | Yes | |
| `TestScanFlagsClientImportOutsideWA` | ✓ | ✓ | Yes | |
| `TestScanFlagsBannedCall` | ✓ | ✓ | Yes (for the 16 names only, H12) | |
| `TestScanAllowsRetryReceiptInAllowListedFile` | ✓ | ✓ | **No** (vacuous, `z1`) | M7 |
| `TestRepoHasNoViolations` | ✓ | ✓ | Yes (canary reproduced) | |
| Step 5 canary | ✓ reproduced | | | depguard, forbidigo, archtest all fail as planned |
| Files (hooks, skills, workflows, `AGENTS.md` 47 lines, `NOTICE`, `.gitignore`) | ✓ | | | Hooks tested (2.7). `.golangci.yml` weaker than specified (H12). |

### Task 2: store, retry, guard, testkit

| Planned test | Exists? | Passes? | Fails if broken? | Notes |
| --- | --- | --- | --- | --- |
| `TestRetryStopsOnSuccess` | ✓ | ✓ | Yes | |
| `TestRetryGivesUpAtMax` | ✓ | ✓ | Weak | does not check the wrapped error |
| `TestGuardRecoversPanic` | ✓ | ✓ | **Yes** (`rf10b`) | |
| `TestInsertMessageIsIdempotent` | ✓ | ✓ | Weak | `rf1a` survives (clause is redundant behind the `EXISTS` check) |
| `TestInsertMessageUnderKnownAliasIsDuplicate` | ✓ | ✓ | **Yes** (`rf1b`) | |
| `TestAliasLearnedLaterCollapsesInView` | ✓ | ✓ | **Yes** (`rf8e`) | |
| `TestEventForUnknownMessageIsStored` | ✓ | ✓ | **Yes** (`rf2d`) | |
| `TestIDMappingIsIdempotent` | ✓ | ✓ | Weak | ignores errors (`rf1d`) |
| `TestOpenGapClosesWhenMessageArrives` | ✓ | ✓ | **Yes** (`rf7b`) | |
| `TestNoUpdateOrDeleteOnRawTables` | ✗ | — | — | `x8` (raw table overwritten) survives; DB accepts UPDATE/DELETE (probe) |
| `TestWriteErrorIsReturned` | ✓ | ✓ | Yes | |
| `TestChatStatsOldestNewest` | ✓ | ✓ | Yes | |
| `TestTimestampStoredAsGiven` | ✓ | ✓ | Yes (store level only) | |
| `TestReopenRunsMigrationsOnce` | ✓ | ✓ | Weak | trivial, M11 |

### Task 3: normaliser and scope

| Planned case | Exists? | Fails if broken? | Notes |
| --- | --- | --- | --- |
| plain text | ✓ `TestPlainTextNormalizes` | Yes (`x3`) | |
| reply; forwarded | ✓ `TestReplyAndForwarded` | Yes | |
| image; sticker | ✓ `TestImageAndStickerMedia` | Yes | |
| document; audio voice note; video | ✗ ✗ ✗ | — | `y7` (voice-note detection) survives |
| ephemeral / view-once unwrap | ✗ | — | live path relies on whatsmeow; history path does not unwrap (H5) |
| protocol edit (old and new formats) | ✗ | — | `rf2a` survives; Table 1 test missing |
| revoke | ✗ | — | `rf2b` survives; non-revoke misclassified (H8) |
| reaction; pin; poll vote (current and newer) | ✗ ✗ ✗ | — | `rf2c` survives; Table 1 `TestPollVoteCurrentAndNewVersions` missing |
| edit with unknown target (normaliser) | ✗ | — | store-level version exists |
| undecryptable (deterministic ID, fail mode) | ✓ `TestUndecryptableIsDeterministic` | Yes | |
| alternate JID → mapping | ✓ `TestAlternateJIDCreatesMapping` | **Yes** (`rf8c`) | |
| group info change; participant join/leave; archive and mute | ✗ ✗ ✗ | — | H7 |
| scope: excluded chat dropped for **every kind** | partial | Weak | only `KindMessage` |
| scope: excluded PN also excludes mapped LID | ✓ `TestScopeExcludesDirectAndAlias` | **Yes** (`rf8a`) | |
| scope: …and the other way round | ✗ | — | |
| scope: not excluded → kept; `status@broadcast`; noise kinds; group info kept | ✓ `TestScopeAlwaysSkipsNoise` | Yes | |

11 of 27 exist.

### Task 4: wa wrapper

| Planned test | Exists? | Passes? | Fails if broken? | Notes |
| --- | --- | --- | --- | --- |
| `TestSessionDSNEnablesForeignKeys` | ✓ | ✓ | Yes | also opens the real sqlstore on modernc |
| `TestDecideVersion` (table) | ✓ | ✓ | **Yes** (`rf6c`) | |
| `TestHistoryPropsUseConfiguredLimits` | ✓ | ✓ | Yes | |
| `TestClassifyDownloadErr` | ✓ | ✓ | Yes | |
| `Run` / pairing | live-only per plan | | | The reconnect *policy* is pure logic and should be unit-tested (B2) |

### Task 5: ingest pipeline

| Planned test | Exists? | Fails if broken? | Notes |
| --- | --- | --- | --- |
| `TestHandleStoresKeptMessage` | ✓ | **Yes** (`y1`) | |
| `TestHandleDropsExcludedChat` | ✓ | **Yes** (`x1`) | |
| `TestHandleWriteFailureRetriesUntilStored` | ✗ | — | `rf5a` survives; B1, H2 |
| `TestHandleRecoversPanic` | ✗ | — | `rf10a` survives |
| `TestUndecryptableThenResentClosesGap` | ✓ | **Yes** (`rf7a`) | |
| `TestMappingFromAltJIDStoredBeforeFilter` | ✗ | — | `rf8b` survives |
| `TestSameMessageUnderLIDAndPhoneStoredOnce` | ✗ | — | store-level version exists; Table 1 test missing |
| `TestChatEventsStored` | ✗ | — | `y3` survives |
| `TestFromMeUpdatesPhoneActivity` | ✗ | — | `y2` survives |
| `TestUnknownEventIgnored` | ✗ | — | |
| `goleak.VerifyNone` | ✗ | — | M5 |

### Task 6: history import

| Planned test | Exists? | Fails if broken? | Notes |
| --- | --- | --- | --- |
| 2 conversations (3 kept, 2 excluded) → stored 3, skipped 2, one ledger row | ✗ | — | only single-conversation fixtures |
| `TestHistoryThenLiveNoDuplicate` | ✓ | Yes | |
| `TestLateChunkAfterCompleteIsStored` | ✓ | Weak | `rf9a`, `rf9b` killed, `rf9c` survives; passes only because progress differs (H9) |
| `TestMappingsInChunkApplyToItsMessages` | ✗ | — | `rf8d` survives |
| `TestNonsensicalHistoryTimestampStoredAsGiven` | ✗ | — | `rf9d` survives |
| `TestEmptyOrPartialChunkDoesNotPanic` | ✓ | Yes | |
| `TestCoverageReportsOldestPerChat` (+ store ledger tests) | ✗ | — | |

### Task 7: media queue

| Planned test | Exists? | Fails if broken? | Notes |
| --- | --- | --- | --- |
| document → file at layout path, `done`, sha256 | partial `TestDocumentDownloads` | Weak | asserts state only; `x7` survives; uses `time.Sleep` |
| sticker → skipped | rule level `TestDecideSkips…` | Weak | passes via Download list (`san` survived; `san2` killed) |
| 150 MB video → skipped, no download | rule level | **Yes** (`x9`) | |
| fails twice then succeeds → `attempts == 3` | ✗ | — | `rf3d` survives |
| always fails → `failed`, 5 attempts, backoff | ✗ | — | `rf3a` survives |
| expired → one re-upload; `MediaRetry` → `done` | ✗ | — | H3 |
| 30 expired → exactly 10 requests, rest refill | ✗ | — | H4; `rf3b`, `rf3c` survive |
| live enqueued after 100 history first | ✗ | — | `x4` survives |
| `Enqueue` returns immediately | ✗ | — | |
| `pending`/`downloading` rows recovered after restart | ✗ | — | `x5`, `x6` survive (logic verified by probe) |
| worker panic recovered, row `failed: panic` | ✗ | — | H11 |
| `goleak` | ✗ | — | |

### Task 8: backups

| Planned test | Exists? | Fails if broken? | Notes |
| --- | --- | --- | --- |
| snapshot opens, same `messages` count | partial | Weak | `TestSnapshotVerifiesAndNeverIncludesSession` checks integrity, not the count |
| snapshot while another goroutine inserts | ✗ | — | |
| `Verify` good passes / truncated fails | partial `TestVerifyRejectsTruncated` | Weak | `x12` survives (non-DB file) |
| `Keep: 2`, three runs → two folders | ✗ | — | `x13` survives; H16 |
| `TestSessionNeverBackedUp` | ✓ (merged) | Yes | |
| destination missing or unwritable → error | ✗ | — | |
| `Schedule` fires once at `Backup.Time` | ✗ | — | |

### Task 9: health and ntfy

| Planned test | Exists? | Fails if broken? | Notes |
| --- | --- | --- | --- |
| ntfy POST (method, path, title, bearer, body) | ✓ | Yes | path not asserted |
| `logged_out` → exactly one alert; repeats gated; `Status()` | partial | Weak | existing test uses `client_outdated`; `rf4a` survives; `rf4d` killed |
| `client_outdated` names the version | ✗ | — | `rf6b` survives |
| `temp_banned` includes expiry | ✗ | — | `rf4b`, `y6` survive |
| 6 h 1 min without `Seen` → alert, then "recovered" | ✗ | — | recovered not implemented (H10) |
| 10 days of only `Seen(t,false)` → phone-quiet | ✗ | — | rule not implemented |
| disk below threshold → alert | ✗ | — | `rf5c` survives |
| `WriteFailed` and `Panic` → one alert each per 6 h | ✗ | — | `rf5b`, `rf10d`, `rf10e` survive |
| failed `BackupResult` → alert naming error | ✗ | — | |
| `health.json` rewritten and parses back | ✗ | — | |
| `goleak` | ✗ | — | |

### Task 10: record and replay

| Planned test | Exists? | Notes |
| --- | --- | --- |
| round trip for each supported kind | partial (`Message` only) | recorder supports 1 of 9 kinds; replay loses fields (H14) |
| `Scrub`: none of the originals survive, consistent mapping | partial `TestScrubRemovesIdentifiers` | JSON with plain JIDs only; real shapes leak (H14); consistency untested |
| `TestReplayMatchesGolden` + `DumpRaw` + `testdata/` | ✗ | not implemented at all |
| synthetic scenario | ✗ | |

### Task 11: CLI, service, guide

| Planned test | Exists? | Notes |
| --- | --- | --- |
| `status`/`chats` against seeded folder, golden comparison | ✗ | H17 |
| `run` without session prints pair-first message | ✓ `TestRunWithoutSessionSaysPairFirst` (**killed `x10`**) | covers only a missing file (H18) |
| `TestVersionPrints` (extra) | ✓ | |

---

## 5. Mutation results (Review Focus 1 to 10)

Each mutation was applied alone in a fresh scratch copy, with `go test -count=1 ./...`. 61 valid mutants: **19 killed, 42 survived** (1 of those, `rf1a`, is an equivalent mutant). Invalid (non-compiling) mutants were reformulated and re-run; they are not counted.

| RF | Mutation | Result |
| --- | --- | --- |
| 1 same message twice | `rf1a` drop `ON CONFLICT DO NOTHING` on `messages` | survived (redundant behind the `EXISTS` check) |
| | `rf1b` remove the alias check in `InsertMessage` | **killed** `TestInsertMessageUnderKnownAliasIsDuplicate` |
| | `rf1c` drop `ON CONFLICT` on `message_events` | **survived** (no test inserts an event twice; in production this spins, H2) |
| | `rf1d` drop `ON CONFLICT` on `id_mappings` | survived (test ignores the error) |
| 2 edit/delete/reaction for unknown message | `rf2a` edit detection off | **survived** |
| | `rf2b` revoke event dropped | **survived** |
| | `rf2c` reaction event dropped | **survived** |
| | `rf2d` store refuses events whose target is missing | **killed** by 3 store/ingest tests |
| 3 media download fails | `rf3a` failed download marked `done` | **survived** |
| | `rf3b` expired media never re-requested | **survived** |
| | `rf3c` rate limiter removed | **survived** |
| | `rf3d` attempts not counted | **survived** |
| 4 unlink / restriction | `rf4a` `logged_out` raises no alert | **survived** |
| | `rf4b` `temp_banned` raises no alert | **survived** |
| | `rf4c` `LoggedOut` event mapped to `disconnected` | **survived** |
| | `rf4d` alert gate disabled | **killed** `TestImmediateStateAlertsAreGated` |
| 5 write failure / disk | `rf5a` `Ingest` returns early on write error | **survived** |
| | `rf5b` `WriteFailed` never reported | **survived** |
| | `rf5c` disk-space alert off | **survived** |
| 6 client version | `rf6a` `client_outdated` handled like `disconnected` | **survived** |
| | `rf6b` outdated alert omits the version | **survived** |
| | `rf6c` version override ignored | **killed** `TestDecideVersion` |
| 7 undecryptable | `rf7a` undecryptable events not stored | **killed** `TestUndecryptableThenResentClosesGap` |
| | `rf7b` gap never closes | **killed** (2 tests) |
| 8 LID / phone | `rf8a` filter ignores aliases | **killed** `TestScopeExcludesDirectAndAlias` |
| | `rf8b` mappings stored after the filter | **survived** |
| | `rf8c` no mapping from alternate JIDs | **killed** `TestAlternateJIDCreatesMapping` |
| | `rf8d` chunk mapping list not stored | **survived** |
| | `rf8e` `messages_resolved` stops collapsing | **killed** `TestAliasLearnedLaterCollapsesInView` |
| 9 history chunks | `rf9a` skip the ledger insert | **killed** `TestLateChunkAfterCompleteIsStored` |
| | `rf9b` chunks below 100% not ledgered | **killed** |
| | `rf9c` late chunk's messages dropped | **survived** |
| | `rf9d` history timestamps clamped | **survived** |
| 10 panics | `rf10a` handler guard removed | **survived** |
| | `rf10b` `guard.Run` stops recovering | **killed** `TestGuardRecoversPanic` |
| | `rf10c` media-worker guard removed | **survived** |
| | `rf10d` panic not counted | **survived** |
| | `rf10e` panic not alerted | **survived** |
| Hard constraints and wiring | `x1` filter bypassed in `Ingest` | **killed** `TestHandleDropsExcludedChat` |
| | `x2` four write connections instead of one | **survived** |
| | `x3` timestamp replaced by received time | **killed** `TestPlainTextNormalizes` |
| | `x4` live media no longer first | **survived** |
| | `x5` claim does not mark `downloading` | **survived** |
| | `x6` restart does not reset `downloading` rows | **survived** |
| | `x7` sha256 not recorded | **survived** |
| | `x8` `INSERT OR REPLACE` into the raw `message_events` table (CON-2) | **survived** |
| | `x9` video cap off | **killed** `TestDecideSkipsStickerAndLargeVideo` |
| | `x10` "pair first" message removed | **killed** |
| | `x11` unknown config keys accepted | **killed** `TestLoadRejectsUnknownKey` |
| | `x12` backup `Verify` no-op | **survived** |
| | `x13` backup retention off | **survived** |
| | `y1` `Seen` not called | **killed** `TestHandleStoresKeptMessage` |
| | `y2` `FromMe` always false | **survived** |
| | `y3` chat events not routed | **survived** |
| | `y4` `MediaRetry` not routed | **survived** |
| | `y5` `HistorySync` not routed through `Handle` | **survived** |
| | `y6` ban expiry dropped | **survived** |
| | `y7` voice-note detection | **survived** (the branch is dead code) |
| | `san` ignore the Skip list | survived (passes via the Download list); `san2` skip → pending: **killed** |
| | `z1` empty the archtest allow-list | **survived** (vacuous test) |

---

## 6. Deferrals in `PROGRESS.md`

| Deferral (as Codex wrote it) | Verdict |
| --- | --- |
| Recovered alerts / "periodic recovered-alert coverage" (Task 9) | **Should be a unit test now.** The fake clock and the notifier fake already exist. The rules themselves (disconnected, phone-quiet, recovered) are not implemented (H10). |
| Panic-row failure marking "needs the Task 12 corpus" (Task 7) | **Should be a unit test now.** A fake downloader that panics reproduces it in 20 lines (`TestProbeWorkerPanic`). The behaviour is also unimplemented (H11). |
| Replay coverage beyond message envelopes (Task 10) | **Mostly a unit test now.** Go-constructed events for every kind round-trip fine offline, and the plan asked for a synthetic golden scenario in Task 10. Only the *recorded, scrubbed real corpus* is legitimately live-only. |
| Golden files for replay | **Should exist now** for the synthetic scenario (`DumpRaw` is missing). The recorded-corpus goldens are legitimately live-only. |
| Golden files for `status` / `chats` ("no separate golden file was needed") | **Should be a unit test now.** The plan's Step 1 requires it, and a seeded temp folder needs no phone. |
| Limiter is wall-clock | **Should be fixed now.** `AllowN(clock.Now(), 1)` allows the fake clock the plan specified. |
| `Run`, pairing, reconnect ("verified live in Task 12") | The *socket* work is legitimately live-only. The *policy* (when to re-dial, wait, pause) is pure logic and **should be a unit test now** (B2). |
| Task 12 itself: pairing, receipt audit, version drill against the real handshake, unlink, LID dedup on real data, history reach, 7-day audit | **Legitimately live-only.** |

---

## 7. Fix plan

Order matters: fix and test B1/B2 first, then the data-fidelity group, then the rest. Each item starts with a failing test, per the plan's TDD rule; a different agent or session should write the test (red) before the fix (green). The probe files in the evidence folder give a head start for several of them.

### Before Task 12

1. **B1 never drop an event.** Failing tests: `TestHandleWriteFailureRetriesUntilStored`, `TestMappingWriteFailureIsRetried`, `TestEnqueueFailureIsRetried`, `TestImportHistoryRetriesLedgerWrite`. Includes making `Handle` stop discarding errors.
2. **H2 retry back-off.** Failing tests: `TestWriteRetryBacksOff`, `TestDuplicateEventIsNotARetryableError`; also add the second duplicate-event assertion to the store tests (kills `rf1c`).
3. **B2 reconnect policy.** Failing tests: `TestClientOutdatedSameVersionWaits30Minutes`, `TestOutdatedAfterConnectedTriggersRefresh`, `TestTemporaryBanPausesCapture`, `TestLoggedOutStopsTheReconnectLoop`, `TestStreamReplacedBacksOff`. Then wire the whatsmeow logger to `slog` (M3) so the drill is observable. **Do not run Task 12 step 5 before this.**
4. **H18 unpaired start.** Failing test: `TestRunWithUnpairedSessionFileSaysPairFirst`; add `RestartPreventExitStatus` in the unit; add the "Paired" message on `PairSuccess` (M13).
5. **H1 store speed.** Failing test: `TestInsertMessageCostDoesNotGrowWithTableSize`.
6. **H5 + H6 history fidelity.** Failing tests: `TestHistoryEphemeralAndDocumentCaptionAreUnwrapped`, `TestHistoryFromMeMessageHasOwnJIDAsSender`, `TestHistoryPushNameIsStored`, `TestLiveMessageKeepsKnownChatName`, `TestExcludedChatNameNotStoredFromHistory`, `TestHistoryEditIsStillAnEvent`.
7. **H7 + H8 chat events and protocol messages.** Failing tests: `TestChatEventsStored`, `TestSameGroupInfoTwiceIsOneRow`, `TestArchiveOnAndOffAreDistinct`, `TestProtocolEditOldAndNewFormats`, `TestNonRevokeProtocolMessageIsNotARevoke`, `TestRevokeNormalizes`, plus reaction/pin/poll-vote normaliser tests (Table 1 `TestPollVoteCurrentAndNewVersions`).
8. **H9 ledger identity.** Failing test: `TestTwoDistinctChunksWithSameProgressAreBothLedgered`; add `TestMappingsInChunkApplyToItsMessages`, `TestNonsensicalHistoryTimestampStoredAsGiven`, `TestCoverageReportsOldestPerChat`.
9. **H3 + H4 + H11 media.** Failing tests: `TestRequestMediaRetryReceivesMessageInfoAndKey`, `TestExpiredRequestsAreRateLimited` (10 now, 20 after the fake clock advances), `TestExpiredMediaRetryCompletes`, `TestWorkerPanicMarksRowFailedAndIsCounted`, `TestTransientFailureIsRetriedAfterBackoff`, `TestLiveMediaBeforeHistoryBacklog`, `TestPendingAndDownloadingRowsRecoverAfterRestart`, `TestAttemptsCountedAcrossRetries`.
10. **H10 health rules.** Failing tests: the planned Task 9 list (section 4), including `TestDisconnectedLongerThanThresholdAlerts`, `TestPhoneQuietAlert`, `TestRecoveredMessageAfterEventsResume`, `TestOutdatedAlertNamesVersion`, `TestTempBanAlertIncludesExpiry`, `TestHealthJSONRoundTrip`. Move `Notify` off the handler path (M2).
11. **H12 CON-1 guard.** Failing tests: `TestScanFlagsEveryBannedPrefixMethod`, `TestAllowListedFileStillRejectsSendMessage`; redo the canary with `SetStatusMessage`.
12. **H15 + H16 backup.** Failing tests: `TestRetentionOnlyRemovesSnapshotFolders`, `TestKeepTwoLeavesTwoSnapshots`, `TestFailedSnapshotLeavesNoFolder`, `TestSnapshotRowCountMatchesSource`, `TestSnapshotDuringConcurrentInserts`, `TestScheduleFiresOnceAtConfiguredTime`; fix the service `ReadWritePaths` and re-check it on the Linux server.
13. **H14 record / replay / scrub, H17 CLI.** Failing tests: `TestScrubRemovesNumbersInsideProtobuf`, `TestScrubDeviceSuffixedJID`, `TestRecordLoadRoundTripForEachKind`, `TestReplayMatchesGolden` (add `DumpRaw` and a synthetic scenario), `TestStatusGolden`, `TestChatsGolden`, `TestStatusDoesNotWriteArchive`; add the missing `scrub` entry point and the status fields. A golden is created once with `-update` after a line-by-line review recorded in `PROGRESS.md`.
14. **M1 tooling.** Make `make check` work on a Mac with GNU Make 3.81 and on a machine with no Go; arm64 builds. (Needed so Hamza can run the gate himself.)
15. **Missing Review Focus tests not named above.** `TestHandleRecoversPanic`, `TestMappingFromAltJIDStoredBeforeFilter`, `TestSameMessageUnderLIDAndPhoneStoredOnce`, `TestFromMeUpdatesPhoneActivity`, `TestUnknownEventIsIgnored`, `TestNoUpdateOrDeleteOnRawTables`, `TestOnlyOneWriteConnection`, `TestMediaMetadataRecorded` (sha256 and path), `TestHistoryMessagesOfLateChunkAreStored`, `TestExcludeLIDMatchesPhoneForm` (reverse direction). Re-run the mutations in section 5; the target is that every row in RF 1 to 10 is killed.
16. **M5 goroutines.** Add `goleak` to `ingest`, `media`, `health`; join goroutines in `serve`.
17. **Docs minimum for the live run** (M8): copy the service file, config ownership, expected outputs, ntfy test step, the SQL for step 8, spike labels and expected results in the acceptance log.

### Can wait

- M4 streaming downloads to file and fixed extension table; M6 commit `go mod tidy`; M7 strengthen the weak tests named; M9 `version` from build info; M10 exclude-JID validation and inert keys; M11 migration runner; M12 licence headers vs Table 3; M13(c) platform type experiment; L1 dead code; L2 counters and `health.json` write rate; L3 commit hygiene; L4 verify CI action versions; L5 correct `PROGRESS.md`.

---

## 8. What I could not verify

- Everything in `wa.Run`, pairing and reconnect was verified by reading our code against whatsmeow's source at the pinned version, not by running it (no WhatsApp connection by design). B2 is therefore an analysis, with the source lines cited; it should be confirmed with the unit tests in the fix plan.
- Performance figures are from this Mac (arm64, SSD); the Linux server will differ in speed, not in shape.
- `ProtectSystem=strict` behaviour (H15) is from systemd's documented semantics, not from running systemd.
- Whether `PlatformType=UNKNOWN` affects iOS history depth.
- CI action versions (L4).
- Whether whatsmeow's `SendMediaRetryReceipt` rejects an empty `MessageInfo` or sends a malformed request (H3). Either way the call cannot work.
- `SendHistorySyncServerErrorReceipt` (plan Task 0 asked whether it is needed for expired history blobs): not evaluated.
- I did not follow `phase0-setup.md` on a real Ubuntu host; M8 is from reading.
