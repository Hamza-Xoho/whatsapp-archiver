# WhatsApp Archiver agent rules

Source of truth: `docs/plans/2026-10-14-phase0-capture-logger.md`.
Charter: `docs/charter/WhatsApp Archiver — Project Charter & Build Plan.md`.
Research: `docs/research/researchoutcome.md`.
Review work list: `docs/reviews/2026-10-14-phase0-review.md`. Prompts: `docs/prompts/`.

## Commands

- `make test`: race-enabled tests.
- `make lint`: pinned golangci-lint.
- `make build-all`: Linux, macOS, and Windows builds.
- `make check`: all gates; run before every implementation commit.
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

## Workflow

- Strict red → green → refactor. Commit failing tests separately as `test: ...`.
- During green create `.claude/green.lock`; do not edit tests; delete the lock after `make check`.
- Never delete, weaken, or skip a test. Add a failing regression test before fixing a bug.
- Golden files may be created once with `-update` only after every line is reviewed and the review is logged in `PROGRESS.md`.
- Use fakes only at external boundaries. Every goroutine has an owner and stops on context cancellation.
- Prefer existing helpers: one retry helper, panic guard, and testkit package.
- Look up every whatsmeow identifier in `docs/prior-art.md`; never guess an API.
- Record task status, commits, coverage, deviations, and questions in `PROGRESS.md`.

## Package boundaries

- `internal/wa`: client, session, pairing, versions, downloads, and the one media re-request.
- `internal/ingest`: pure normalization/filtering and the single ingestion path.
- `internal/store`: schema, one writer, reads, and alias views.
- `internal/media`: table-backed workers; no in-memory job list.
- `internal/health`, `internal/backup`: alerts/snapshots; neither imports the WhatsApp client.
- `internal/retry`, `internal/guard`, `internal/testkit`: shared implementations; do not duplicate.

Procedures live in `.claude/skills/`: `bump-whatsmeow`, `add-event-kind`, and `record-fixtures`.
