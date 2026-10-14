# Fix Phase 0 of WhatsApp Archiver (from the independent review)

You are taking over phase 0 of WhatsApp Archiver, a headless, receive-only WhatsApp logger in Go. Codex built it, and an independent review found it is **not ready for the live acceptance run (Task 12)**. Your job is to fix what the review found, test-first, in stages, stopping at each checkpoint for Hamza's go-ahead.

## 1. Read first

1. `docs/reviews/2026-10-14-phase0-review.md` (in the GitHub repo: `archiver/docs/reviews/…`) is **your work list**. Sections 3 (findings), 4 (planned tests vs what exists), 5 (mutations) and 7 (fix plan) matter most. Finding IDs (B1, H5, …) are used below.
2. `docs/reviews/2026-10-14-phase0-review-evidence/` holds probe tests (stored as `*.go.txt`; rename to `.go` in a scratch copy to run) and the mutation harness. Reuse them as the starting point for red tests and to prove each fix.
3. The standard you build to: `docs/superpowers/plans/2026-10-14-phase0-capture-logger.md` (Global Constraints, Engineering Principles, Review Focus 1 to 10, Tasks 0 to 12) and charter v1.1 (`WhatsApp Archiver — Project Charter & Build Plan.md`, sections 5, 7, 11, 13, 13b, 14).
4. `archiver/AGENTS.md`, `archiver/PROGRESS.md`, and `docs/prior-art.md` (look up every whatsmeow name there and in the pinned source under `.cache/gomod/go.mau.fi/whatsmeow@v0.0.0-20261013142727-4cf8bc8e0aeb/`; never guess an API).

Treat `PROGRESS.md` as a claim, not a fact: the review found several "complete" entries overstated it (finding L5).

## 2. Hard rules

- **Never connect to WhatsApp.** Do not run `archiver pair` or `archiver run`, do not create a session, do not display QR codes. Every test uses fakes, Go-constructed fixtures or the replay harness. Task 12 belongs to Hamza.
- **CON-1 is absolute:** nothing a contact can see is ever sent. The only send is `SendMediaRetryReceipt` in `internal/wa/rerequest.go`.
- **Install nothing globally.** Go 1.26+ and golangci-lint live in `.tools/`, caches in `.cache/`. On macOS the system Make 3.81 ignores the Makefile's `PATH` export, so until finding M1 is fixed run `PATH="$PWD/.tools/bin:$PWD/.tools/go/bin:$PATH" make check`. Check `file .tools/bin/golangci-lint` matches this machine.
- **Strict red, then green.** For every fix: write the failing test, run it, confirm it fails for the *expected behavioural reason* (not a compile typo), commit it as `test: …`; then create `.claude/green.lock`, implement the least code, run `make check`, delete the lock, commit as `feat:`/`fix:`. Implementation commits must not touch `_test.go` files (the pre-commit hook enforces this). Use separate subagents for red and green where it helps.
- **Never weaken, skip or delete a test to get green.** Golden files are created once with `-update` only after you read every line and record that in `PROGRESS.md`.
- **Prove each fix with the review's own mutation.** After a fix, re-apply the matching mutation from review section 5 in a scratch copy and confirm a test now fails ("killed"). The target is that every Review Focus row (RF 1 to 10) is killed.
- **Least code (YAGNI, DRY).** One ingest path (`Pipeline.Ingest`), one write connection, one retry helper, one panic guard, panics recovered only at the two boundaries. Wrap errors with context. No dead code.
- **No `Co-Authored-By` trailer** on commits. **Do not push** to GitHub without asking Hamza: the repo (Hamza-Xoho/whatsapp-archiver) is public.
- If something is ambiguous or blocked, stop and ask rather than working around it.

## 3. Stage 0: workspace tidy (do this first, ask before moving anything)

The project grew in pieces and is messy: `archiver/` is the git repo and the Go module, while the charter, plan and research live in a sibling `docs/` folder that is not in git; a review copy exists in two places; `.claude-flow/` and `.DS_Store` are stray. Propose, in a short plan, a single clean layout where the repo contains everything needed to continue (code, `AGENTS.md`, plan, charter, review, prompts), and `AGENTS.md` links resolve. Points to raise with Hamza before acting:

- whether the charter and research go into the **public** repo, or the repo goes private first (they may contain personal details);
- where the canonical copy of the review and these prompts lives (one copy, not two);
- `.gitignore` entries for `.claude-flow/` and `.DS_Store`.

