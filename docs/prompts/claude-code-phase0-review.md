# Review Phase 0 of WhatsApp Archiver (built by Codex)

You are an independent reviewer. Codex implemented phase 0 of WhatsApp Archiver in this repo, following a signed-off plan. Your job is to find out, with evidence, whether what it built matches the plan, keeps the hard constraints, and is ready for the live acceptance run (Task 12) on Hamza's Linux server with his iPhone.

Codex's own account in `PROGRESS.md` says everything passed. **Treat that as a claim to verify, not a fact.** This is an evaluation pass: don't fix anything. Report what you find, then stop and wait for Hamza's go-ahead.

## 1. What to read

- `project-docs/superpowers/plans/2026-10-14-phase0-capture-logger.md` — the plan: Tasks 0–12, Global Constraints, Engineering Principles, Review Focus 1–10, File Structure. This is the standard you review against.
- `project-docs/superpowers/plans/WhatsApp Archiver — Project Charter & Build Plan.md` — the charter v1.1. Sections 5, 7, 11, 13, 13b and 16 matter most.
- `project-docs/prompts/codex-phase0.md` — the brief Codex was given, including its adaptations: one self-contained folder, `AGENTS.md`, red/green commits, no live WhatsApp.
- `project-docs/research/researchoutcome.md` — background for whatsmeow edge cases.
- Then the build itself: `AGENTS.md`, `PROGRESS.md`, `docs/`, `Makefile`, `go.mod`, `cmd/`, `internal/`.

## 2. Before reviewing: check the copy is complete

Hamza moved the build into this folder. The listing he sees shows no hidden files, which suggests they didn't come across: `.git`, `.gitignore`, `.golangci.yml`, `.github/`, `.claude/`, `.githooks/`, `.tools/`, `.cache/`.

- Check which of these exist.
- If `.git` or `.golangci.yml` is missing, **stop and ask Hamza where Codex's original folder is** (the old `archiver/` folder, a T3 worktree, or the Trash). Don't recreate them: the commit history and the lint config are part of what you are reviewing.
- Continue only after he restores them, or tells you to review without them; in that case mark every check that needed them "not verifiable".

`AGENTS.md` still points at `../docs/…`. The planning docs now live in `project-docs/`. Note this as a finding, but don't edit it.

## 3. Ground rules

- **Never connect to WhatsApp.** Don't run `archiver pair` or `archiver run` against the real service, and don't create a session.
- **Install nothing globally.** Go isn't installed on this Mac. Download Go 1.26+ for this OS and architecture from go.dev into `.tools/go`, which is where the Makefile expects it, and run everything through the Makefile so caches stay in `.cache/`.
- **Don't change the repo.** Experiments (mutations, canaries) run in a scratch copy: `cp -R` the repo to a temp folder. Leave the real folder exactly as you found it, apart from `.tools/`, `.cache/` and your report.
- **Evidence for every finding:** file and line, the command you ran and its output, and the plan section it breaks. Say "not verified" when you couldn't check something.
- **Parallel subagents.** You may use them for independent areas in section 4. Give each one the plan section it checks against. Merge their results yourself, and re-check any blocker they report before including it.

## 4. What to check

1. **Gates.**
   - Run `make check`. Record the result for each gate: race tests, lint and the three cross-builds.
   - Run `go test -cover ./...` and record coverage per package.
   - Open `.golangci.yml`. Confirm `depguard` bans the root `go.mau.fi/whatsmeow` package outside `internal/wa`, and `forbidigo` bans the send methods listed in the plan's Global Constraints, with only the one allow-listed file.
   - In the scratch copy, re-run the Task 1 canary: lint and `internal/archtest` must both fail on a file that imports the client and calls `SendMessage`.
2. **Plan conformance, task by task (0–11).** For every task, compare the plan's Files, Interfaces and Step 1 test list with what exists. Build a matrix with these columns:
   - test the plan names
   - exists?
   - passes?
   - would it fail if the behaviour broke?
   - notes

   Flag every planned test that is missing, renamed into something weaker, or deferred.
3. **Review Focus 1–10, by mutation.** For each item, break the behaviour in the scratch copy and run the tests. At least one test must fail; if none does, the behaviour is untested. Revert each mutation before the next. Examples:
   - drop `ON CONFLICT DO NOTHING`
   - remove the alias check in `InsertMessage`
   - remove the panic guard around the handler
   - make `Ingest` return early on a write error
   - stop storing undecryptable events
   - skip the history ledger insert
   - make `decide` download stickers
   - treat `client_outdated` like `disconnected`
