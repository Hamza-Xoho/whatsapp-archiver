# Archive MCP Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** Deliver a production-oriented Linux MCP companion with legacy/V1 reads, tested access controls, installation instructions and a Hermes setup guide.

**Architecture:** A separate Go stdio executable delegates fixed read-only tools to an archive reader. The Linux installer grants a dedicated account access only to archive/WAL/SHM files, while Hermes gets a fixed launcher. Optional module artifacts have a generic interface but remain unavailable until their archive schema exists.

**Tech Stack:** Go, modernc SQLite, official Go MCP SDK, YAML, Linux ACLs/systemd, Python 3 installation helpers/tests.

**Spec:** `docs/superpowers/specs/2026-11-07-archive-mcp-design.md`

## Global Constraints

- Linux amd64 bridge release only; preserve existing archiver cross-platform builds.
- No archiver restart, reinstall, schema/index change, migration, WhatsApp connection or live message read.
- `mode=ro`, `query_only`, 2-second request deadlines, 250 ms busy timeout, one request at a time, no immutable fallback.
- Empty selected-chat policy denies content; deny wins across aliases; reload policy each request.
- Bounded results, authenticated cursors, message references and safe current-state projection.
- Build scratch stays under ignored `.cache/tmp` because `/tmp` is a small tmpfs.
- Git identity was initially absent; Hamza subsequently approved the existing author identity for repository-local configuration and explicitly authorized committing/pushing this feature branch. Do not invent an identity or rewrite history.

## Review Focus

- WAL sibling recreation and reader reconnection must not require restarting capture.
- Aliases learned after a policy edit must never widen a denied chat's access.
- Edits/revocations must not leak old text through search hits or context.
- Malformed/oversized MCP input and responses must fail predictably without log disclosure.
- A Linux installation must not grant Hermes shell/session-file access through its privileged launcher.

## Shared interfaces

`internal/archiveread` exports `LoadConfig(path string) (Config, error)`,
`New(configPath string) (*Engine, error)`, `(*Engine).Call(context.Context, string, json.RawMessage) (Result, error)`,
`(*Engine).Inspect(context.Context) (map[string]any, error)`,
`(*Engine).Catalog(context.Context) ([]Chat, error)` and `CodeOf(error) string`.
`Config` has `Archive`, `RuntimeDir`, `ViewerBaseURL`, and `Policy` (Mode, AllowChats, DenyChats), matching the design YAML.
`Result` exports contract `schema_version`, `data`, `scope`, `observed_at`, `warnings`, `next_cursor`, `has_more`.
Tool names are the six core tools plus `list_artifacts` and `get_artifact`; the latter return `FEATURE_UNAVAILABLE` until an artifact adapter is supplied.

### Task 1: Compatible archive reader and policy

**Files:** Create `internal/archiveread/{config,reader,identity,messages,search,cursor,artifacts}.go` and focused `_test.go` files.
**Consumes:** Existing schema fixtures, pure `store.Fold` semantics.
**Produces:** Shared interfaces above; all query/authorization/projection behavior.

- [x] Write real SQLite fixture tests for legacy/V1, unknown schemas, no writes/missing archive, policy reload/aliases/deny, current edits/deletions, literal Arabic/English search, dates/limits/cursors/context, WAL concurrent inserts/recreation, and artifact unavailability.
- [x] Run through a temporary Makefile include: `make -f Makefile -f .superpowers/sdd/2026-11-07-archive-mcp/reader.mk reader-test`; record expected red before adding production code.
- [x] Implement the adapters and bounded reader; no raw protocol decoding or new archive objects. Add synthetic scale checks with measured deadlines and writer progress.
- [x] Re-run tests green; self-review policy predicates and cancellation; report evidence.
- [x] Commit only with configured identity and passing required gate; otherwise record the limitation.

### Task 2: MCP transport, CLI and Linux build

