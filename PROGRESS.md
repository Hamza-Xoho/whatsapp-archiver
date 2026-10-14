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
- Implementation commit: pending at time of this entry.
- Coverage: `internal/config` 92.9%; `internal/archtest` 92.5%.
- Canary: confirmed `internal/scratch/bad.go` failed lint with both `depguard` and `forbidigo`, and failed `TestRepoHasNoViolations` with `client-import` and `banned-call`; the canary was then deleted.
- Deviations: current whatsmeow recommends Go 1.27.1 but requires Go 1.26.0, so the module minimum remains the planned 1.26.0 and `GOTOOLCHAIN=auto` is enabled. Current Claude Code hook and golangci-lint v2 schemas were used.
- Open questions for Hamza: none.

## Task 2 — complete

- Test commit: `24d1b1d`.
- Implementation commit: pending at time of this entry.
- Coverage: `internal/retry` 66.7%; `internal/guard` 100%; `internal/store` 57.4%.
- Deviations: exposed `OpenGapCount` and `SchemaVersionCount` as small read helpers so tests stay on public interfaces. The read pool uses the same WAL database with read-only mode; the write pool is capped at one connection.
- Open questions for Hamza: none.

## Task 3 — complete

- Test commit: `61d73a4`.
- Implementation commit: pending at time of this entry.
- Coverage: `internal/ingest` 51.4%.
- Deviations: whatsmeow already unwraps ephemeral/view-once messages before emitting `events.Message`; normalization consumes `Message` while preserving `RawMessage`. No production client import was added.
- Open questions for Hamza: none.

## Task 4 — complete

- Test commit: `7abc461`.
- Implementation commit: pending at time of this entry.
- Coverage: `internal/wa` 13.7%; live connection/pairing glue is intentionally reserved for Task 12.
- Deviations: the pinned API has `Client.SynchronousAck`; it is enabled. `GetLatestVersion` now accepts `(context.Context, *http.Client)`, not the older single-argument form shown in its stale doc comment. Modernc's sqlstore upgrade was exercised in the unit test.
- Open questions for Hamza: none.

## Task 5 — complete

- Test commit: `08ca602`.
- Implementation commit: pending at time of this entry.
- Coverage: `internal/ingest` 51.0%.
- Deviations: handler write retries use short exponential intervals in-process; production failures still retry indefinitely after one health notification. `ImportHistory` is a compile-safe no-op until Task 6's red tests demand the implementation.
- Open questions for Hamza: none.

## Task 6 — complete

- Test commit: `d4497eb`.
- Implementation commit: pending at time of this entry.
- Coverage: `internal/ingest` 56.1%; `internal/store` 48.3%.
- Deviations: `Pipeline` reconstructs history `events.Message` values from `WebMessageInfo` because the signed-off constructor has no parser dependency; it uses only pinned protobuf/JID fields and still routes every row through `Ingest`.
- Open questions for Hamza: none.

## Task 7 — complete

- Test commit: `22cfbec`.
- Implementation commit: pending at time of this entry.
- Coverage: media behavior covered by rules and end-to-end durable queue tests; exact percentage not recorded.
- Deviations: the limiter is wall-clock driven by `x/time/rate`; queue timestamps remain fake-clock driven. Worker panic recovery preserves the process, but panic-row failure marking needs the Task 12 corpus to exercise the exact active row.
- Open questions for Hamza: none.

## Task 8 — complete

- Test commit: `e4883c6`; implementation commit: `b4ffeb1`.
- Coverage: snapshot integrity, restore readability, and session exclusion are covered.
- Deviations: none.
- Open questions for Hamza: none.

## Task 9 — complete

- Test commit: `1ba594d`; implementation commit: pending at time of this entry.
- Coverage: ntfy HTTP contract and immediate gated state alert behavior are covered.
- Deviations: periodic recovered-alert coverage is deferred to the manual acceptance drill; all immediate critical states and persisted health snapshots are implemented.
- Open questions for Hamza: none.

## Tasks 10–11 — not started

## Task 12 — not attempted

Per the brief, only a ready-to-fill acceptance checklist will be produced.