4. **Hard constraints, read in the code:**
   - **CON-1:** nothing a contact can see is sent; no `SendPresence` and no forced active receipts; the only send is `SendMediaRetryReceipt` in `internal/wa/rerequest.go`; `AutomaticMessageRerequestFromPhone` is on; `SynchronousAck` is on (Codex says it is).
   - **CON-2:** no UPDATE or DELETE on raw tables.
   - **CON-7:** JIDs stored as received, with aliases resolved in views.
   - **One ingest path:** live, history and replay all go through `Pipeline.Ingest`.
   - **One write connection** to `archive.db`.
   - **Panics recovered** only at the two boundaries, the event handler and the media workers.
   - **Never back up `session.db`.**
   - **Timestamps stored as given**, plus the time received.
   - **Data never dropped:** a write failure keeps retrying while an alert fires.
5. **whatsmeow correctness against the pinned source.** Check every whatsmeow identifier the code uses against the source in the module cache (`.cache/gomod/go.mau.fi/whatsmeow@<pinned version>/`) and against `docs/prior-art.md` Table 2. Look closely at:
   - pairing and QR printing
   - the synchronous event handler and reconnect
   - the version check and the re-check after `ClientOutdated`
   - the history-sync device properties
   - `TemporaryBan` and `StreamReplaced` handling
   - media download, expired-media errors and the `MediaRetry` flow
   - **history import:** Codex rebuilds `events.Message` from `WebMessageInfo` by hand instead of using whatsmeow's own parser. Judge whether fields are lost (sender and participant, LID/alternate JIDs, timestamps, edit and revoke keys) and whether the parser could be used inside `internal/wa` instead.
6. **Concurrency and resilience:**
   - goroutine ownership and shutdown
   - leak tests (the plan requires goleak; `go.mod` doesn't list it)
   - the media queue's claim and reset logic under several workers
   - the rate limiter (Codex made it wall-clock where the plan wanted the fake clock)
   - run the suite with `-race -count=20` on `internal/media`, `internal/ingest` and `internal/health`
7. **The deferrals in `PROGRESS.md`.** For each item Codex pushed to Task 12, decide whether it is really untestable offline, or a planned unit test that was skipped and could be written with fakes now. Items include:
   - recovered alerts
   - panic-row failure marking
   - replay coverage beyond message envelopes
   - golden files for replay and for `status` / `chats` output
8. **Dependencies.** `go.mod` lists `golang.org/x/time` and `github.com/mdp/qrterminal/v3` as indirect, although the plan has our code use a rate limiter and print QR codes directly. Find out whether they are really used, and whether `go mod tidy` leaves `go.mod` unchanged.
9. **Git history** (if `.git` is back):
   - Every task should have a `test:` commit before its implementation commit.
   - No implementation commit touches a `_test.go` file.
   - The pre-commit hook and `.claude/green.lock` mechanism exist and work.
   - The commit hashes in `PROGRESS.md` are real.
10. **Docs and operability:**
    - `docs/prior-art.md`: Table 1 rows map to real tests; Table 2 names match the code.
    - `docs/phase0-setup.md` is followable by a Linux beginner, including the config example, pairing, the service, updates, the rsync media backup and the session rule.
    - `docs/phase0-acceptance.md` covers all 13 steps of Task 12 and spikes 1–5.
    - `deploy/archiver.service` is sound.
    - The Makefile can bootstrap Go on a fresh machine, and builds a binary for Hamza's Linux server and for the Mac's architecture.
11. **Code quality against the Engineering Principles:** YAGNI, DRY, small packages, errors wrapped with context, no dead code, interfaces only at external boundaries, readable names.

## 5. Report

Write `project-docs/reviews/2026-10-14-phase0-review.md`, then tell Hamza it's ready and stop. Use this structure:

1. **Verdict.** At most 5 sentences: ready for Task 12, ready after the listed fixes, or not ready, and the single biggest problem.
2. **Gate results.** `make check`, coverage per package, canary, race runs.
3. **Findings**, sorted by severity, one table: ID, severity, area, what's wrong, evidence (file:line or command output), plan reference, suggested fix. Severity levels:
   - **Blocker:** breaks CON-1 or CON-2, can lose data, or stops pairing or capture from working.
   - **High:** a Review Focus behaviour is untested or wrong; a planned test is missing; a whatsmeow misuse.
   - **Medium:** an unjustified plan deviation; a weak test; a missing doc step.
   - **Low:** style, naming, small doc issues.
4. **Conformance matrix:** Tasks 0–11, planned tests against what exists.
5. **Mutation results:** Review Focus 1–10, the mutation made, which test caught it, or "none".
6. **Deferrals:** each one marked "legitimately live-only" or "should be a unit test now".
7. **Fix plan:** an ordered list Hamza can hand back to Codex or to you, grouped into "before Task 12" and "can wait". Each fix names the failing test to write first.
