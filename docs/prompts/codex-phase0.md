# Build Phase 0 of WhatsApp Archiver

You are implementing phase 0 of WhatsApp Archiver: a headless, receive-only WhatsApp logger in Go. The design and the plan are finished and signed off. Your job is to execute the plan faithfully, test-first, inside one new folder that holds everything the product needs.

## 1. Read first

These files are read-only for you. Never edit anything outside `archiver/`.

1. `docs/superpowers/plans/2026-10-14-phase0-capture-logger.md` — **the implementation plan and your source of truth**: Tasks 0–12, Global Constraints, Engineering Principles, Review Focus, File Structure. Ignore its first line about the superpowers sub-skill; that is for Claude Code.
2. `docs/superpowers/plans/WhatsApp Archiver — Project Charter & Build Plan.md` — the charter (v1.1). Read at least sections 5, 7, 11, 13, 13b and 16.
3. `docs/research/researchoutcome.md` — the design research behind the plan. Consult it when a task cites it.

## 2. One folder holds everything

Create `archiver/` at the project root. It is the whole product and its own git repository, so it can be moved, zipped or published on its own. The plan's File Structure is relative to `archiver/`.

```
archiver/                ← the WhatsApp Archiver itself: git repo root and Go module root
  AGENTS.md              ← the plan's CLAUDE.md content (agent rules); CLAUDE.md holds only the line "@AGENTS.md"
  PROGRESS.md            ← your task log (section 5)
  .tools/                ← Go toolchain (only if needed), golangci-lint and any other binaries; gitignored
  .cache/                ← Go module sources (GOMODCACHE), build cache, prior-art clones, scratch modules; gitignored
  models/                ← reserved for ML models in phase 1+; phase 0 downloads none, so don't create it yet
  data/                  ← runtime data (session.db, archive.db, media/, config.yaml); gitignored
  docs/                  ← the docs the plan creates: prior-art.md, MODULES.md, phase0-setup.md, phase0-acceptance.md
  .github/  .claude/  .githooks/  cmd/  internal/  testdata/  deploy/  Makefile  go.mod  …
```

Containment rules:
- The Makefile sets `GOPATH`, `GOMODCACHE`, `GOCACHE` and `GOBIN` to paths under `.cache/` and `.tools/`, and puts `.tools/bin` (and `.tools/go/bin` if used) first on `PATH`. Every Go command and tool you run goes through the Makefile or uses the same variables, so module sources, build caches and toolchain downloads all land inside `archiver/`.
- Install nothing globally: no `brew`, no `sudo`, no `npm -g`, no `go install` into `~/go`. If `go` 1.26+ is missing, download the official tarball for this OS and architecture from go.dev into `.tools/go`. If an older `go` exists, let `GOTOOLCHAIN=auto` fetch the newer toolchain (it lands in the in-folder `GOMODCACHE`). Install golangci-lint at a pinned version into `.tools/bin`.
- Your own source-scan walker in `internal/archtest` must skip `vendor/`, `.cache/`, `.tools/`, `data/` and `testdata/`. (`go test ./...` and golangci-lint already skip dot-folders and `vendor/`.)
- Go module path: `whatsapp-archiver`. It will change when a GitHub path exists.
- No licence: don't add a `LICENSE` file or licence headers to our own code (charter Q-8). Code adapted from openclaw/wacli keeps its MIT notice in `NOTICE` and in the file header, as the plan says.

## 3. Adapting the plan for Codex

The plan was written for Claude Code. Apply these substitutions and nothing else:

