# Phase 1 readiness report, 2026-10-20

Re-run on 2026-10-19 at about 22:10 Asia/Riyadh, from `docs/prompts/2026-10-20-phase1-readiness-check.md`, after the Mac moved to the same network as ryzen. The server was reached at `192.168.0.104` because `ryzen.local` no longer resolves from the Mac. Server access was read-only, and every temporary file the run created on the server was removed. Earlier results in this report superseded the first run's BLOCKED entries for checks 3 to 9.

## Check results

| # | Check | Result | Evidence |
| --- | --- | --- | --- |
| 1 | Local repo | **FAIL** | `git status --short` lists two untracked files: `?? docs/prompts/2026-10-20-phase1-readiness-check.md` and `?? docs/reviews/2026-10-20-phase1-readiness-report.md`. No tracked file is modified. `main`, `fix/recovered-after-restart` and `origin/main` all at `1d725dd`. `wc -l PROGRESS.md` = 487. |
| 2 | Local gate (`make check`) | **PASS** | Exit 0 on the current tree. All 11 packages `ok`; golangci-lint `0 issues.`; builds for linux/amd64, linux/arm64, darwin/arm64, darwin/amd64, windows/amd64. Log: `.cache/readiness-check.log`. |
| 3 | Server service, hash, health | **PASS** | `systemctl is-active archiver` = `active`. `sha256sum` = `adc871fe…2831` (expected). `health.json`: `"state": "connected"`, `"panics": 0`, version `2.3000.1050780110` (latest). |
| 4 | Log since restart, no ERROR/WARN | **FAIL** | 44 lines since `01:29:40`; 38 are ERROR or WARN. Breakdown in "Problem 5" below. Two classes are real defects: SQLITE_BUSY decrypt failures (Problem 2) and undecryptable gaps (Problem 3). |
| 5 | Traffic (messages > 227884) | **PASS** | `SELECT COUNT(*) FROM messages` = 228085 at 22:02 local. |
| 6 | Secrets, four tables | **PASS** | Counter run on a `.backup` copy of the live archive: `messages` 228087 rows, `message_events` 3802, `media` 32465, `chat_events` 91; `with_message_secret=0` in all four. The copy and the tool were removed. |
| 7 | Nightly snapshot | **PASS** | Newest `20261019T003000…` (03:30 local). `PRAGMA integrity_check` = `ok`. Counter on it: 0 secrets in all four tables. Rows: messages 227887. |
| 8 | History status line | **PASS (recorded)** | `history: complete back to 2020-08-28, chunks 57, last chunk 2026-10-16 19:12 +03`. The check only requires the line to be quoted. The "complete" rule is an open decision (Problem 8). |
| 9 | Rollbacks present | **PASS** | `archive.db.pre-scrub` (161890304 bytes), `archive.db-wal.pre-scrub`, `archive.db-shm.pre-scrub` in `/var/lib/whatsapp-archiver/`; `archiver.before-19oct` (root-owned) in `/opt/whatsapp-archiver/`. |
| 10 | Phase 0 acceptance: steps still needing Hamza | **FAIL** (list not empty) | Listed below. Steps that need your phone are SKIPPED because you said manual WhatsApp steps may be skipped. |

Read-only server state at check time: `open_gaps` = 10, `message_events` kind `undecryptable` = 31, `history_sync_chunks` = 57, `busy_timeout` on the CLI connection = 0 (the service sets its own; see Problem 2).

## Verdict

**NOT READY**

The rule is: READY only if checks 1 to 9 all PASS and the step list in check 10 is empty. Two checks fail, and one step list is open:

1. Check 1: two untracked documents. Needs your decision (commit them, or move them out). Not done here.
2. Check 4: 38 WARN/ERROR lines. Two classes are real (Problems 2 and 3). The network class (Problem 5) needs your rule on whether it is an allowed class.
3. Check 10: manual phone steps are open. Skipped at your instruction, so the list is not empty.

A readiness sign-off is not possible while Problems 2 and 3 are open: they mean messages can be permanently missed.

## Problems, mini plans

Each problem below came from a read-only analyst. I checked the cited code before listing it. Nothing has been implemented yet; each plan starts with a failing test.

### Problem 1: untracked readiness files (check 1)

- Cause: the scheduled prompt and this report are not committed.
- Plan: you choose one. Commit both as `docs: phase 1 readiness check` (I will do this only on your say-so), or move them outside the repo.
- Cost if wrong: none to the code. A commit is easy to amend.

### Problem 2: SQLITE_BUSY on decrypt (check 4)

