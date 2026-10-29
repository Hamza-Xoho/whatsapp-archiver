# WhatsApp Archiver agent rules

Source of truth: `docs/plans/2026-10-26-v1-plan.md` and `docs/v1/schema.md`.
Charter: `docs/charter/WhatsApp Archiver — Project Charter & Build Plan.md`.
Research: `docs/research/researchoutcome.md`.
Review work list: `docs/reviews/2026-10-14-phase0-review.md`. Prompts: `docs/prompts/`.

## Commands

- `make test`: race-enabled tests, then the 300k-message scale test (`-run AtScale`) without `-race`.
- `make lint`: pinned golangci-lint.
- `make build-all`: Linux, macOS, and Windows builds.
- `make check-fast`: Stop hook; race tests with `-short` (skips the scale test) on changed packages plus lint.
- `make test-quiet`: for agents; race tests showing only failures and the summary.
- `make check`: all gates; still required in full before every commit/merge.
- Run Go commands through this Makefile so toolchains, modules and caches stay inside the repo.

## Invariants

- CON-1: never send anything a contact can see. Never call presence, read-receipt, message, reaction, edit, delete, group, or account mutation methods. Only `internal/wa/rerequest.go` may call `SendMediaRetryReceipt`. The guards are static and cannot see calls made through reflection; do not use reflection on the client.
- Only `internal/wa` imports the root `go.mau.fi/whatsmeow` client package. Data subpackages are allowed elsewhere.
- CON-2: `messages`, `message_events`, `chat_events`, `id_mappings`, and `history_sync_chunks` are insert-only.
- CON-7: store JIDs exactly as received; resolve LID/phone aliases in read views.
- Live, history, and replay all use `ingest.Pipeline.Ingest`; no parallel write path.
- Exactly one connection writes `archive.db`.
- Recover panics only at the event-handler and media-worker boundaries; never crash the process for an event.
- Everything in `data/` is ignored. `session.db` is never backed up, copied, or restored.
- Never connect to WhatsApp from tests or agent sessions.
- A deployment session on a real server follows the "Let Claude Code do the setup" part of `README.md`: the agent never runs `pair` or `run` itself and never reads message text.

## Workflow

- Strict red → green → refactor. Commit failing tests separately as `test: ...`.
- Run `make check-fast` before ending every turn.
- During green create `.claude/green.lock`; while it exists, do not edit test files; delete the lock after `make check`.
- Never delete, weaken, or skip a test. Add a failing regression test before fixing a bug.
- Golden files may be created once with `-update` only after every line is reviewed and the review is logged in `docs/v1/STATE.md`.
- Use fakes only at external boundaries. Every goroutine has an owner and stops on context cancellation.
- Prefer existing helpers: one retry helper, panic guard, and testkit package.
- Look up every whatsmeow identifier in `docs/prior-art.md`; never guess an API.
- Record task status, commits, coverage, deviations, and questions in `docs/v1/STATE.md`.

## Package boundaries

- `internal/wa`: client, session, pairing, versions, downloads, and the one media re-request.
- `internal/ingest`: pure normalization/filtering and the single ingestion path.
- `internal/store`: schema, one writer, reads, and alias views.
- `internal/media`: table-backed workers; no in-memory job list.
- `internal/health`, `internal/backup`: alerts/snapshots; neither imports the WhatsApp client.
- `internal/retry`, `internal/guard`, `internal/testkit`: shared implementations; do not duplicate.

## Skills

- `.claude/skills/add-message-kind/SKILL.md`: use when adding a message or message-event kind to the V1 normaliser.
- `.claude/skills/bump-whatsmeow/SKILL.md`: use when updating the pinned whatsmeow version.
- `.claude/skills/deploy-ryzen/SKILL.md`: Hamza's Mac only; never run on this machine. Use there for an approved build, install, verification, or rollback on ryzen.
- `.claude/skills/record-fixtures/SKILL.md`: Hamza's Mac only; never run on this machine. Use there when recording, scrubbing, reviewing, and committing replay fixtures.
- `.claude/skills/ryzen-evidence/SKILL.md`: Hamza's Mac only; never run on this machine. Use there for read-only operational evidence from ryzen.

## Parallel work

- `mainarchiver` is main-only; do not edit or commit there except when integrating merges.
- Give each workstream its own worktree on branch `v1/<name>`, created from `origin/main`.
- Hamza decides merge order.
- Edit `docs/v1/STATE.md` only under your workstream's heading.