Do not move or delete anything until Hamza approves the plan. Then fix tooling finding **M1** (Make 3.81 `PATH`, Go bootstrap, wrong-architecture lint binary accepted, arm64 builds, no error-hiding `2>/dev/null ||` fallbacks) so `make check` works on this Mac with no manual `PATH`.

## 4. Stages (checkpoint after each: report results, wait for go-ahead)

Follow review section 7. Within a stage, one finding per red/green cycle.

**Stage 1: blockers that can lose data or hurt the account**
- **B1** never drop an event: mapping insert, media enqueue and every `ImportHistory` write go through the retry-until-success path; `Handle` stops discarding errors.
- **H2** retry back-off (no hot spin; a duplicate-key error must not retry forever).
- **B2** reconnect policy: extract it into a small pure state machine and unit-test it with an injected sleep (30-minute wait when the version did not change, pause on `TemporaryBan`, stop looping after `LoggedOut`, back off on `StreamReplaced`, keep listening for `ClientOutdated` at all times). Wire whatsmeow's logger to `slog` (M3).
- **H18** unpaired `run` exits with a clear "run `archiver pair` first" and does not crash-loop under systemd.

**Stage 2: data fidelity**
- **H5** history import: unwrap ephemeral / view-once / document-with-caption, correct sender for own messages, keep push names, keep edits as events. Prefer whatsmeow's `ParseWebMessage` exposed from `internal/wa` and injected as a fake at the boundary.
- **H6** chat names are never wiped by an empty name; excluded chats never get a `chats` row from history.
- **H7, H8** chat events keep their payload, timestamp and a deterministic ID; only a real revoke is a revoke.
- **H9** unique ledger identity for history chunks.
- **H1** insert cost must not grow with table size (alias lookup by primary key); add a scaling test.

**Stage 3: media and health**
- **H3, H4, H11** media: valid re-upload request (message info and key from the row), rate-limited retries with the injected clock and a refill pass, worker panic marks the row `failed: panic` and is counted and alerted.
- **M4** seconds-scale backoff and later re-queue for transient failures.
- **H10** health rules from Task 9 (disconnected too long, phone quiet, recovered, alert gate) and the full Task 9 test list; move `Notify` off the handler path (M2).

**Stage 4: guardrails, backup, replay, CLI**
- **H12** CON-1 guard: prefix-based ban plus an explicit allow-list, exception limited to `SendMediaRetryReceipt`; redo the canary with `SetStatusMessage` and with `SendMessage` inside `rerequest.go`.
- **H15, H16** service `ReadWritePaths` vs backup path; retention removes only snapshot folders.
- **H14** scrub (device-suffixed JIDs, numbers inside protobuf), a CLI entry point, recorder for all kinds, `DumpRaw`, synthetic golden scenario, `TestReplayMatchesGolden`.
- **H17** `status` / `chats`: read-only open, time zone, missing fields, golden tests.
- **M5** goleak in `ingest`, `media`, `health`; join goroutines in `serve`.
- Everything in Table 1 of `docs/prior-art.md` exists and passes (**H13**), plus the other missing Review Focus tests listed in review section 7 item 15.

**Stage 5: docs for the live run**
- **M8** make `docs/phase0-setup.md` followable by a Linux beginner (expected output after each command, copy the service file, config ownership, ntfy test, update commands) and `docs/phase0-acceptance.md` complete (spike numbers, SQL for step 8, expected results, `SetPassive(false)` added to the receipt-audit notes).
- Then the "can wait" list (M6, M7, M9 to M13, L1 to L5) as time allows.

## 5. Per-finding routine

1. Quote the finding ID and its evidence line.
2. Write the red test; run it; paste the expected failure.
3. Commit `test: …`.
4. Lock, implement, `make check`, unlock.
5. Re-run the matching probe and mutation; show it is now killed.
6. Commit; update `PROGRESS.md` (task/finding, commit hashes, coverage of touched packages, deviations, open questions).

## 6. Done when

- Every finding marked Blocker or High in the review is fixed, or Hamza has explicitly deferred it in writing in `PROGRESS.md`.
- `make check` passes on a clean clone, including race tests, lint and the cross-builds (Linux amd64/arm64, macOS arm64/amd64, Windows).
- A fresh run of the review's mutations kills every Review Focus 1 to 10 row, and the probe tests no longer reproduce the bugs.
- Your final message gives: what changed per stage, mutation before/after counts, anything you deferred, anything Hamza must decide, and the exact commands to build the Linux binary and copy it to the server for Task 12. Do not attempt Task 12.
