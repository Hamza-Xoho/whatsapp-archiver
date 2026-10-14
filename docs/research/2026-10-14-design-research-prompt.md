# WhatsApp Archiver: design and build research prompt

Paste everything inside the box below into Claude with Research turned on (or any web-enabled deep-research agent). The prompt works on its own. If you want, you can also attach the charter and the phase 0 plan to the same chat for extra detail.

Source docs: `docs/WhatsApp Archiver — Project Charter & Build Plan.md` (v0.3, 13 Oct 2026) and `docs/superpowers/plans/2026-10-14-phase0-capture-logger.md`.

````text
You are a senior software architect running a design review and prior-art investigation for a solo developer who builds everything with AI coding agents (Claude Code). Your job is not to approve the plan below. Your job is to find out, using current evidence, whether there are better, simpler or more reliable ways to design and build it. Look hardest for places where the plan reinvents something that already exists, builds for needs that may never arrive, or depends on a fragile part. Treat this as a pre-build architecture review. That means specific, sourced and dated evidence, named projects and libraries with versions, and an honest "no good evidence found" wherever that is the truth. It is now October 2026. WhatsApp's protocol and the libraries around it change month to month, so prefer 2025–2026 sources and date everything.

## The project

**What it is.** WhatsApp Archiver is a self-hosted app that pairs with a personal WhatsApp account as a linked device. It silently records chosen chats into a local database: messages, edits, deletions, reactions, pins, polls, group changes and media. It then runs plug-in processing modules over what it records, such as voice-note transcription, translation, OCR and classification. A local web dashboard controls all of it, and a read-only MCP server lets AI agents (Claude, Hermes Agent) query the archive.

**Why it exists.** The developer is a first-year medical student at a university in Saudi Arabia. Course communication there runs through WhatsApp groups that act as the de facto learning-management system. Announcements, schedule and room changes, quiz dates and files are mixed into student chatter. Almost everything is in Arabic, probably a mix of Modern Standard Arabic and informal Saudi dialect, and some of it arrives only as voice notes or PDFs. Media links expire, nothing is searchable across groups, and there is no translation. The archive also feeds a wider personal project that captures everything from university (email, LMS, files) for use with AI tools.

**Who builds and uses it.** One self-taught developer, strongest in Python, builds the whole thing through Claude Code. His workflow is spec → plan → test-driven, task-by-task subagent execution. Build time is not the constraint. The constraint is long-term maintainability by one developer working through AI agents. He is the main user, but the design standard is a public GitHub release that a non-technical person can install on macOS, Windows or Linux using only the README. His hardware is an always-on Linux home server (a Ryzen desktop with integrated graphics only, so no usable GPU), an old ThinkPad, and a Mac.

**The current design (charter v0.3 and phase 0 plan, 13–14 Oct 2026):**
- Capture: Go, using whatsmeow (go.mau.fi/whatsmeow) as a linked device. Full history sync is requested at pairing. Everything is logged by default, with a per-chat exclude list (a "selected chats only" mode also exists). Status, typing, presence, receipts and calls are skipped. The raw serialized protobuf is kept for every message.
- Storage: SQLite (WAL mode) on local disk, in three layers:
  - raw: chats, participants, messages, message_events, media. Insert-only; edits, deletions and reactions are stored as new event rows.
  - derived: one generic artifacts table that every module writes to.
  - control: modules, per-chat module settings, jobs.

  The WhatsApp session keys live in a separate SQLite file. Media goes in a folder tree by chat and month. Backups are nightly VACUUM INTO snapshots with an integrity check and retention.
- Media: downloads run on their own worker queue so they never block logging. Documents, images and audio are downloaded; video is capped at 100 MB; stickers and GIFs are skipped. Failed downloads retry with backoff, and the app requests a re-upload when a media link has expired.
- Modules: separate programs in any language (Python by default), one folder each, each with a manifest. The manifest gives:
  - name and version
  - triggers: a message type, a MIME type, or an artifact kind that another module produces
  - the artifact kinds the module produces
  - typed settings, from which the UI form is generated automatically
  - required API keys and hardware needs
  - the run command
  - the mode: per_job (one process per message, JSON in and out) or service (a long-running process fed jobs line by line)

  Modules chain through artifact kinds (transcribe → translate → classify). Jobs are keyed by message, module and version, with retries and versioned re-processing. Engines (a local model or an API) are adapter settings inside a module, not separate modules. Adding a module must never change core code, the database schema or the UI.