- **Status: FIXED in the working tree, not committed.** `_txlock=immediate` added to `sessionDSN` (`internal/wa/session.go`); `docs/prior-art.md` updated; `TestSessionDSNReadThenWriteTxnsWaitInsteadOfBusy` was red (`database is locked (5)`) and is green. A `SetMaxOpenConns` call in the test was removed because the architecture guard bans it in `internal/wa`.
- Evidence: `internal/wa/session.go:6` builds the session DSN with `busy_timeout(5000)` and no `_txlock`. `internal/wa/wa.go:76` sets `EnableDecryptedEventBuffer = true`, so each decrypt runs in whatsmeow's deferred read-then-write transaction. When another pooled connection holds the write lock (most likely the per-message push-name update), SQLite returns SQLITE_BUSY at once, and `busy_timeout` does not apply. Ten or more messages failed this way on 19 Oct.
- Plan:
  1. Add `&_txlock=immediate` to `sessionDSN`.
  2. Red test first: `TestSessionDSNReadThenWriteTxnsWaitInsteadOfBusy` in `internal/wa/wa_test.go`. Two transactions that each read, meet at a barrier, then write. It fails today with `database is locked (5)`.
  3. Update the DSN in `docs/prior-art.md:51`.
- Rejected: `SetMaxOpenConns(1)` (can hang), WAL alone (does not fix read-then-write upgrade).
- Cost if wrong: writes to `session.db` serialise. A write lock held over 5 s now fails at BEGIN, which is the same end result as today.

### Problem 3: open decryption gaps (check 4, Phase 0 Q10)

- Evidence: 10 open gaps, 31 `undecryptable` events. A gap closes only when a `messages` row with the same ID appears in the same chat. Existing recovery (retry receipts and one automatic placeholder request at first failure) has already run for every open gap, and nothing re-requests old gaps.
- Limits found:
  - Reactions, edits, revokes, pins and poll votes are sent with `decrypt-fail=hide`. They become events, not `messages` rows, so their gaps can never show as closed.
  - A DM recovered under the other JID form, with no `id_mappings` row, stays open.
- Plan:
  1. Owner runs read-only evidence queries on the server (counts and types, never text) and a journal grep to classify each of the 10 gaps.
  2. Explain the unrecoverable ones in the Q10 document, which `docs/phase0-acceptance.md:298` allows.
  3. Only if the `hide` limit matters: add a `source_id` column and change the view, test first.
  4. Only if you accept a CON-1 exemption (AGENTS.md forbids sending message-type requests outside `internal/wa/rerequest.go`): add a re-request sweep. Trial one gap under supervision first, because it is unknown whether the phone answers for old messages.
- Also open: why open gaps rose from 1 (18 to 19 Oct) to 10. This needs per-hour counts against the journal's restart and disconnect times.
- Cost if wrong: an unexplained gap passes readiness; a sweep that sends the wrong message type breaks CON-1.

### Problem 4: history import loss on shutdown (open item 2)

- Evidence (code reading, not reproduced): whatsmeow ignores the handler's `ack=false` for `HistorySync` (`message.go:735`). The notification stanza is acked within milliseconds, before the blob is downloaded (`message.go:489-497`). `ImportHistory` writes the ledger row first (`history.go:65`), then one transaction per row. A shutdown mid-chunk leaves a ledger row that claims more messages than were stored.
- Plan:
  1. Set `ManualHistorySyncDownload=true`. Download the blob in `eventHandler` (`wa.go:141`), run the import, and forward and ack the notification only if the import succeeded.
  2. One transaction per chunk, with the ledger row written last. Mappings commit first, because the exclusion filter reads through the read pool.
  3. Red tests first: `TestHistoryNotificationIsNotAckedUntilItsChunkIsStored`, `TestOpenDownloadsHistoryManually`, `TestInterruptedHistoryImportLeavesNoLedgerRow`. No-loss proof: `TestNoHistoryLossAcrossInjectedShutdown`, which reopens the store and delivers the chunk three times.
  4. Still needs a real-server check: whether WhatsApp re-delivers an un-acked notification after a restart, and whether the blob is still downloadable then.
- Decisions for you: whether to `DeleteMedia` after commit; how to handle a 404 or 410 on download; whether to use a per-row SAVEPOINT. Existing ledgered chunks that were only partly imported also need a rule.
- Cost if wrong: duplicate deliveries, or a stuck import. The change adds no UPDATE, DELETE or REPLACE, so CON-2 holds.

### Problem 5: network WARN/ERROR lines (check 4)