- **Agent rules file.** Where the plan says `CLAUDE.md`, write `AGENTS.md`, under 150 lines, and point it at the planning docs by relative path (`../docs/...`). Create `CLAUDE.md` containing only `@AGENTS.md`, so Claude Code sessions read the same rules.
- **Red and green as separate commits** (instead of separate subagents):
  1. Write the task's failing tests.
  2. Run them and confirm they fail for the expected reason, not a compile typo.
  3. Commit them as `test: …`.
  4. Create `.claude/green.lock`, implement, then delete the lock.
  5. Commit the implementation with the plan's message.

  The implementation commit must not touch any `_test.go` file. Enforce this with a git pre-commit hook in `.githooks/pre-commit` (`git config core.hooksPath .githooks`). It rejects staged `_test.go` changes while the lock exists, and rejects deleting any `_test.go` file at any time.
- **Stop hook.** The plan's Stop hook becomes your own habit: run `make check` before every implementation commit, and never commit an implementation that fails it. Still create the plan's `.claude/settings.json` hooks and skills, for later Claude Code sessions.
- **Golden files.** You may create a golden file once with `-update`, but only after reading every line and recording in `PROGRESS.md` that you checked it. Never regenerate a golden file to make a failing test pass; fix the code or stop and ask.

## 4. Hard rules

1. **Never connect to WhatsApp.** Don't run `archiver pair` or `archiver run` against the real service, don't display or scan QR codes, and don't create a session. Every test uses fakes, Go-constructed fixtures or the replay harness. The live run (Task 12) belongs to Hamza.
2. **CON-1 is absolute.** No whatsmeow send method appears anywhere except `SendMediaRetryReceipt` in `internal/wa/rerequest.go`. Show the Task 1 guard canary failing before you delete it.
3. **Never guess a whatsmeow API.** Names come from `docs/prior-art.md` Table 2 (Task 0), checked against whatsmeow's source at the pinned version (in the in-folder module cache). If the plan names something that doesn't exist, use the real equivalent and log the deviation.
4. **Never weaken, skip or delete a test to get green.** Every test named in `docs/prior-art.md` Table 1, and every Review Focus item in the plan, must exist and pass.
5. **Least code.** Follow the plan's Engineering Principles (TDD, YAGNI, DRY, one ingest path, one write connection, no fatal panics). Each new dependency gets a one-line justification in its commit message.
6. **Stay inside `archiver/`.** Read `../docs/`, never write to it.
7. **When blocked, stop and ask instead of working around it.** That includes network access denied by your sandbox, a design question the plan doesn't answer, or a constraint that can't be met. Never bypass the sandbox or a guardrail.

## 5. How to work

- Do Tasks 0 to 11 in order, following each task's steps exactly.
  - In Task 0, clone `openclaw/wacli`, `mautrix/whatsapp` (read its CHANGELOG and code for ideas only, copy nothing) and `tulir/whatsmeow` into `.cache/prior-art/`. Read wacli PRs #441 and #461 with `gh` or the GitHub API. Pin whatsmeow in a scratch module under `.cache/scratch/`.
  - Task 1 then pins the same version in `go.mod`.
- **Do not attempt Task 12.** It needs Hamza's Linux server and his iPhone. Instead, write `docs/phase0-acceptance.md` as a ready-to-fill checklist of Task 12's steps, with space for each result. The phone is an iPhone (charter Q-7), so spike 2 applies.
- After every task, update `PROGRESS.md` with:
  - task number and status
  - commit hashes
  - coverage of the task's packages
  - deviations from the plan and why
  - open questions for Hamza

  If your context is reset or compacted, read `PROGRESS.md` first and resume at the first unfinished task.
- Keep updates short while you work.

## 6. Done when

- Tasks 0–11 are committed with their tests, and `make check` passes on a clean clone of `archiver/`, including the Linux, macOS and Windows cross-builds.
- `docs/prior-art.md`, `docs/MODULES.md` (stub), `docs/phase0-setup.md` and the `docs/phase0-acceptance.md` checklist exist. The setup guide covers copying the build to the Ubuntu server, `config.yaml`, pairing, the service, updates and backups.
- Your final message gives:
  - what was built
  - every deviation from the plan
  - anything Hamza must decide
  - the exact commands to build the Linux binary on the Mac and copy it to the server for Task 12