- Topology: one binary that can run three roles, which talk only over a hub API with node tokens, so roles can be spread across machines by config:
  - hub: owns the database, media, job queue, API and UI
  - capture: holds the WhatsApp session and buffers locally if the hub is down
  - worker: pulls jobs and reports its capabilities, such as a GPU or Python

  Tailscale can optionally be embedded in the hub for remote access.
- UI: Svelte or React (built with Vite), embedded in the Go binary, with live updates over Server-Sent Events. It has seven screens: dashboard (per-chat pipeline strips and an activity feed), chats, modules, queue, archive search with a per-message trace, settings, and nodes. On top of that come a first-run wizard, a "doctor" diagnostics command, and an installer for each setup profile (single laptop, home server, split across machines, plus optional remote access).
- Alerts: push notifications through ntfy and Telegram when the device is logged out or disconnected, when no events arrive, when a backup fails, or when disk is low.
- Build order: phase 0 is a headless, capture-only logger with a CLI (pair, run, status, backup) and a systemd service, built with strict TDD so that capture starts as early as possible. Later phases add, in order:
  1. the UI and chat controls
  2. media rules and history backlog
  3. jobs and live views
  4. the module system, with transcribe and translate as the first modules
  5. archive search and the MCP server
  6. the setup wizard, installers and public release

**Fixed constraints. Do not propose breaking these. If you think one is wrong, say so in a separate note, with evidence.**
1. The app never sends anything to WhatsApp: no messages, reactions, read receipts, presence or typing. Ideally the build contains no send code at all.
2. Raw captured data is immutable; processing only adds to it.
3. Data and the live database stay on local disk, never in a cloud-sync folder. Third-party APIs see content only through modules the user switches on.
4. It links to the user's personal number, because that is the account in the groups. The WhatsApp Business Cloud API is ruled out because it can't read personal groups.
5. It must be publishable: no secrets in the repo, and it runs on macOS, Windows and Linux.

## What to investigate (in priority order)

For each question, compare the current plan with the alternatives you find. End each one with a recommendation (keep, change or replace), your confidence, and the evidence it rests on.

1. **Build vs. reuse: how much of this already exists?**
Why it matters: the plan writes everything from scratch: the capture daemon, archive schema, media downloader, job queue, module runner, dashboard and MCP server. If a maintained open-source project already covers a large share, building on it could save most of the work and inherit years of bug fixes against WhatsApp's protocol.
Evidence that would settle it: maintained projects that passively archive a personal WhatsApp account into a queryable store. For each, find its architecture, storage format, history backfill support, media handling, license, latest release, number of maintainers and the health of its open issues. Evaluate at least:
- lharries/whatsapp-mcp
- the mautrix-whatsapp Matrix bridge, with a homeserver and client acting as the archive and UI, and modules running as bots
- WAHA (WhatsApp HTTP API)
- Evolution API
- aldinokemal/go-whatsapp-web-multidevice
- any whatsmeow-based CLI or sync tool that writes to SQLite with full-text search

Search GitHub beyond these. For each serious candidate, estimate what share of this project's requirements it covers and what adapting it would cost. Also say whether depending on it would make the never-send guarantee harder to prove.