- Evidence: all four classes are logged by whatsmeow, passed through `internal/wa/walog.go:19-35` at the same level. Websocket errors reconnect automatically. Keepalive timeouts reconnect after 3 minutes of misses. Our code does not react, and the "Disconnected for" alert needs more than 15 continuous minutes (`internal/health/health.go:189`). The `Set status notification (<nil>)` line is harmless (an empty About text, which we ignore).
- Plan:
  1. **Status: DONE in the working tree, not committed.** The status-notification line is demoted to Debug by a prefix table in `waLogger.logf` (`internal/wa/walog.go`). `TestBenignWhatsmeowWarnIsDebug` was red (`[WARN WARN]`) and is green.
  2. Add Info lines "connection lost" and "connection restored" in `Pipeline.Handle`, test first, so each drop is visible in the journal.
  3. Owner rule (not mine to decide): whether these WARN/ERROR lines are an allowed class. Proposed bound: at most 10 per 24 h, authenticated within 2 minutes after each drop, and no "Disconnected for" alert. Media download WARNs are allowed once no `media` row is in `failed`.
  4. Side finding: the media download WARN prints the signed CDN URL into the journal. Redact it.
- Cost if wrong: accepting too early hides chronic flapping. Keeping zero tolerance means the gate cannot pass on a home network.

### Problem 6: `archiver status` cost

- Evidence: the `chatStatsQuery` fix (commit `ae97290`) is already in `main`, `origin/main` and `fix/recovered-after-restart` (verified with `git branch --contains`). PROGRESS.md's "not merged" note is stale. `status` still reads `messages` through the `messages_resolved` window twice: `internal/store/store.go:358` and `internal/store/views.go:10-11`.
- Plan: a single `StatusReport` pass that also returns `OldestHistory`. Tests first: an `EXPLAIN QUERY PLAN` test asserting one non-covering scan of `messages`, and an oracle that `StatusReport` equals `HistoryCoverage` plus `ChatStats`, including a case where the first copy is live and a later alias copy is history (guards the "complete back to" line).
- Cost if wrong: the "complete back to" date claims more history than the archive has.

### Problem 7: manual WhatsApp tests

- Result: none in `cmd/` or `internal/` needs a manual WhatsApp action. Nothing was skipped; the only existing skip is the root-permissions guard at `internal/backup/backup_test.go:295`. `make test` passes with 347 tests passing, 0 skipped, 0 failed.

### Problem 8: history "complete" rule

- Current rule (`TestHistoryCompleteWhenAnyChunkReachesHundred`) marks history complete when any chunk reaches 100. The status line above reads "complete back to 2020-08-28" under that rule. The proposal is to use the latest sync's last chunk and a per-chat "back to". Decision open for you.

### Problem 9: environment notes

- `ryzen.local` does not resolve from the Mac on the new Wi-Fi. Use `192.168.0.104`. PROGRESS.md says there is no DHCP reservation possible, so the address can change.
- PROGRESS.md "Open before phase 1" item (1), message secrets not scrubbed, looks stale: the counter found 0 secrets in all four tables on the live archive and the snapshot.

### Check 10: phase 0 steps still open

- Step 1: iOS version, WhatsApp version, date/time and the ntfy result are blank. Needs your phone.
- Step 2: not run. Pairing with `--record`. Blocks step 10.
- Step 3: partly done. Untested: the 100 MB video skip, the sha256 check, "nothing on anyone's phone".
- Step 4: skipped. Receipt audit, gates phase 1.
- Steps 6, 7, 8, 9, 10, 12, 13: not run.
- Steps 5 and 11: recorded as done.

## Remaining decisions for Hamza

1. Problem 1: commit the two readiness documents, or move them.
2. Problem 3: whether to accept a CON-1 exemption for a re-request sweep.
3. Problem 4: `DeleteMedia` after commit; handling of 404 or 410; SAVEPOINT or not.
4. Problem 5: the allowed-class rule for network WARNs.
5. Problem 8: the history "complete" rule.
6. Check 10: whether the skipped manual steps count toward the readiness verdict.

## Not done by this run

- No code was changed. Plans only.
- No `archiver pair` or `run`, no service changes, no config changes, no deletes on the server, no commits, pushes, merges or stashes.
- Server writes were limited to a counter copy of `archive.db` and a counter binary in `/tmp`, plus a journal file in `/tmp` that I created and removed. All removed, verified with `sudo ls`.
- Files written locally: this report, `.cache/readiness-check.log`, and `.cache/build` binaries from `make check`.
- Analyst scratch files are in the session scratchpad, not in the repo.
