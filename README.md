# WhatsApp Archiver

[![CI](https://github.com/Hamza-Xoho/whatsapp-archiver/actions/workflows/ci.yml/badge.svg)](https://github.com/Hamza-Xoho/whatsapp-archiver/actions/workflows/ci.yml)

A self-hosted, receive-only WhatsApp capture logger. It stays logged in as an extra linked device (like WhatsApp Web), writes every message and event it receives into a local SQLite archive, downloads the attached files, and tells your phone when something stops working. It never sends anything a contact can see.

It is written in Go on top of [whatsmeow](https://github.com/tulir/whatsmeow), builds to one static binary for Linux, macOS and Windows, and needs no C compiler.

> **Status: phase 0 (capture logger).** The capture pipeline, health alerts, backups and the Ubuntu deployment guide are built and tested. It has not yet been paired with a real account: that acceptance run is the next step (see [What is left](#what-is-left)). The modules, web UI and search described in the [charter](docs/charter/WhatsApp%20Archiver%20%E2%80%94%20Project%20Charter%20%26%20Build%20Plan.md) are later phases.

## Read this first

- **It is an unofficial client.** WhatsApp does not offer a supported way to archive personal chats, and linking an unofficial client carries a small risk that the account is restricted. The archiver stays silent to keep that risk low, but the risk is not zero.
- **It depends on staying up to date.** WhatsApp retires old client versions without warning. The archiver detects a rejected version, raises a "client outdated" alert and can look up the newest version itself, but you still have to update the program when told.
- **Your archive holds other people's messages.** Keep it, its backups and anything made from it for your own use, and check the law where you live before archiving anyone's messages. Chats that must never be stored belong in the `exclude` list, and it has to be set **before** the first pairing because stored rows are never deleted.
- **Your data stays on your machines.** The archiver talks to WhatsApp, and to the ntfy server you choose for alerts. Alert text contains no chat identifier and no message text.

## What it does

- Pairs as a linked device and requests the full available history, with explicit size and age limits. Every history chunk is recorded in a ledger so a missing chunk shows up.
- Stores messages, edits, deletions, reactions, group and chat events exactly as received. Rows are insert-only: nothing is updated or deleted.
- Records messages the phone could not decrypt as open gaps, and fills them in when the phone resends them.
- Keeps phone-number and LID identities apart as received and links them in a mapping table, so one person is one chat in the read views.
- Downloads documents, images, audio and video (sizes and types configurable) with a rate-limited retry queue for files WhatsApp has already expired.
- Skips the chats you exclude, matching both a person's phone-number and LID forms. If the exclusion check itself cannot run, nothing is written for that chat.
- Survives reconnects, temporary bans, replaced sessions and client-version cut-offs with bounded back-off, and shuts down without cutting an import short.
- Takes a nightly snapshot of the archive (never of the login session) and prunes old snapshots.
- Sends [ntfy](https://ntfy.sh) alerts: disconnected, logged out, banned, client outdated, no events, phone inactive, disk space low, write failures, recovered panics, failed backups.
- Includes a record and replay harness with a fixture scrubber, so real traffic can become test fixtures without real numbers.

## How it works

```
 WhatsApp ──► internal/wa ──► recorder ──► ingest.Pipeline ──► store (archive.db)
 (linked      (only package     (optional,    one write path:        insert-only tables,
  device)      that touches      --record)     live, history and      LID/phone alias views
               whatsmeow)                      replay all use it          │
                                                   │                      ▼
                                                   └──► media queue   media/ files
                                                   └──► health ──► ntfy alerts
```

- `internal/wa` is the only package that imports the whatsmeow client. Static guards (depguard, forbidigo and an AST test in `internal/archtest`) fail the build if any other package imports it, or if any code outside one allow-listed file calls a method that sends something visible.
- `archive.db` has exactly one writer. The login session lives in a separate `session.db`, which is never backed up, copied or restored.
- Live events, history sync and fixture replay go through the same `ingest.Pipeline`, so a replayed fixture exercises the real code.

The invariants, package boundaries and workflow rules are in [AGENTS.md](AGENTS.md).

## Quick start (developers)

You need `git`, `make` and `curl`. The first `make` downloads a pinned, checksum-verified Go toolchain and golangci-lint into `.tools/`; nothing is installed globally.

```sh
git clone https://github.com/Hamza-Xoho/whatsapp-archiver.git
cd whatsapp-archiver
make check                       # race tests, lint, and builds for 5 platforms
make build
.cache/build/archiver version
```

The first `make check` is slow because it builds the Go cache from scratch.

| Command | What it does |
| --- | --- |
| `make test` | Race-enabled tests |
| `make lint` | Pinned golangci-lint |
| `make build` | Build for this machine into `.cache/build/archiver` |
| `make build-all` | Linux, macOS and Windows binaries into `.cache/build/` |
| `make check` | All of the above; run before every commit |

## Running it

Pairing and running against a real account is covered step by step in [docs/phase0-setup.md](docs/phase0-setup.md), written for someone who has never used a Linux server (it covers both Ubuntu Server over SSH and Ubuntu Desktop at the keyboard): install Ubuntu, create a locked-down service user, configure ntfy, write the config, pair, start the systemd service, update and restore. The manual acceptance checklist for the first real run is [docs/phase0-acceptance.md](docs/phase0-acceptance.md). If you would rather have Claude Code do the setup for you over SSH, [docs/claude-code-deploy.md](docs/claude-code-deploy.md) explains how to give it a limited login and which steps still need you.

Do not run `pair` or `run` against a real account until you have read the setup guide's configuration section. Never run `run` in a terminal once the service is installed: it would use the same login as the service.

### Commands

```
archiver <command> [--data DIR]        DIR defaults to ./data
```

| Command | Purpose |
| --- | --- |
| `pair [--record DIR]` | Show the QR code, link the device, then capture. `--record` also saves supported events for building fixtures. |
| `run` | Capture as a service. Exits with code 3 (no restart) if not paired. |
| `status` | Read-only summary: connection state, client version, last event, history received, per-chat counts, failed and pending media, open gaps, data-folder size. |
| `chats` | Read-only list of chats with their canonical address and whether each is excluded. Use it to find group addresses for `exclude`. |
| `backup` | Take a snapshot of `archive.db` now. |
| `scrub IN OUT` | Replace the phone numbers and WhatsApp addresses in recorded fixtures with stable fakes. Message text and names are kept, so read the output before committing it. |
| `replay DIR [--data D] [--dump]` | Feed recorded fixtures through the real pipeline into a fresh archive. Refuses to write into an existing `archive.db`. |
| `version` | Program and whatsmeow versions. |

### Configuration

`<data>/config.yaml`, mode `0600`. Every key is optional; a misspelled key is an error. The full annotated example is in the [setup guide](docs/phase0-setup.md#6-write-the-configuration).

```yaml
time_zone: Europe/London
exclude:                         # chats that must never be stored
  - "441234567890@s.whatsapp.net"
history: {days_limit: 3650, size_mb_limit: 4096}
media:
  download: [document, image, audio, video]
  video_max_mb: 100
  skip: [sticker, gif]
alerts:
  ntfy: {url: "https://ntfy.sh/your-private-topic", token: ""}
backup: {destination: "/srv/backups/whatsapp-archiver", time: "03:30", keep: 7}
```

An `exclude` entry that is not `<digits>@s.whatsapp.net`, `<digits>@lid` or `<id>@g.us` is rejected when the config loads, because it would otherwise exclude nothing.

### What is on disk

| Path (inside the data folder) | Contents | Backed up |
| --- | --- | --- |
| `archive.db` | Messages, events, chats, id mappings, history ledger, media queue | Yes, nightly |
| `session.db` | The WhatsApp login (device keys) | **Never** |
| `media/` | Downloaded files | No |
| `config.yaml`, `health.json` | Settings; last known health state | No |

## Development

The project is built test-first and the rules are enforced by tooling, not by habit:

- **Red then green.** A failing regression test is committed separately (`test:`) before every fix (`fix:`). The pre-commit hook rejects deleted test files and, while `.claude/green.lock` exists, any change to a test file. Enable it with `git config core.hooksPath .githooks`.
- **Mutation testing.** [docs/reviews/2026-10-14-phase0-fixes-mutations/](docs/reviews/2026-10-14-phase0-fixes-mutations/) holds 285 exact-text mutation specs and a runner (`run.py`). On the current head all 285 are killed. Run a few with `python3 run.py specs.json <name>…`.
- **CON-1 guard.** Depguard, forbidigo and an AST test keep the WhatsApp client confined to `internal/wa` and ban every contact-visible send method.
- **Fixtures.** Real traffic is recorded with `pair --record`, scrubbed with `archiver scrub`, and committed only after the scrubbed files have been read. See `.claude/skills/record-fixtures/SKILL.md`.
- **CI.** `make check` runs on every push and pull request ([workflow](.github/workflows/ci.yml)). A weekly workflow opens a pull request that bumps whatsmeow to its latest commit, but only if `make check` still passes ([workflow](.github/workflows/bump-whatsmeow.yml)).

Where things are:

| Path | What |
| --- | --- |
| `cmd/archiver/` | CLI, service lifecycle, `status`, `chats`, `replay` |
| `internal/wa/` | The only whatsmeow client code: pairing, versions, downloads, the one media re-request |
| `internal/ingest/` | Normalization, exclusion filter, the single write path, recorder, scrubber |
| `internal/store/` | Schema, single writer, read views |
| `internal/media/` | Table-backed download workers and the re-request queue |
| `internal/health/`, `internal/backup/` | Alerts and snapshots |
| `internal/config/`, `internal/retry/`, `internal/guard/`, `internal/testkit/`, `internal/archtest/` | Config, shared helpers, static guards |
| `docs/` | [Charter](docs/charter/WhatsApp%20Archiver%20%E2%80%94%20Project%20Charter%20%26%20Build%20Plan.md), plan, research, setup and acceptance guides, review and fix history |
| `PROGRESS.md` | Per-task log of commits, coverage and deviations |

For the story of how the phase 0 review findings were fixed, start with [docs/reviews/2026-10-14-phase0-fixes-mutations/final-report.md](docs/reviews/2026-10-14-phase0-fixes-mutations/final-report.md).

## What is left

**To finish phase 0 (the next steps, and the only ones that need a real account):**

1. Follow [docs/phase0-acceptance.md](docs/phase0-acceptance.md): pair once, exercise a throw-away test group, drill the version-drift, restart, unlink and backup-restore cases, and check history reach.
2. Let it run for seven days and audit that no message is missing (charter SC-1 and SC-10).
3. Record the corpus: scrub and commit the fixtures the acceptance run produces.
4. Decide the open questions in the [final report](docs/reviews/2026-10-14-phase0-fixes-mutations/final-report.md): whether to buffer decrypted events for redelivery, whether to archive history stub messages, and the recording-versus-privacy trade-off.

**Known small gaps (none can lose data or leak a chat):** `go mod tidy`, goroutine-leak checks in `wa`, `backup` and `store`, a guard for exported functions returning `any`, and a list of minor wording and tooling items in the [ledger](docs/reviews/2026-10-14-phase0-fixes-mutations/ledger.md).

**Before it can be shared as a product (later phases):** a licence, the module contract and first processing modules (transcription, translation), the web UI with browser pairing, Arabic-aware search and a read-only MCP server, then packaging (releases, Docker, service installers, a first-run wizard). The plan for each is in section 13 of the charter.

## Licence and credits

No licence has been chosen yet, so the code is not available for reuse. Parts are adapted from [openclaw/wacli](https://github.com/openclaw/wacli) under the MIT licence; see [NOTICE](NOTICE) and `docs/prior-art.md`. The WhatsApp protocol library is [whatsmeow](https://github.com/tulir/whatsmeow) (MPL-2.0). This project is not affiliated with or endorsed by WhatsApp or Meta.
