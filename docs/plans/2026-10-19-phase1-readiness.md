# Phase 1 readiness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Clear the "Open before phase 1" code work in `PROGRESS.md`: a fast `archiver status`, a documented history-label finding, a read-only backup source, goroutine-leak checks, the exported-`any` guard gap, and a `PROGRESS.md` under 500 lines.

**Architecture:** Five independent changes. Tasks 1 and 3 touch disjoint files (`cmd/archiver/status.go`, `internal/store` versus `internal/backup`) and run in parallel in separate worktrees. Task 2 is a characterization test plus a report, no semantic change. Task 4 touches test files and `go.mod`, and runs after 1 and 3. Task 5 moves tables out of `PROGRESS.md` and runs last, before the status entries are written.

**Tech Stack:** Go 1.26 (pinned via `.tools/go`), `modernc.org/sqlite`, `go.uber.org/goleak` v1.3.0, `golangci-lint` v2.14.0, GNU Make 3.81 (macOS).

**Spec:** `PROGRESS.md`, section "Open before phase 1 (recorded 19 Oct 2026)", and the brief in the session that started this plan. `AGENTS.md` is the workflow contract.

## Global Constraints

- Build and test only through `make check` (and `make test` / `make lint` while iterating). Go is `.tools/go/bin/go` via the Makefile.
- Never run `archiver pair` or `archiver run`. Never read message text from any database. `status`, `chats`, `backup` and aggregate counts are fine.
- Never connect to WhatsApp from tests or agents. Nothing may send anything to WhatsApp or to a contact.
- No changes to the live server (`ryzen.local`, `archiver.service`, `/opt/whatsapp-archiver`). Ask first.
- No commits, no push, no merge into `main`. The user said commit only when asked. Changes stay in the working tree (or the task's worktree) for review.
- `AGENTS.md` red → green → refactor: a failing test is written and shown failing before the fix. Never delete, weaken or skip a test. Do not edit tests during green unless the test itself is the task.
- Files you touch stay under 500 lines. Do not expand `PROGRESS.md` except for short status entries.
- No `Co-Authored-By` trailers anywhere (the repo's CLAUDE.md forbids them).
- Use the `.claude/green.lock` lock during green as `AGENTS.md` says; delete it after `make check`.
- Golden files: no `-update` without reviewing every line and logging the review in `PROGRESS.md`.

## Review Focus

- `status` on an archive with no messages, no media and no `health.json`: must print the same empty-state lines as before.
- `status` while the service holds the archive open (`-wal` present): reader must still see committed rows.
- `backup` against an archive in WAL mode with no `-shm` present: snapshot must still verify.
- History label with zero chunks, with chunks but no history messages, and with progress exactly 100 on the last of several chunks received out of order.
- goleak on `internal/wa` tests that start the reconnect loop or the pairing printer: any leaked goroutine must be fixed at its owner, not ignored.

---

### Task 1: `archiver status` cost independent of archive size

**Files:**
- Modify: `internal/store/views.go` (the `ChatStats` query and `mediaCount` helper)
- Modify: `internal/store/store.go` (`HistoryCoverage` query, only if Task 2's finding changes it; Task 2 is not to change semantics, so leave it)
- Modify: `cmd/archiver/status.go` only if the output path needs it (it should not)
- Test: `internal/store/status_cost_test.go` (create; in-package like `insert_cost_test.go`)
- Test: `cmd/archiver/status_test.go` (existing golden tests must stay green, unchanged)

**Interfaces:**
- Consumes: `store.OpenReadOnly(path) (*Store, error)`, `(*Store).ChatStats(ctx) ([]ChatStat, error)`, `(*Store).HistoryCoverage(ctx) (Coverage, error)`.
- Produces: unchanged public signatures. `ChatStats` returns the same `[]ChatStat` for the same archive, in the same order.

- [ ] **Step 1: Measure the baseline (no code change).** Build the binary with `make build`. Build a synthetic archive with `.cache/build/archiver replay internal/ingest/testdata/events/synthetic` against a scratch data dir (not `data/` in the repo root, not the live one). Scale it: write a small generator under `.cache/` that inserts ~200k messages and ~30k media rows into the scratch `archive.db` through `store` (not raw SQL that bypasses `Pipeline`, unless noted). Record `time` and `ps -o cputime` for `archiver status` with `-config` pointing at the scratch data dir. Save the numbers in the task report.
- [ ] **Step 2: Capture the query plan.** With `sqlite3` or a test helper, run `EXPLAIN QUERY PLAN` on the `ChatStats` and `HistoryCoverage` SQL. Expect `SCAN messages`, `USE TEMP B-TREE` for the window function, and a correlated subquery over `media` per chat.
- [ ] **Step 3: Write the failing test.** `internal/store/status_cost_test.go`, `TestChatStatsDoesNotRescanPerChat`: seed 20,000 messages across 200 chats and 5,000 media rows through `Store` write methods; assert that the time for `ChatStats` on the full archive is under a bound that the current code misses (measure it first; choose the bound at about 4x the time of a 1/10-size archive, as `insert_cost_test.go` does with the ratio). Also assert via `EXPLAIN QUERY PLAN` that the plan has no `CORRELATED SCALAR SUBQUERY` per chat row over `media` and no `SCAN media`.
- [ ] **Step 4: Run to verify it fails.** `make test` with `-run TestChatStatsDoesNotRescanPerChat`. Expected: FAIL, showing the plan or the ratio.
- [ ] **Step 5: Implement the fix.** Likely shape: compute the alias map once (`chat_aliases` restricted to the chats present), join media and gaps to it by `GROUP BY canonical_jid` in one pass instead of four correlated subqueries; avoid recomputing `messages_resolved`'s window over the whole table for each aggregate. Keep the SQL in `views.go`; keep `ChatStat` fields and the output format unchanged.
- [ ] **Step 6: Run the test to verify it passes, then the goldens.** `make test`. `TestChatStatsDoesNotRescanPerChat` passes; `cmd/archiver` golden tests pass without `-update`.
- [ ] **Step 7: Measure after.** Re-run Step 1's commands on the same scratch archive. Report before and after CPU and wall time, and confirm the `status` output is byte-identical (`diff` the two outputs; they must match, except for the `data folder:` size line if it changed).
- [ ] **Step 8: Refactor and verify.** Tidy names, keep the file under 500 lines, `make check`. Delete `.claude/green.lock`.

### Task 2: history "complete" label investigation (no semantic change)

**Files:**
- Test: `internal/store/history_label_test.go` (create)
- No production change.

**Interfaces:**
- Consumes: `store.Open`, `(*Store).InsertHistoryChunk(ctx, HistoryChunk) error`, `(*Store).HistoryCoverage(ctx) (Coverage, error)`, `cmd/archiver/status.go` `historyLine(loc, store.Coverage) string`.
- Produces: none. The report to the user is the output.

- [ ] **Step 1: Read the derivation.** `store.HistoryCoverage` sets `Complete` to `COALESCE(MAX(progress)>=100,0)` over all of `history_sync_chunks`. `historyLine` prints "complete back to" with the minimum over `OldestByChat`, which is the oldest message across every chat, including chats whose history is not finished.
- [ ] **Step 2: Write the characterization test.** `TestHistoryCompleteRuleUsesAnyChunkAtHundred`: insert chunks with progress 10, 40 and 57 (no 100), then one chunk at 100 out of order (received first). Assert `Complete` equals what the current rule gives (true, because one chunk is at 100). Add a second case: only progress 10, 40, 57 → `Complete` false, and `historyLine` prints "in progress (57%)". Name the test's comment: this pins the current rule so the decision can be made without the test going red unexpectedly; flip it when the user decides.
- [ ] **Step 3: Run it.** `make test -run HistoryComplete`. Both pass (characterization). Report that it passes because it pins the current rule.
- [ ] **Step 4: Report, do not change.** In the report include: the rule, the line of code, the observed live counts (2 of 57 chunks at 100, from the PROGRESS note), and a recommendation for the correct label. Recommendation to present: complete only when the chunk with the highest `chunk_order` for the latest history sync reports progress 100, and "back to" as the oldest message of the chats whose history is complete, not the global minimum. The user decides.

### Task 3: backup opens `archive.db` read-only

**Files:**
- Modify: `internal/backup/backup.go` (around line 46, the `sql.Open` for `VACUUM INTO`)
- Test: `internal/backup/backup_test.go` (add a test)

**Interfaces:**
- Consumes: `backup.Run(ctx, cfg *config.Config) (Report, error)`, `backup.Verify(path string) error` (unchanged).
- Produces: unexported `openSource(path string) (*sql.DB, error)` returning a connection whose DSN carries `mode=ro`.

- [ ] **Step 1: Write the failing test.** `TestSourceConnectionIsReadOnly`: open the source via `openSource(path)` on an archive created by `store.Open`; `db.Exec("CREATE TABLE x(y)")` must fail with an error containing `readonly`. Also keep the existing `TestRun...` tests unchanged.
- [ ] **Step 2: Run to verify it fails.** Expected: FAIL, the write succeeds (or `openSource` is undefined; if undefined, show it fails to compile and also demonstrate the write succeeds against the current inline `sql.Open` in a scratch test).
- [ ] **Step 3: Implement.** Add `openSource`, DSN `file:<path>?mode=ro&_pragma=busy_timeout(5000)`, used by `Run`. Do not add `immutable=1`: the service may be writing (WAL). Check that `VACUUM INTO` succeeds on the read-only handle; if it does not, report it and stop (do not weaken the test).
- [ ] **Step 4: Verify.** `make test`; the snapshot still verifies (`TestRun...` passes); a WAL archive with `-wal` present still snapshots.

### Task 4: small gaps (after Tasks 1 and 3 land)

**Files:**
- Modify: `go.mod`, `go.sum` (`go mod tidy`, diff first, keep the pinned whatsmeow version)
- Create: `internal/wa/main_test.go`, `internal/backup/main_test.go`, `internal/store/main_test.go` (each `TestMain` calling `goleak.VerifyTestMain(m)`, same as `internal/health/main_test.go`)
- Modify: `internal/archtest/scan.go` and `internal/archtest/archtest_test.go` for the exported-`any` gap (the CON-1 static guard lives here, not in `internal/guard`, which is the panic guard)

**Interfaces:**
- Consumes: `archtest.Scan`-style fixture helpers in `archtest_test.go` (the existing `fixture`/`violations` helpers around lines 250–290).
- Produces: none.

- [ ] **Step 1: `go mod tidy`.** Run `make` target equivalent: `.tools/go/bin/go mod tidy -diff` (Go 1.26 supports `-diff`) and show the diff. Apply only if it removes unused entries and changes no version. Do not bump whatsmeow (AGENTS: use the `bump-whatsmeow` procedure for that).
- [ ] **Step 2: goleak.** Add the three `TestMain` files. Run `make test`. If a goroutine leaks, the test fails: fix it at the owner (context cancellation), do not add `goleak.IgnoreTopFunction` unless a third-party goroutine is documented; report each.
- [ ] **Step 3: Exported-`any` red test.** In `archtest_test.go`, add fixtures for an exported func, field and var whose type is `any` (or `interface{}`) and whose value is or holds a `*whatsmeow.Client` (e.g. `func Raw() any { return client }`, `var C any = client`, `type S struct{ C any }` with an assignment in an exported func). Expected today: not flagged. Run; show FAIL.
- [ ] **Step 4: Implement** the check in `scan.go` next to the existing `client-escape` rule: an exported result, field or var whose declared type is `any`/empty interface is flagged `client-escape` when the file imports whatsmeow. Keep the rule narrow so existing passing fixtures stay green. Run `TestRepoHasNoViolations`.
- [ ] **Step 5: Verify.** `make check`.

### Task 5: split `PROGRESS.md` (runs last)

**Files:**
- Modify: `PROGRESS.md` (currently 621 lines; target under 500 lines)
- Create: `docs/reviews/2026-10-19-progress-tables.md` (moved tables and gap lists, verbatim)

**Interfaces:**
- Consumes: none.
- Produces: a pointer line in `PROGRESS.md` to the new file, which the coordinator uses in the status entries.

- [ ] **Step 1: Choose the material.** Move the per-task "Mutations killed" and "Probes" bullets and the "Coverage" bullets from the completed sections, plus the gap and mutation tables, into the new file, grouped under the same headings. Keep the first line of each moved section (what was done, commit hashes) in `PROGRESS.md`.
- [ ] **Step 2: Check nothing is lost.** Compare the set of commit hashes, mutation names (`h1-view`, `rf5a`, …) and probe names before and after: `grep -o` both files and the new file, and confirm the union equals the before set. Report the count.
- [ ] **Step 3: Verify the limit.** `wc -l PROGRESS.md` under 500. Every file under 500 lines.

### Task 6: finish and record

**Files:**
- Modify: `PROGRESS.md` (short status entries only, one per task above, plus the "Open before phase 1" checkboxes)

- [ ] **Step 1: Run `make check`** on the combined working tree, from a clean `.claude/green.lock` state.
- [ ] **Step 2: Record.** One short entry per task: what changed, test names, `make check` result, measured numbers for Task 1, the open decisions for Task 2. Tick the matching lines under "Open before phase 1".
- [ ] **Step 3: Report to the user:** the summary, the server-only steps left, and the four decisions with recommendations. No commits.