**Files:** Create `internal/mcpbridge/{server,schemas}.go`, `cmd/archiver-mcp/{main,lock_linux}.go` and tests; update `go.mod`, `go.sum`, Makefile.
**Consumes:** Shared reader interface.
**Produces:** `archiver-mcp serve|inspect|catalog|version`, official SDK stdio server, smoke-testable binary and Linux release tarball.

- [x] Write integration tests against the real SDK transport and synthetic archive: initialize/list/call, eight tools, denied content, JSON errors/unknown arguments, size clipping, stdout purity, cancellation, process-lock/EOF lifecycle, and inspect/catalog behavior.
- [x] Run red through a temporary Makefile include; only then add production code and pinned official SDK dependency.
- [x] Implement schemas, SDK error mapping and full-result size enforcement; request decoding rejects oversized inputs and unknown fields. CLI logs use stable redacted errors, not raw DB errors.
- [x] Add `mcp-test`, `mcp-build`, `mcp-release`, SDK dependency and formatting targets. Integrate bridge tests into full gates; build bridge for Linux amd64 only and stamp revision/dirty state.
- [x] Run green integration tests, binary version/inspect/stdin smoke tests; report evidence.
- [x] Commit only if identity and full gates permit.

### Task 3: Linux installation and Hermes documentation

**Files:** Create `deploy/mcp/` configuration, fixed launcher, ACL helper and systemd/tmpfiles templates; `scripts/install-mcp.sh`, uninstall helper and behavioral tests; update README and create `docs/mcp-linux.md` and `docs/hermes-linux.md`.
**Consumes:** Binary CLI/config and default paths from the design; no direct Go imports.
**Produces:** Additive Linux install/uninstall workflow and copyable human instructions.

- [x] Write sandboxed behavioral installer/helper tests proving malformed paths/users, symlinks, missing files, exact ACL scope, argv injection, preservation of policy/pre-existing ACLs, no archiver service mutation, and refusal to overwrite unknown installations.
- [x] Run red before adding scripts; test harness must never modify actual host users/services/ACLs.
- [x] Implement Linux-only install checks, dedicated reader, root-owned fixed launcher/sudoers rule, ACL restoration/maintenance and cleanup. Never run them against this host or Ryzen in this session.
- [x] Document source builds, installing alongside an existing service, selecting chats, metadata-only readiness, local MCP smoke tests, linking Hermes, updates, troubleshooting and reversible removal.
- [x] Write Linux Hermes install/setup using current official instructions, OpenRouter free/cheap selection and auxiliary-model costs, restricted archive profile/tool choices, cloud privacy boundary, and future modules/artifacts. Do not install Hermes or request credentials here.
- [x] Run sandbox tests green and shell/Python syntax checks; report evidence.

### Task 4: Integration, review and release verification

**Files:** Integration fixes, `docs/v1/STATE.md`, this plan and design status.

- [x] Independently review each implementation task for spec compliance and code quality; address material findings with failing regressions before fixes.
- [x] Run `make check`, installer tests and `make mcp-release`; check tarball contents/checksum and synthetic stdio smoke test.
- [x] Run whole-change security/correctness review, fix material findings, and rerun affected/full gates as warranted.
- [x] Record actual capabilities, limitations, checks and Git identity status; remove green lock on completed checks; keep production archive untouched.
- [x] Preserve reviewable source when identity is absent; the subsequent user instruction authorizes a feature commit/push with the approved repository-local identity. No PR, main update or remote deployment is requested.

## Handoff evidence

Completed implementation and independent reviews. Final `GOFLAGS=-p=1 make check` passed, including race/scale/lint/cross-platform capture builds, Linux MCP build and 27 installer tests. `make mcp-release` passed; all packaged file and archive checksums verified. The built executable passed a synthetic real stdio handshake/policy-reload/search/reference/EOF smoke. No live deployment or Hermes credential setup was performed. The user subsequently authorized a commit/push and supplied the existing author identity for local Git configuration. Verification/review logs remain in the ignored plan workspace. The feature starts at `f5a8824`; reconcile the 264 newer commits on `origin/main` (`1843fde` at publication preparation) before integration.