2. **Is the capture layer as reliable and safe as the plan assumes?**
Why it matters: everything rests on a silent linked device staying connected for months on a personal number without losing messages and without the account being restricted.
Evidence that would settle it:
- The 2025–2026 state of whatsmeow (release cadence, breaking changes, how responsive the maintainers are), compared with alternatives such as Baileys and whatsapp-web.js.
- Documented bans or restrictions of receive-only or linked-device clients. Separate repeated patterns from one-off anecdotes, and say which behaviours drive them (sending, automation, new numbers, many devices).
- How history sync behaves in practice: how far back it reaches, whether a full sync can be requested reliably, and how it differs between Android and iOS.
- The window after which an inactive linked device is logged out.
- Handling of LIDs (WhatsApp's anonymous member identities in groups), including mapping them to phone numbers.
- Messages that fail to decrypt, and retry receipts.
- Whether whatsmeow sends any receipts or presence automatically that a never-send design has to account for.
- Re-downloading expired media.

Look at how mature whatsmeow users, especially mautrix-whatsapp, handle each of these, since their code holds hard-won fixes. Also check whether there is a sound way to backfill history older than the history-sync window, for example from an Android or iOS phone backup using a tool like WhatsApp-Chat-Exporter, and to merge it without creating duplicates.

3. **Is the module system the right shape, or is there a proven pattern to copy?**
Why it matters: "add a module without touching the core" is the project's main promise of extensibility, and it is the part Claude Code will build against most often. A home-grown design (manifest, subprocesses, SQLite job queue) is easy to get subtly wrong in retries, idempotency, back-pressure, the lifecycle of long-running service modules, and versioned re-runs.
Evidence that would settle it:
- How comparable self-hosted apps structure their processing pipelines and plugins, and what their maintainers say went wrong. Candidates:
  - Immich: a separate machine-learning service, job queues and a job-status UI
  - Paperless-ngx: consume → OCR → classify
  - Karakeep: a SQLite-backed queue with AI inference workers
  - ArchiveBox: extractor plugins
  - Frigate
  - Home Assistant integrations
  - Datasette plugins
- Plugin mechanisms compared: subprocess JSON over stdin/stdout, HTTP workers, HashiCorp go-plugin, WebAssembly (for example Extism), and MCP itself as the module contract (each module an MCP server or tool).
- Job-queue options that suit a single Go binary on SQLite (for example goqite, backlite or a hand-rolled queue), and whether a workflow engine would be overkill.

Recommend the simplest contract that still lets an AI agent add a Python module from one instruction and test it against fixtures.

4. **Is the multi-machine role split worth building in v1?**
Why it matters: roles talking over an authenticated API, node tokens, job routing by capability, local buffering on the capture node and an embedded Tailscale node add up to a lot of distributed-systems code. The main user will probably run everything on one box.
Evidence that would settle it:
- What comparable self-hosted apps did, and how much complexity it added: for example Immich's remote machine-learning option, Frigate's detectors, and Tdarr's remote nodes.
- Whether remote workers can be added later without rework, if v1 is one process with a clean internal boundary.
- The current state and known problems of embedding Tailscale through tsnet.
- The simplest design that keeps the split available as a later option.

Give the strongest case for building the split now and the strongest case against.

5. **Which hub, UI and data stack is easiest to build with AI agents and to maintain?**
Why it matters: the plan pairs a custom Go HTTP API with a separate Svelte or React single-page app embedded at build time. That means two languages, a JavaScript toolchain and a hand-built real-time layer. A simpler stack might cut the code dramatically.
Evidence that would settle it: compare these options:
- (a) the current plan
- (b) PocketBase used as a Go framework for the hub (SQLite, auth, real-time subscriptions, admin UI, file storage, hooks)
- (c) server-rendered Go with templ plus htmx or Datastar, with no single-page app
- (d) any other strong option you find

Judge each on how much code it takes to build the seven screens and live views, its real-time support, how reliably AI coding agents produce working code in it (use practitioner reports, not guesses), maintenance burden, and single-binary packaging for all three operating systems.
For the data layer, cover:
- Arabic full-text search in SQLite: FTS5 tokenizers (trigram, unicode61, ICU), and normalising diacritics (tashkeel), alef and ta-marbuta variants, and tatweel.
- Whether to add semantic search (for example sqlite-vec) for the MCP use case.
- Continuous replication with Litestream or similar, compared with nightly VACUUM INTO snapshots.
- Whether whatsmeow's session store works cleanly with the pure-Go modernc.org/sqlite driver, or needs CGO with mattn/go-sqlite3.

6. **How should this be vibe-coded so the result is correct and stays maintainable?**
Why it matters: Claude Code will write every line from specs and plans. The developer wants proven techniques, not guesswork.
Evidence that would settle it: 2025–2026 practitioner reports and official guidance on building multi-phase apps with Claude Code or similar agents, covering:
- repo structure and CLAUDE.md conventions
- spec-driven workflows (for example GitHub Spec Kit, Kiro-style specs, and the superpowers plugin he already uses) and their measured trade-offs
- TDD with agents, and how to stop agents gaming the tests
- whether Go is a good language for agent-written code compared with TypeScript or Python
- how to test a WhatsApp client with no fake server to test against (recorded event fixtures, replay harnesses, golden files), including how mautrix-whatsapp and other whatsmeow projects test
- enforcing architecture rules mechanically rather than through the prompt: for example, banning send methods with golangci-lint forbidigo or depguard or with build tags instead of a test that greps the source; import-boundary checks; Claude Code hooks
- what commonly goes wrong in agent-built Go services, which this plan should guard against

7. **What does a minimal version that captures reliably look like?**
Why it matters: a message that isn't captured is gone for good, so the fastest route to trustworthy capture has the highest value. The plan already starts with a headless logger; check whether that phase is scoped right.
Evidence that would settle it: critique the phase 0 scope (pair/run/status/backup CLI, insert-only store, media queue, backups, ntfy and Telegram alerts, systemd service) against what reliable 24/7 capture actually requires, based on how existing whatsmeow-based loggers fail in the field. Name anything missing, for example handling of decryption failures, app-state sync, LID mapping, clocks and time zones, or log rotation. Also name anything that could wait.

Secondary questions (answer briefly after the main ones):

8. **Engines for Arabic.** As of late 2026, which speech-to-text options handle Saudi (Hijazi and Gulf) Arabic voice notes best? Compare local options on a CPU-only Ryzen machine (Whisper large-v3 or turbo via whisper.cpp or faster-whisper, plus any newer open models) with hosted APIs. Give published word error rates for Saudi or Gulf dialect where they exist, rough speed on CPU, and current price per hour of audio. Do the same for translating informal Arabic into English: LLM APIs compared with local models that fit on a CPU-only machine. Give the source and date of every benchmark.

9. **Packaging and release.** What is current best practice for shipping a single Go binary as a background service on macOS, Windows and Linux to non-technical users (for example GoReleaser, kardianos/service, Homebrew, Docker images, installers)? Give examples of self-hosted apps whose install docs actually work for beginners.

## Required stance on evidence
- Prefer primary sources: official docs, repositories, changelogs, release notes, and maintainers' own comments in issues and discussions. Treat SEO listicles and AI-written blog posts as weak evidence.
- For every project or library you recommend, give its version, latest release date, license, rough number of maintainers, and whether it is actively maintained as of October 2026.
- Date every claim about how WhatsApp behaves, and say when a claim rests on a single anecdote.
- Argue against the current design wherever the evidence supports it. Finding that a component is unnecessary, already exists elsewhere, or is fragile is the point of this review. Equally, say plainly where the current plan is already the best option, so it isn't changed for novelty's sake.
- Where reliable evidence doesn't exist, say so and suggest a short experiment that would settle the question.
- Keep recommendations within the fixed constraints and buildable by one developer working through Claude Code.

## Output format
Return your findings in exactly this structure:

# Design Research Report: WhatsApp Archiver

## Verdict at a glance
Five sentences at most: should the current design be built as planned, adjusted, or substantially replaced? Name the single biggest change, if there is one.

## The design as understood
A short restatement of the current design and its key bets, so the developer can confirm nothing was misread.

## Build vs. reuse
A table of the serious existing projects: what each covers, maturity, license, fit with the fixed constraints, and a recommendation (build on it / borrow ideas or code / ignore).

## Findings by question
For each question, most decision-relevant first: current plan → what the evidence says (with citations and dates) → recommendation (keep / change / replace) → confidence (high / medium / low).

## Recommended architecture
The revised design as a table or short text diagram. List each component, the chosen library or approach, what changed from the current plan, and why. If nothing should change, say so.

## Vibe-coding playbook
Concrete changes to the repo, CLAUDE.md, testing approach, guardrails and phase plan for building this with Claude Code.

## Red flags and the bear case
The strongest case against the current plan, and the risks that remain even with the recommended changes. Do not soften this section.

## What to change, in order
A prioritised list in three parts: before phase 0 starts; before the UI and module phases; later or optional.

## Spikes to run
Experiments of a day or less that would settle the remaining open questions. For each: what to build, what to measure, and what result would change the decision.

## Sources
Every source with its date, plus the version or release date for software.
````
