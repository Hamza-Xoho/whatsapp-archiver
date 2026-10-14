# WhatsApp Archiver — Project Charter & Build Plan

14 Oct 2026 · @Hamza · **v1.1, signed off 14 Oct 2026**

## 1. TL;DR

WhatsApp Archiver is a self-hosted app that silently records a user's WhatsApp chats (all of them by default, or a chosen set) into a local database, then runs plug-in processing modules (transcription, translation, OCR, classification) over what it records, all controlled from a simple web UI.

It is built first as Hamza's own tool for Batterjee Medical College, where Arabic WhatsApp groups act as the de facto LMS. It acts as a linked WhatsApp device that never sends anything a contact can see, and it runs as one process on one machine, with an internal boundary that lets remote workers be added later if a real need appears. It stores raw data untouched and treats every kind of processing as a module that Claude Code can add later without changing the core.

The design research of 14 Oct 2026 (`docs/research/researchoutcome.md`) confirmed the core design and moved effort to where whatsmeow loggers actually fail in 2026: WhatsApp retiring client versions without warning, messages the device can't decrypt, the switch from phone numbers to LIDs, and history sync that arrives late and in pieces. Phase 0 now treats those as core features, and the distributed roles, built-in Tailscale, long-running module mode and JavaScript front end are dropped or deferred.

Version 1 is done when the app pairs from the browser, logs the chats the user has chosen (everything by default), saves their files, shows live per-chat processing status, and runs at least one module end to end through the module contract, with backups, phone alerts and the storage view working. It will be published on GitHub, with setup simple enough for a basic user on any machine.

**Document status:** v1.1, signed off 14 Oct 2026. Revises v1.0 using the design research. Items marked *agreed* came from Hamza or were approved by him; *agreed, from research* marks the v1.1 changes, and section 18 lists every one with its source. Later changes go through the decision log.

## 2. Problem and motivation

Key university information arrives as Arabic WhatsApp messages mixed into busy group discussion, and nothing records, translates or surfaces it reliably.

- **Problem:** BMC runs its course communication through WhatsApp groups. Announcements, schedule changes, quiz dates, room changes and shared files sit among student chatter. Everything is in Arabic, and some of it arrives only as voice notes or PDFs. *(agreed)*
- **Gap:** WhatsApp has no searchable archive across groups, no translation layer, and no way to separate announcements from discussion. Media links expire. Hermes Agent's WhatsApp gateway is built to chat back, not to listen passively, and it replies in groups by default. *(agreed)*
- **Cost of doing nothing:** missed deadlines and changes, slides and files lost to expiry, and no clean data feed for the wider BMC Archive project or for AI tools.

## 3. Users and use cases

The primary user is Hamza; next come people running their own copy from GitHub, from a single laptop to a home server. AI agents reading the data and Claude Code extending it are users too.

| Use case | Actor | Goal | Trigger |
| --- | --- | --- | --- |
| UC-1 | Hamza | Choose which chats, groups and communities get logged, and what processing each one gets | First setup; added to a new BMC group |
| UC-2 | Hamza | See at a glance what the app is doing per chat: logging, downloading, transcribing, translating, failing | Opens the dashboard |
| UC-3 | Hamza | Read and search the archive himself, in Arabic and in translation | Needs to check an announcement or find a file |
| UC-4 | Hermes, Claude or another agent | Query logged messages and their processed output to build digests and answer questions | Scheduled digest; Hamza asks a question |
| UC-5 | Claude Code | Add a new processing module from one instruction, without touching the core | Hamza says "add a module that does X" |

**Not for:** businesses, bulk messaging or marketing, or anyone needing a hosted service. Self-hosters setting up their own copy must find it easy (section 12e), but features follow Hamza's use.

## 4. Solution concept

A silent linked device writes chosen chats into a local database; plug-in modules enrich what it records; a web UI and a read-only MCP server sit on top. *(agreed)*

```
                    ┌──────────────────────── one process: archiver ───────────────────────────┐
 WhatsApp ────────▶ │ wa (only importer of the whatsmeow client, receive-only)                  │
 (linked device)    │   └─▶ ingest: normalise → scope filter → one ingest path ─▶ store          │
                    │                                                      archive.db (raw,     │
                    │ media queue (live first) ◀─────────────────────────  insert-only) + media/│
                    │ jobs (SQLite leases) ─▶ module runner ─▶ modules/<name> (one process/job) │
                    │                                   └─▶ artifacts (derived layer)           │
                    │ web (Go templates + htmx + SSE)   mcp (read-only)   health ─▶ ntfy        │
                    └───────────────────────────────────────────────────────────────────────────┘
```

Each part does one job: capture only records, modules only add artifacts, and agents only read. Any part can be replaced without touching the others.

## 5. Scope, non-goals and constraints

Version 1 is a read-only, local-only archiver with a UI and a module system; everything that sends, syncs to the cloud, or serves other users is out.

**In scope (v1):**

- Pairing as a WhatsApp linked device from the browser, with session status. *(agreed)*
- Per-chat, per-group and per-community control over what is logged. *(agreed)*
- Saving files (documents, images, voice notes, video with a size cap). *(agreed)*
- Processing as plug-in modules: transcription, translation with a chosen language, OCR, classification. *(agreed)*
- Live views of logging and processing, per chat and app-wide. *(agreed)*
- A read-only MCP server and exports so Hermes, Claude or other agents can use the data. *(agreed)*
- Capture hardening: surviving WhatsApp client-version cut-offs, recording decryption gaps, keeping LID and phone-number identities apart, and accounting for every history-sync chunk. *(agreed, from research)*

**Non-goals:**

- **Sending anything to WhatsApp that a contact can see** (messages, reactions, edits, read receipts, presence, typing) — the app must never act as Hamza in a group, and staying silent lowers ban risk. See CON-1 for the protocol traffic whatsmeow sends on its own.
- **Google Drive or any cloud sync in v1** — storage is local; an upload pipeline for the database and media comes later. *(agreed)*
- **Multi-user accounts, a hosted service or commercial use** — each person runs their own copy; the GitHub release must still be easy for anyone to install. *(agreed)*
- **The digest or agent logic itself** — Hermes or Claude consume the data through MCP; the archiver doesn't decide what to tell Hamza.
- **WhatsApp Business Cloud API** — it can't read Hamza's personal groups.
- **Running parts of the app on different machines in v1** — one process on one machine; the job boundary in section 9b leaves room for remote workers later. *(agreed, from research)*
- **A Tailscale node built into the app** — users who want remote access run Tailscale on the host. *(agreed, from research)*

**Constraints:**

| ID | Constraint | Type |
| --- | --- | --- |
| CON-1 | Never send anything a contact can see: no messages, reactions, edits, deletions, read receipts, presence or typing. The protocol traffic whatsmeow sends by itself (acks, delivery receipts marked inactive, decryption retry receipts, key uploads, history-sync receipts, app-state fetches) and media re-upload requests are accepted, because nothing shows in anyone's app. Only `internal/wa` imports the whatsmeow client, and lint bans every send method outside one allow-listed file. *(revised — agreed, from research; v1.0 said "no send code paths exist in the build", which can't be met because the send code lives in whatsmeow)* | safety |
| CON-2 | Raw logged data is immutable; processing only adds, never edits | data |
| CON-3 | Adding a module must never require changing core code, the database schema or the UI | architecture |
| CON-4 | Not tied to any machine or OS: one self-contained binary for macOS, Windows and Linux (pure Go, no C compiler needed). In v1 everything runs in one process. *(revised — agreed, from research)* | deployment |
| CON-5 | No secrets, session files or personal data in the repo; it must be safe to publish | repo |
| CON-6 | Live database stays on local disk; never inside a cloud-sync folder | data |
| CON-7 | WhatsApp identities are stored exactly as received (LID or phone-number form). The two forms are linked in a mapping table and resolved when reading, never by rewriting stored rows. *(agreed, from research)* | data |

## 6. Objectives and success criteria

The app succeeds if nothing in the chosen BMC groups is lost, Hamza can see what it's doing, and new processing is cheap to add.

| ID | Objective | Success criterion (measurable) | Verify via |
| --- | --- | --- | --- |
| OBJ-1 | Complete capture of chosen chats | SC-1: every message in an enabled chat over a 7-day period is in the database, checked against the phone by hand | Manual audit of 2 groups |
| OBJ-1 | | SC-2: 0 messages from disabled chats are written to disk | Database query after a week |
| OBJ-1 | | SC-10: no silent gaps — every message WhatsApp delivered but the device couldn't decrypt shows in status as a gap until it is recovered *(agreed, from research)* | Checked during the 7-day audit |
| OBJ-2 | Files survive expiry | SC-3: at least 99% of enabled-type media in enabled chats downloaded within 5 minutes of arrival, including while a history backlog is downloading | Media table stats |
| OBJ-3 | Simple control from a UI | SC-4: pairing, enabling a chat and turning on a module each take under 1 minute with no file editing | Walkthrough |
| OBJ-4 | Visible processing | SC-5: a status change for any job shows in the UI within 2 seconds | Watch the feed while a voice note processes |
| OBJ-5 | Modules are cheap to add | SC-6: Claude Code adds a new module from one instruction, touching only `modules/<name>/`, and it passes the module test harness | Add the OCR module this way |
| OBJ-6 | Reliable unattended running | SC-7: survives a restart and a dropped connection with no data loss and no re-pairing | Kill and restart test |
| OBJ-6 | | SC-9: when WhatsApp rejects the client version, an alert reaches the phone within 5 minutes and the app reconnects without a new build as soon as WhatsApp publishes a newer version *(agreed, from research)* | Version-drift drill (spike 3) |
| OBJ-7 | Anyone can set it up | SC-8: someone with no Linux or terminal experience completes profile A and profile B from the README alone, on fresh machines, without outside help | Hand the README to a non-technical tester before release |

**Definition of done (v1):** SC-1 to SC-10 pass; FR-1 to FR-12 and FR-16 to FR-26 implemented; transcription and translation run as modules.

## 7. Functional requirements

FR-1 to FR-12 and FR-16 to FR-26 make up v1; FR-13 to FR-15 are later work.

| ID | Requirement | Priority | Acceptance | Refs |
| --- | --- | --- | --- | --- |
| FR-1 | Pair as a linked device by scanning a QR code shown in the UI; show connection status and session age | must | Pair, restart, still connected | OBJ-3, OBJ-6 |
| FR-2 | Logging mode: everything (the default, with per-chat exclusions) or selected chats only; toggle logging per chat, group or community; communities expand to their sub-groups; processing modules are enabled separately per chat. An exclusion of a phone-number chat also covers its LID form once the mapping is known | must | Toggling changes what is written within one message | OBJ-1, OBJ-3 |
| FR-3 | In everything mode new chats are logged automatically and flagged as new; in selected mode they wait in a pending tray, buffered 48 hours, then kept on approval or deleted on rejection | must | Join a test group, approve, see buffered messages | OBJ-1 |
| FR-4 | Log messages, edits, deletions, pins, reactions, polls, group info and participant changes, and app-state changes (archive, mute, pin chat); record messages the device can't decrypt as gap events and have the phone resend them; record LID↔phone-number mappings as WhatsApp reveals them; skip typing, presence, receipts, Status and calls *(revised — agreed, from research)* | must | Edited message keeps both versions; a decryption gap shows in status, then becomes a message once resent | OBJ-1 |
| FR-5 | Download media on arrival by rule: documents, images, audio, video up to a size cap; skip stickers and GIFs; live media before backlog media; expired media re-requested from the sender at a limited rate; rules per chat from phase 2 *(revised — agreed, from research)* | must | SC-3 | OBJ-2 |
| FR-6 | Request full history at pairing with explicit day and size limits; import every history-sync chunk through the same filters, including chunks that arrive hours later; keep a ledger of chunks and show "history complete back to <date>" per chat *(revised from should to must — agreed, from research)* | must | Oldest recovered message per chat shown in status; a chunk that arrives after a restart is stored | OBJ-1 |
| FR-7 | Discover modules from their folders (`module.toml`) and generate their settings page, per-chat toggle and pipeline pill from the manifest | must | New module appears after restart with no UI change | OBJ-5 |
| FR-8 | Run modules as one process per job from a SQLite job table with leases: retries with backoff, permanent failures kept with their error, chaining through artifact kinds, versioned re-processing; a module may accept a batch of jobs so a model loads once *(revised — agreed, from research)* | must | Bump version, re-run over archive; kill a module mid-job and the job is retried | OBJ-5 |
| FR-9 | Live views: per-chat pipeline strip, activity feed, queue view with retry/skip, per-message trace, health panel | must | SC-5 | OBJ-4 |
| FR-10 | Browse and search the archive by chat, sender, date and text, including module output; Arabic search ignores diacritics, tatweel and common letter variants (أ/إ/آ → ا, ة → ه, ى → ي) | should | Find a message by its English translation, and by an Arabic word typed without diacritics | UC-3 |
| FR-11 | Read-only MCP server over messages and artifacts | should | Hermes or Claude can list and search | UC-4 |
| FR-12 | Ship transcription and translation (target language chosen) as the first two modules | must | Voice note → Arabic transcript → English translation | OBJ-5 |
| FR-13 | OCR and classification modules | could | — | OBJ-5 |
| FR-14 | Export to JSON, Markdown, CSV | could | — | UC-4 |
| FR-15 | Usage meter: API calls and estimated cost per module | could | — | OBJ-4 |
| FR-16 | First-run wizard: password, pairing, chat selection, module and engine setup, and a pointer to the remote-access guide | must | A new user records a first chat in about 15 minutes with no file editing | OBJ-3 |
| FR-17 | Engine choice per module, local model or API key, with cost, privacy and speed guidance, model download management, spending cap, and a test before use | must | No jobs run until the chosen engine passes its test on a sample | OBJ-3, OBJ-5 |
| FR-18 | Bulk actions: enable a whole community; apply settings to many chats at once | should | Ten chats configured in one action | OBJ-3 |
| FR-19 | Re-pairing flow for unexpected unlinks and for moving capture to another machine, with an inactivity warning beforehand; recovery is always a fresh pairing, never a restored session file | must | Unlink the device on the phone; re-pair from the UI with no data loss | OBJ-6 |
| FR-20 | Releases built with GoReleaser: archives for macOS, Linux and Windows, a Homebrew tap and a Docker image; service installers for Linux and macOS; a doctor command that checks the install and explains fixes in plain words. A Windows background service is added when a Windows user needs one *(revised — agreed, from research)* | must | SC-8 | OBJ-7 |
| FR-21 | Remote access guide: reach the dashboard from the user's other devices through Tailscale installed on the host; the dashboard asks for a login whenever it isn't opened on the host itself *(revised — agreed, from research; v1.0 built Tailscale into the app)* | should | Dashboard opens on a phone away from home by following the guide | OBJ-3 |
| FR-22 | README and guides as specified in section 12e | must | SC-8 | OBJ-7 |
| FR-23 | Backups: scheduled database snapshots taken safely while running, to a destination the user picks (another disk, a network share, or a cloud remote); media copied by a standard tool (rsync or restic) that the guide sets up; restore from the UI. Session keys are never backed up; recovery re-pairs *(revised — agreed, from research)* | must | Restore a backup onto a fresh install and find every message | OBJ-1, OBJ-6 |
| FR-24 | Notifications to the user's phone for health problems (disconnected, logged out, client version rejected, account restricted, no events, phone quiet, disk low), failed backups and module errors; ntfy first, further channels (Telegram, email) behind the same interface *(revised — agreed, from research)* | must | Unplug the network; an alert reaches the phone within the set period | OBJ-6 |
| FR-25 | Storage view: space used by chat, media type, database, models and backups; growth rate and projected days until full; a size estimate before enabling chats; alerts at set thresholds; ways to free space (per-chat media rules, retention such as keeping text but removing media older than a set age, moving the data folder to another disk) | must | The view's total matches the data folder's size on disk; an alert fires before the disk fills | OBJ-3, OBJ-6 |
| FR-26 | Client-version drift: detect WhatsApp's "client outdated" rejection as its own health state; at startup and after a rejection, use the newest client version WhatsApp publishes (with an operator override); the release pipeline bumps whatsmeow weekly and the docs give a one-command update *(agreed, from research)* | must | SC-9 | OBJ-6 |

## 8. Non-functional requirements

Reliability and safety matter most; speed matters only for the live UI.

| ID | Category | Requirement | Why |
| --- | --- | --- | --- |
| NFR-1 | Reliability | Runs as a background service; restarts itself; reconnects after network loss without re-pairing | It runs unattended |
| NFR-2 | Reliability | Writing a message never waits on processing; a failing module can't block logging | Capture must not depend on slow or broken modules |
| NFR-3 | Observability | A health alert reaches the user's phone (FR-24), not only the dashboard, when no event has arrived for a set period, when the session drops, when WhatsApp rejects the client version or restricts the account, and when the user's own phone has been quiet long enough to risk WhatsApp's 14-day inactivity unlink | Silence otherwise looks like "no new messages" |
| NFR-4 | Privacy | Data stays on the box; only modules Hamza enables send content to third-party APIs | Classmates' and staff messages are in the archive |
| NFR-5 | Maintainability | whatsmeow has no tagged releases; the project tracks its `main` branch through an automated weekly bump that must pass the tests and the replay corpus, and every install can update with one command *(revised — agreed, from research)* | WhatsApp retired client versions on 14 Sep and 5 Oct 2026, and pinned builds went dark within minutes |
| NFR-6 | Portability | One pure-Go binary per platform (SQLite through `modernc.org/sqlite`, no C compiler) with the UI built in; Python needed only on machines that run Python modules *(revised — agreed, from research)* | Easy to run, cross-compile and publish |
| NFR-7 | Responsiveness | UI status updates within 2 seconds (SC-5) | The live views are a core feature |
| NFR-8 | Resilience | A panic in an event handler or media worker is caught, logged and counted, never fatal; exactly one connection writes to the archive; logs are structured JSON sent to the OS log (journald on Linux), which handles rotation *(agreed, from research)* | A crash loses everything that arrives while the process is down |
| NFR-9 | Enforced rules | Architecture rules are checked by tools, not only stated in `CLAUDE.md`: import and send bans in lint, race and goroutine-leak checks in tests, hooks that run the checks and protect test files *(agreed, from research)* | Practitioners report `CLAUDE.md` is followed roughly 70–80% of the time; every line is written by agents |

## 9. Technical direction

A Go core with the UI built in, SQLite on local disk, and modules as separate programs, mostly Python. All *agreed*; see ADR-2 to ADR-5, ADR-7 and ADR-15 to ADR-26.

| Layer | Choice | Why |
| --- | --- | --- |
| WhatsApp client | whatsmeow (Go, MPL-2.0), tracking `main`, wrapped by `internal/wa` | Maintained by the mautrix-whatsapp author; no existing project is receive-only, so a thin own core is justified; openclaw/wacli (MIT) is the nearest prior art to borrow from (ADR-21) |
| Core | Go, one process: `wa`, `ingest`, `store`, `media`, `jobs`, `modules`, `web`, `mcp`, `health`, `backup` | One process, one binary |
| Storage | SQLite (WAL) via `modernc.org/sqlite`, pure Go; whatsmeow's session store opened with `_pragma=foreign_keys(1)`; one write connection; media folder on local disk | No C compiler; avoids the whatsmeow #454 foreign-key pitfall |
| Search | A `search_text` column normalised in Go, indexed with FTS5 `unicode61` and `trigram` (confirmed by spike 8 before phase 3) | Deterministic Arabic folding that unit tests can pin down |
| Jobs | One `jobs` table with leases, hand-rolled; `maragu.dev/goqite` v0.4.0 as the fallback | Job completion and artifact writes happen in one transaction |
| UI | Server-rendered Go templates (`html/template` or templ) with htmx; live updates over Server-Sent Events | One language, no JavaScript toolchain, testable with `httptest` |
| Modules | Any language; Python by default (Whisper, OCR, model SDKs); one process per job, JSON in and out | The ML ecosystem lives in Python |
| Agent access | MCP server, read-only, in the same binary | Hermes and Claude both speak MCP |
| Deployment | One process per machine; GoReleaser builds for macOS, Linux and Windows; systemd on Linux, launchd on macOS; a Docker image | CON-4 |
| Updates | Weekly automated whatsmeow bump in CI; client-version check at startup and after a rejection | NFR-5, FR-26 |

**Repo layout (intended):**

```
whatsapp-archiver/
  go.mod               # Go module at the repo root
  cmd/archiver/        # the one binary
  internal/
    wa/                # the only importer of the whatsmeow client; receive-only surface
    ingest/            # normaliser, scope filter, the single ingest path, history ledger, record/replay
    store/             # archive.db: migrations, insert-only raw layer, read views
    media/  jobs/  modules/  web/  mcp/  health/  backup/  config/  retry/  guard/  testkit/
  modules/
    _template/         # working example module to copy
    transcribe/
    translate/
  testdata/events/     # recorded, scrubbed WhatsApp events for replay tests (from a test group only)
  docs/                # this charter, MODULES.md, prior-art.md, guides
  .claude/             # hooks and skills (bump-whatsmeow, add-event-kind, record-fixtures, add-module)
  .golangci.yml        # import and send bans
  data/                # session, database, media, keys (gitignored)
  CLAUDE.md            # under 150 lines: commands, invariants, package boundaries
```

**Conventions:** all config editable through the UI and stored in the database from phase 2 (a YAML file before that); secrets only in `data/`; the module contract in `docs/MODULES.md` is the source of truth for module work; the engineering principles in the phase 0 plan bind every later phase.

## 9b. Design: where things run

In v1 the whole app runs as one process on one machine. The job queue sits behind an internal interface, so remote workers can be added later as an adapter rather than a rewrite. *(agreed, from research; replaces v1.0's hub, capture and worker roles, ADR-8)*

**Why the role split was dropped:** Hamza runs one always-on Ryzen server with no GPU that matters, so routing work by capability buys nothing today. Buffering on a separate capture node duplicates what SQLite already guarantees. Node tokens, capability routing and an embedded Tailscale node each add failure modes to the one component that must not lose messages.

**The seam that keeps the option open:**

- `internal/jobs` exposes a `JobSource` interface with three calls: lease a job, complete it (with its artifacts), fail it. The module runner talks only to that interface.
- A later remote worker is an HTTP adapter over the same three calls, plus a media-fetch endpoint. Capture and storage don't change.
- What would trigger it: a real second machine running modules (for example an Apple Silicon Mac for Whisper speed). Record the decision in the log when it happens.

**Remote access:** run Tailscale (or Tailscale Serve, for HTTPS) on the host; the guide covers it. The dashboard asks for a login whenever it isn't opened from the host itself.

**Packaging:** native builds for macOS, Linux and Windows, plus a Docker image. Install steps never assume a particular machine.

## 10. Design: the module contract

Every module receives the same envelope, returns the same artifact shape, and describes itself in one manifest; the core never changes when a module is added. *(agreed in principle; v1.1 details agreed, from research)*

**Folder:** `modules/<name>/` holds `module.toml`, the module's code and a `fixtures/` folder.

**Envelope (one JSON object on stdin per job):**

| Field | Contents |
| --- | --- |
| job_id | The job being run, for logs |
| message | id, chat (id, name, course tag), sender (id, name, role), timestamp, original text, quoted message id, forwarded flag |
| media | type, MIME type, local file path (read-only), size, duration or page count; empty when there is no file |
| artifacts | everything other modules have already produced for this message |
| settings | this module's settings for this chat (e.g. target language) |

**Batch input:** a module whose manifest sets `batch` receives `{"jobs": [envelope, ...]}` and returns `{"results": [...]}` in the same order, so a heavy model loads once per batch.

**Result (one JSON object on stdout):** `{"artifacts": [...]}`, each artifact with a `kind` (e.g. `transcript`, `translation`, `ocr_text`, `label`, `event`), a language, text, and optional structured data. Exit code 0 means success. Any other exit code means a retryable failure, with stderr kept as the error. `{"error": "...", "retry": false}` on stdout means a permanent failure. The core stores all artifacts in one generic table, so new modules never need new tables.

**Manifest (`module.toml`):**

| Field | Purpose |
| --- | --- |
| name, version, label, icon, description | Identity and UI display; bumping version enables re-processing |
| triggers | What wakes it: message type, MIME type, or an artifact kind another module produces |
| produces | Artifact kinds it outputs |
| settings | Typed fields (text, number, select, multiselect, toggle); the UI form is generated from these |
| requires_env | API keys it needs; the UI asks for them |
| needs | What must be installed (Python version, ffmpeg, a local model); checked by `doctor`, not used for routing |
| command | Command to run it, any language |
| timeout | Per-job time limit; a job over it is killed and retried |
| batch | Optional maximum batch size |

v1.0's `mode` field (`per_job` or a long-running `service`) and needs-based routing to workers are removed. *(agreed, from research)* Model warm-up was the only reason for service mode, and batch input gives most of the gain; service mode can return later behind the same JSON schema.

**Chaining:** triggers on artifact kinds let modules feed each other. Transcribe produces `transcript`; translate triggers on message text, `transcript` and `ocr_text`; classify triggers on `translation`; extract-events triggers on certain labels. Adding OCR later means translate picks up PDF text with no change.

**What the core guarantees:** jobs are unique on target + module + module version, so nothing runs twice; a job is leased with an expiry, so a crashed module's job returns to the queue; retries use exponential backoff and keep the error; the artifacts and the job's completion are written in one transaction; every status change is pushed to the UI; versioned re-runs work over the whole archive; raw data is never touched.

**Making it Claude-friendly:** `modules/_template/`; a `fixtures/` folder of input and expected-output pairs in every module; `archiver module test <dir>` runs a module against its fixtures (or a real message) and validates the output, which gives Claude Code a closed test loop; `docs/MODULES.md`, `CLAUDE.md` and an `add-module` skill state the contract and the rule never to edit core for a module.

## 11. Design: data model

The database has a raw layer that only capture writes, a derived layer that only modules write (plus a search index the core rebuilds from raw and derived data), and a control layer for settings and jobs. Field-level schema lives in the phase plans. *(agreed; v1.1 additions agreed, from research)*

| Layer | Entity | Holds |
| --- | --- | --- |
| Raw | chats | Chat JID as received, name, type (DM, group, community), parent community; WhatsApp metadata, which may update. User-set fields (logging state, course tag) live alongside as control columns |
| Raw | participants | JID as received (LID or phone), display name; user-set role (staff, class rep, student) as a control column |
| Raw | id_mappings | LID ↔ phone-number pairs as WhatsApp reveals them, with source and first-seen time; insert-only *(new)* |
| Raw | messages | Id, chat and sender JID as received, WhatsApp timestamp (UTC, stored as given) and time received, original text, quoted id, forwarded flag, media type, source (live or history), raw protobuf |
| Raw | message_events | Edits, deletions, pins, reactions, poll votes and decryption gaps, each with time, new value and raw protobuf |
| Raw | chat_events | Group info and participant changes; app-state changes such as archive, mute and pin chat *(new)* |
| Raw | history_sync_chunks | Every history-sync chunk: type, order, progress, counts, oldest and newest timestamp, time received *(new)* |
| Raw | media | File path, MIME type, size, hash; download state, attempts and reason are control data and may change |
| Derived | artifacts | Message, module, module version, kind, language, text, structured data, created time |
| Derived | search_index | Normalised text of messages and artifacts with FTS5 indexes; rebuildable at any time *(new)* |
| Control | modules | Installed modules, enabled state, global settings, API key references |
| Control | chat_module_settings | Which modules run on which chat, with per-chat settings |
| Control | jobs | Target, module, version, status (queued, running, done, failed, skipped), attempts, lease expiry, next run time, error, timings, cost |
| Control | pending_buffer | Messages from groups awaiting approval, with expiry time |
| Control | backups | Schedule, destination, and each run's time, size and result |
| Control | notification_channels | Configured channels and which events each one receives |
| Control | storage_stats | Periodic size snapshots by chat and type, used for growth and projections |

v1.0's `nodes` table is removed with the role split.

**Read views:** `chat_aliases` maps the LID and phone forms of a chat to one canonical id (LID preferred, as mautrix-whatsapp has done since August 2026); `messages_resolved` shows a message once even if it arrived under both forms. Raw rows are never rewritten (CON-7).

The `jobs` table drives every live view in the UI. Rebuilding derived data means deleting artifacts for a module and re-queuing its jobs; raw tables are never touched.

## 12. Design: UI screens and live views

The UI is a local web dashboard with eight screens, rendered by the Go server with htmx and updated live over Server-Sent Events; simplicity and seeing what is happening are the two priorities. *(agreed; rendering approach and removal of the Nodes screen agreed, from research)*

| Screen | What it does |
| --- | --- |
| Dashboard | Connection status, chats logging, queue size; every enabled chat with its pipeline strip; live activity feed |
| Chats | Every chat, group and community with toggles for logging, file types, and each module; pending tray for new groups; community expand |
| Modules | Installed modules from their manifests, each with an enable switch, generated settings form, API key entry, and a re-process button when the version changes |
| Queue | Jobs by stage and status, throughput, failed jobs with error text and retry or skip |
| Archive | Browse and search by chat, sender, date and text; open a message to see its trace: original, each module's output, timings, cost |
| Settings | Pairing and re-pairing QR, media caps, storage paths, health thresholds, MCP on or off |
| Storage | Space used in total and broken down by chat, media type, database, models and backups; growth per week; projected days until the disk is full; tools to free space (FR-25) |
| Backups and alerts | Backup schedule, destination, last result and restore (FR-23); notification channels and which events alert the phone (FR-24) |

**Live views (FR-9):**

- **Pipeline strip per chat:** one pill per enabled stage, showing count and state (done, running, queued, failed, off). Clicking a pill opens that chat's jobs at that stage.
- **Activity feed:** rolling log of every event and job change, filterable by chat and stage.
- **Message trace:** step-by-step view of everything that happened to one message.
- **History-sync progress:** the chunk ledger and "complete back to <date>" per chat, during first pairing and after.
- **Health panel:** connection state, WhatsApp client version in use, session age, time since last event, time since the phone was last active, decryption gaps, disk use, and the inactivity-logout warning.

A first mockup of the dashboard was shown in chat on 13 Oct 2026; the visual design is left to the builder.

## 12b. First run and user journeys

A new user should go from install to recording their first chat in about 15 minutes, guided by a wizard rather than by separate screens. *(agreed)*

**First-run wizard (FR-16):**

1. **Welcome and password.** Create the UI login. Show where the app is storing data and how much free space there is.
2. **Pair WhatsApp.** QR code with phone instructions (WhatsApp → Linked Devices → Link a Device). Confirm with "Connected as +…". History starts arriving straight away and keeps arriving for hours.
3. **Choose chats.** Log everything (the default) or only selected chats, and exclude any chat; search, a "suggested" strip, communities that enable all their groups at once, and bulk settings (FR-18). An estimate of the storage needed is shown before confirming (FR-25).
4. **Choose processing.** For each module the user turns on, pick local or API and complete its setup right there (section 12c). No module runs until its setup passes a test.
5. **Remote access (optional).** A link to the Tailscale guide (section 12d).
6. **Done.** Land on the dashboard with the backlog import already running.

**Everyday use:** nothing to do. New groups appear as new (or pending, in selected mode) with an approve button; the archive is searchable in the original language and in translation; agents read through MCP if enabled. When WhatsApp retires the client version, the phone gets an alert telling the user to update.

**Adding a module:** Claude Code creates it under `modules/`; after a restart it appears on the Modules screen and as a toggle on every chat; "Run on existing messages" backfills it.

**Adding a machine (later):** when remote workers exist (section 9b), a second machine runs a worker that leases jobs over the network. Not part of v1.

**Recovery (FR-19):** re-pairing is a first-class flow, both for unexpected unlinks and for moving capture to another machine. It always starts a fresh pairing; a session file is never copied or restored, because a stale one breaks encryption sessions. The health banner warns before WhatsApp's inactivity unlink, and failed jobs show their error with a retry button.

## 12c. Processing engines: local or API

Transcription and translation each let the user choose an engine: a model running on their own hardware, or a provider's API with their own key. The UI guides the choice and tests it before any jobs run. *(agreed)*

The engine is a setting of the module, not a separate module: the transcribe module contains adapters for each engine, so chaining and the rest of the contract stay the same.

**One adapter for most APIs, with OpenRouter as the default:** API engines use a single "OpenAI-compatible" adapter configured by a base URL, a key and a model name. That one adapter covers OpenRouter, OpenAI, Groq, and local servers such as Ollama that speak the same format. OpenRouter is the suggested default: one key and one balance reach hundreds of models, including transcription models through its speech-to-text endpoint, so switching model is a dropdown change in the UI, which lists available models live from OpenRouter. OpenRouter passes model prices through without markup and charges 5.5% when credits are bought (minimum $0.80), and unused credits can expire after a year, so small top-ups cost proportionally more. Users who prefer to pay a provider directly enter that provider's URL and key instead. For privacy, the OpenRouter settings let users restrict routing to providers that don't retain prompts. *(agreed)*

**Engine options (checked 13 Oct 2026; prices change, so the UI links to each provider's pricing page):**

| Task | Engine | Type | Runs best on | Notes |
| --- | --- | --- | --- | --- |
| Transcription | whisper.cpp | Local | Apple Silicon Macs (uses the Mac's GPU); also CPU | Easy single install; good default on a Mac |
| Transcription | faster-whisper | Local | NVIDIA GPU; CPU works but is slow with large models | Good default on Linux and Windows |
| Transcription | Cohere Transcribe Arabic | Local, or Cohere API | GPU recommended | Open weights (Apache 2.0), 2B parameters, all major Arabic dialects plus Arabic-accented English. Lowest word error rate on the Open Universal Arabic ASR leaderboard: 25.87 vs Whisper large-v3's 36.86 (Jul 2026). Hosted API has free rate-limited access |
| Transcription | Whisper large-v3 fine-tuned on Arabic dialects | Local | GPU recommended | Community models; reportedly far better than base Whisper on Gulf Arabic |
| Transcription | OpenAI transcription API | API | Anywhere | $0.003 per minute (gpt-4o-mini-transcribe) to $0.006 per minute (gpt-4o-transcribe) |
| Transcription | Groq (hosted Whisper) | API | Anywhere | $0.04 per hour (large-v3-turbo) or $0.111 per hour (large-v3); free tier with daily limits |
| Transcription | Other providers (ElevenLabs, Deepgram) | API | Anywhere | Adapters can be added later |
| Translation | Ollama with a multilingual model | Local | A machine with enough RAM or a GPU | Free, private; quality depends on the model chosen |
| Translation | NLLB-200 | Local | CPU or GPU | Dedicated translation model; weaker on informal dialect |
| Translation | Claude, OpenAI or Gemini API | API | Anywhere | Paid per token; handles informal Arabic and context well; can also classify in the same call |
| Translation | DeepL or Google Translate API | API | Anywhere | Google Cloud Translation: $20 per million characters, first 500,000 characters a month free; DeepL priced per character too. Literal translation, no classification |

Whisper comes in sizes from tiny to large; larger sizes are much better at Arabic and at dialect, and much slower without a GPU. The UI recommends a size based on the machine's hardware.

**What the research found about Arabic quality (14 Oct 2026):** off-the-shelf Whisper gets roughly one word in three wrong on Saudi dialect. arXiv 2504.12254 (Apr 2025) reports an average word error rate of 36.86 for large-v3 and 40.05 for large-v3-turbo; NADI 2025 and the SADA studies show dialect fine-tuning helps but still leaves 30–40% error on Saudi speech. So: *(agreed, from research)*

- On a CPU-only machine the default local engine is Whisper large-v3 (not turbo) through faster-whisper or whisper.cpp with int8, until the bake-off (spike 6) on 30 of Hamza's real voice notes decides otherwise. If the local real-time factor is above 1.0 or its error rate is much worse than an API's, the API becomes the default.
- Transcripts are labelled as machine output and always link to the original audio; the app never presents them as authoritative.
- Translation defaults to an LLM API through OpenRouter (Q-3), confirmed by a blind ranking of 50 informal messages (spike 7).

**Guidance in the UI when a user turns a module on:**

- **A choice card per engine** showing cost (free or paid), privacy (stays on your machine or is sent to the provider), expected speed on *this* user's machine, and a one-line quality note.
- **Local:** shows the model's download size and disk space before downloading; the app downloads and manages it; a progress bar shows the download.
- **API:** a key field with a link to where to get that provider's key, a short explanation of what a key is, and an optional monthly spending cap after which jobs pause.
- **Test before use:** a "Test" button runs the engine on a built-in Arabic sample voice note or message and shows the output and time taken. Jobs only start after a passing test.
- **Switch any time:** changing engine never deletes existing output; the user can re-run old messages with the new engine.

**Choose with real audio:** the Test screen also accepts the user's own samples. Before committing, the user can run two or three candidate engines on about 10 real voice notes from their own chats and compare the results side by side; dialect quality varies too much for a general ranking to decide.

## 12d. Setup profiles

The installer asks one question, "where should this run?", and sets up one of three profiles; all use the same single-process binary. *(agreed; profile C moved to later and profile D changed — agreed, from research)*

| Profile | Who it suits | What runs where | Main trade-off |
| --- | --- | --- | --- |
| A. Main computer | Trying it out; light use; one laptop or desktop (macOS, Windows or Linux) | Everything on one machine | Only records while the computer is on and awake; uses its disk |
| B. Home server | Reliable 24/7 capture; not enough space on the main computer (Hamza's case) | Everything on an always-on Linux machine; dashboard opened from other devices | Needs a spare machine and a short Linux setup |
| D. Any of the above + remote access | Wants the dashboard on phone, tablet and other computers, at home or away | Adds Tailscale on the host | Needs a free Tailscale account |
| C. Split (later) | Server for capture and storage, plus a stronger machine for local models | Waits for remote workers (section 9b) | Not in v1 |

**Profile A on a Mac or Windows PC:** an installer package; the app starts at login and lives in the menu bar or system tray; the data folder can point to an external drive. The wizard warns that recording stops when the computer sleeps and offers to keep it awake while plugged in.

**Profile B for users new to Linux (README walkthrough, no prior knowledge assumed):**

1. **Hardware:** any spare PC, old laptop or mini PC that can stay plugged in, with enough disk for the user's media.
2. **Install Linux:** Ubuntu Server 26.04 LTS, the current long-term support release, supported until April 2031. Download it, write it to a USB stick with a tool like balenaEtcher, boot from it, and follow the installer, ticking "Install OpenSSH server".
3. **Connect from the main computer** over SSH, with screenshots for macOS and Windows.
4. **Run the one-line installer.** It downloads the release for the machine, creates a service that starts on boot and restarts on failure, offers automatic security updates, installs Python and ffmpeg only when a chosen module needs them, and prints the setup link.
5. **Open the setup link** from the main computer's browser and follow the wizard.

**A doctor command** checks everything at any time (disk space, service running, WhatsApp connected, client version current, module needs installed, engines tested) and explains each problem in plain words with the fix.

**Remote access with Tailscale (profile D, FR-21):** the user installs Tailscale on the host with its official one-line installer and signs in; optionally `tailscale serve` gives the dashboard a private HTTPS address. The dashboard is then reachable from any of their devices running Tailscale. Without Tailscale, the dashboard is available on the home network only. *(agreed, from research; replaces the built-in Tailscale node of ADR-10)*

**Storage:** the user picks the data folder during setup; the UI shows disk use, growth per week and a warning before space runs out. Media download rules (FR-5) are the main lever.

## 12e. README and docs requirements

The GitHub README must get a basic user from nothing to a working setup without outside help, while letting an advanced user skip to the parts they need. *(agreed)*

**Structure:**

1. **What it is, in three sentences,** plus a dashboard screenshot.
2. **Read this first:** it is an unofficial WhatsApp client with a small ban risk; it never sends messages; your data stays on your machines unless you turn on an API engine; it depends on staying updated, because WhatsApp retires old client versions every few weeks, so keep update alerts on and update when told; and your archive holds other people's messages, so keep it and anything made from it for your own use. In Saudi Arabia the PDPL exempts personal or family use only while data isn't disclosed beyond that circle; check the law where you live.
3. **Pick your setup:** the profiles from section 12d as a short decision guide ("Is your computer on all the time? Do you have a spare machine?").
4. **Step-by-step guide per profile,** each self-contained, with screenshots, expected output after every step, and "if this fails" notes.
5. **First-run wizard walkthrough.**
6. **Choosing engines:** local versus API in plain words, what an API key is and where to get one, rough costs, privacy, and how good Arabic transcription really is.
7. **Remote access with Tailscale on the host,** from creating an account to opening the dashboard on a phone.
8. **Updating, backing up and moving to another machine** (moving always means re-pairing).
9. **Troubleshooting,** organised by symptom, starting with the doctor command.
10. **For developers:** architecture, the module contract, and how to add a module with Claude Code.

**Rules:**

- **Assume nothing:** explain every term the first time it appears (terminal, SSH, API key, Tailscale), with a glossary at the end.
- **Every command is copy-paste ready** and says which machine to run it on.
- **Advanced users can skip:** a short "quick start" for each profile at the top, with Docker Compose and config-file options in a separate section.
- **Tested before release:** each profile's guide is followed from a fresh machine before each release (SC-8).

## 13. Build plan

Five phases, each leaving a working app. Phase 0 starts capturing straight away, because messages missed now can't be recovered later, while every module can be re-run over the archive at any time. Modules come before the UI, because the module contract is the riskiest design to get wrong and capture keeps running underneath either way. *(agreed: capture first; phase order and contents agreed, from research)*

| Phase | Goal | Delivers | Main work | Exit criterion |
| --- | --- | --- | --- | --- |
| 0 | Capture first, hardened | FR-4, FR-5, FR-6, FR-26; FR-23 and FR-24 in basic form | Prior-art intake (wacli, mautrix-whatsapp) turned into tests; guardrails (lint bans, hooks, CI, weekly whatsmeow bump); receive-only `wa` wrapper; full-history pairing with explicit limits and a chunk ledger; insert-only store with LID mappings, decryption-gap events and chat events; media queue (live first, rate-limited re-requests); nightly snapshots; health states and ntfy alerts including client outdated; record and replay harness; Linux service; CLI `pair`, `run`, `status`, `chats`, `backup`; repo skeleton, `CLAUDE.md`, `docs/MODULES.md` stub, `.gitignore` for `data/` | Spikes 1 to 5 run and recorded; oldest recovered message checked per group; 7-day SC-1 and SC-10 audit passes with zero missing |
| 1 | Jobs, modules and first engines | FR-7, FR-8, FR-12; FR-17 engine adapters and test command (no UI yet) | Module contract written into `docs/MODULES.md` (section 10); jobs table with leases and the `JobSource` seam; manifest loader; per-job runner with batch input; `archiver module test`; `_template`; transcribe and translate built as ordinary modules, configured from the config file | Contract approved by Hamza; SC-6 holds (translate added only under `modules/`); spikes 6 and 7 pick the default engines |
| 2 | Control UI and live views | FR-1, FR-2, FR-3, FR-5 per-chat rules, FR-9, FR-18, FR-25; UI for FR-17, FR-23, FR-24 | Go templates + htmx + SSE: dashboard, chats, modules, queue, settings, storage, backups and alerts; QR pairing in the browser; settings move from the config file into the database | SC-2, SC-4 and SC-5 hold; capture runs uninterrupted through the switch from config file to UI |
| 3 | Archive search and MCP | FR-10, FR-11 | Arabic normaliser and FTS5 index (after spike 8), archive browse, search and message trace; read-only MCP server; connect Hermes and run a test digest | Hermes answers "what changed in anatomy this week?" from the archive; an Arabic word typed without diacritics finds its message |
| 4 | Setup and release | FR-16, FR-19, FR-20, FR-21, FR-22 | First-run wizard, re-pair flow, GoReleaser pipeline, Homebrew tap, Docker image, Linux and macOS service installers, doctor command, remote-access guide, README and guides with screenshots | SC-8 holds for profiles A and B |
| Later | Extras | FR-13 to FR-15 and more | OCR, classification, exports, usage meter; Telegram and email alert channels; remote workers over `JobSource`; semantic search (sqlite-vec); import from phone backups (WhatsApp-Chat-Exporter, `source=backup`); continuous replication (Litestream); Windows service wrapper; database and media upload pipeline | — |

Phase 0 pairs once against the real account and exercises a throwaway test group on the first day; capture keeps running through every later phase. Each phase gets its own implementation plan in `docs/superpowers/plans/`, written just before it starts.

## 13b. Spikes

Short experiments, a day or less each, that settle questions the research could not. Each is run at the phase gate shown and its result recorded in that phase's acceptance log. *(agreed, from research)*

| # | Spike | When | What to measure | What would change the decision |
| --- | --- | --- | --- | --- |
| 1 | History reach | Phase 0 acceptance | Pair with full sync (3,650 days, 4,096 MB) against the real phone and keep running 48 hours; oldest message per course group; time until progress reaches 100%; chunks that arrive after restarts | If course groups reach the start of term, drop backup import from the later list |
| 2 | iOS vs Android | Phase 0 acceptance (the phone is an iPhone, Q-7) | Compare spike 1's results, which come from the iPhone, with the Android data in the research (90 days on an ordinary pairing; about 22 months on full sync, or 3 years with a raised size limit) | If iOS stops much sooner, raise backup import in priority |
| 3 | Version-drift drill | Phase 0 acceptance | Force a retired client version through the override with the version check off; then turn the check back on | If the alert, the `client_outdated` state or the recovery fails, fix before real use; if the version endpoint fails, ship update instructions instead |
| 4 | Receipt audit | Phase 0 acceptance | With a second phone in the test group and Hamza's phone offline for 30 minutes, confirm no "online" status, no read receipts and no visible delivery ticks come from the archiver over 24 hours | Any visible receipt halts phase 1 until explained |
| 5 | LID dedup | Phase 0 acceptance, after a re-pair | Count `(canonical chat, message id)` pairs that appear twice in raw rows, and check `messages_resolved` shows each once | A duplicate in the view means the dedup key changes before phase 1 |
| 6 | ASR bake-off | Before phase 1 picks defaults | 30 real Saudi voice notes through faster-whisper large-v3 (int8, CPU), large-v3-turbo and one hosted API; manual word error rate on 10; real-time factor on the Ryzen; cost | Local real-time factor above 1.0, or much worse accuracy, makes the API the default |
| 7 | Translation bake-off | Phase 1 | 50 informal messages through an LLM API and a CPU-local model, ranked blind by a native speaker | Decides the default translation engine |
| 8 | modernc FTS5 + trigram | Before phase 3 | The modernc build has FTS5 and the trigram tokenizer, and normalised Arabic queries hit | If not, switch to CGO `mattn/go-sqlite3` with `sqlite_fts5` and accept per-platform builds |

## 14. Risks

The most likely failure is no longer a ban but WhatsApp retiring the client version; the most damaging is still a restriction on Hamza's own number.

| ID | Risk | Likelihood | Impact | Mitigation |
| --- | --- | --- | --- | --- |
| RISK-1 | WhatsApp restricts or bans the number for using an unofficial client | low | high | Never send (CON-1); never set the device online, so delivery receipts stay inactive; accept the residual risk knowingly. Documented whatsmeow bans are about sending; warnings have been seen for reply-only clients (whatsmeow #810); no case of a receive-only device was found, so the risk is unproven either way. If WhatsApp shows any restriction notice (a temporary ban event), alert the user and pause capture (Meta has blocked linked-device sessions for users of an archiving service before) |
| RISK-2 | WhatsApp retires the client version or changes the protocol, and the bridge stops connecting | high *(raised — research)* | high | FR-26: version check at startup and after a rejection, `client_outdated` alert, weekly whatsmeow bump with tests and replay, one-command update (NFR-5). Cut-offs hit on 14 Sep and 5 Oct 2026 |
| RISK-3 | Linked device logged out after the phone is inactive about 14 days (and after 30 days of device inactivity) | med | med | Phone-quiet alert at 10 days (NFR-3); always-on capture avoids the 30-day rule |
| RISK-4 | Messages missed while capture is offline beyond WhatsApp's redelivery window | low | med | Service auto-restart; panics never fatal (NFR-8); uptime shown in the health panel |
| RISK-5 | Media links expire before download | med | med | Download on arrival, live before backlog; expired media re-requested at a limited rate; failures shown in status |
| RISK-6 | Third-party APIs see classmates' messages | med | low | Modules off by default; per-chat control; local options (e.g. local Whisper) where practical |
| RISK-7 | Scope creep into digests, agent logic or a hosted product | med | med | Non-goals in section 5; those live in Hermes or a separate project |
| RISK-8 | Silent gaps from messages the device can't decrypt | med | high | Gaps stored as events, phone asked to resend, counted in status and audited (SC-10) *(new — research)* |
| RISK-9 | LID and phone-number forms split one conversation in two, or duplicate it after a re-pair | med | med | CON-7, mapping table, alias-aware dedup and read views; spike 5 *(new — research)* |
| RISK-10 | History is shorter than hoped: a personal phone refuses on-demand full history, and sync reaches about three years at most | high | med | Request full sync with explicit limits at pairing; chunk ledger; spikes 1 and 2 (the phone is an iPhone, and iOS history behaviour is undocumented); backup import kept as a later option *(new — research)* |
| RISK-11 | Arabic transcription is wrong often enough to mislead (roughly one word in three for base Whisper on Saudi dialect) | high | med | Bake-off (spike 6); transcripts labelled and linked to audio; never presented as authoritative *(new — research)* |
| RISK-12 | A public release makes Hamza the maintainer of every install: if updates stop, every copy stops capturing within weeks | med | high | Weekly automated bump; update alert in the app; README says plainly that it must be kept updated *(new — research)* |
| RISK-13 | Agents quietly weaken tests or rules while building | med | high | Separate test and implementation subagents, hooks that protect test files, lint-enforced rules, golden files updated only by hand (NFR-9) *(new — research)* |

## 15. Assumptions and open questions

Five assumptions let building start now; nothing still open blocks phase 0.

**Assumptions:**

| ID | Assumption | If wrong |
| --- | --- | --- |
| AS-1 | The app links to Hamza's personal number, since that is the only account in the BMC groups | A second number can't see the groups; the design still holds |
| AS-2 | At least one machine Hamza owns can stay on 24/7 to run the app | Messages can be missed during downtime beyond WhatsApp's redelivery window |
| AS-3 | "WhatsApp Archiver" is a working name | Rename only |
| AS-4 | English is the default translation target; Arabic is the main source language | Only module settings change |
| AS-5 | Hamza will build with Claude Code, phase by phase, from this document | Plan granularity may need adjusting |

**Open questions:**

| ID | Question | Blocking? | Status |
| --- | --- | --- | --- |
| Q-1 | How should nodes reach the hub across networks? | — | superseded: no nodes in v1; remote access through Tailscale on the host (ADR-16, ADR-17) |
| Q-2 | For Hamza's own setup: which transcription engine to start with? | phase 1 | answered: local Whisper to start, switchable any time; spike 6 confirms or overturns it |
| Q-3 | Which model provider and budget for translation? | phase 1 | answered: an LLM API (through OpenRouter); spike 7 confirms |
| Q-4 | Should DMs from staff be logged, or only groups? | — | answered: log everything by default; the app is not only for university chats |
| Q-5 | Confirm the current engine list, model names, Arabic quality and pricing in section 12c | — | resolved: engines and prices checked 13 Oct 2026; Arabic quality findings added 14 Oct |
| Q-6 | Minimum and recommended hardware for each profile (RAM, disk, CPU), measured on real installs, for the README | phase 4 | deferred |
| Q-7 | Is Hamza's phone Android or iPhone? History sync behaves differently and only the Android behaviour is documented | — | answered: iPhone, so spike 2 runs in phase 0 |
| Q-8 | Which licence for the public repo? The research assumed MIT; whatsmeow (MPL-2.0) and code adapted from wacli (MIT) both fit; nothing may be copied from mautrix-whatsapp (AGPL) | phase 4 | deferred: no licence for now; decide before the repo is made public. Until then no one else may reuse the code, and code adapted from wacli still keeps its MIT notice |

## 16. Decision log

These decisions came out of the 13 Oct 2026 design conversation and the 14 Oct design research, and should not be reopened without a reason. Superseded decisions stay in the log for the record.

| ID | Decision | Why | Rejected | Status |
| --- | --- | --- | --- | --- |
| ADR-1 | Separate capture from intelligence: the archiver records and processes; Hermes or Claude reads it through MCP | Hermes's WhatsApp gateway replies in groups and can't listen passively | Running Hermes directly on WhatsApp | agreed |
| ADR-2 | Build a standalone app with a UI rather than a config-file daemon | Simple to use and navigate | Fork lharries/whatsapp-mcp and edit config by hand | agreed |
| ADR-3 | Never send; no send code in the build | Can't post into groups by mistake; lower ban risk | Disabling send by config only | superseded by ADR-15 |
| ADR-4 | Raw data immutable; all processing writes to a generic artifacts table | Safe re-processing; modules need no schema changes | Per-module tables; editing messages in place | agreed |
| ADR-5 | Modules are separate programs with a manifest and JSON in and out | Any language, mainly Python; Claude can add one without touching core | Compiled-in Go plugins | agreed; refined by ADR-18 |
| ADR-6 | Local storage only in v1 | Live SQLite in a sync folder corrupts; upload pipeline later | Google Drive as primary store | agreed |
| ADR-7 | Go core on whatsmeow | Mature library, single binary | Node with Baileys | agreed |
| ADR-8 | Split into roles (hub, capture, worker) that talk only over the hub API; where each runs is configuration | The app must not assume any particular machine; heavy modules can move to better hardware later | One process tied to one machine; roles sharing the database file directly | superseded by ADR-16 |
| ADR-9 | Engines (local or API) are settings inside a module, with adapters, not separate modules | Switching engine never changes chaining or stored output kinds | One module per engine | agreed |
| ADR-10 | Remote access through Tailscale built into the hub, optional; home network only by default | Private access from any device without opening router ports or a separate install | Port forwarding; a public cloud relay | superseded by ADR-17 |
| ADR-11 | Recommend Ubuntu Server 26.04 LTS for the home-server profile | Current long-term support release, supported to April 2031, widely documented for beginners | Debian, Fedora (supported, not the guided default) | agreed |
| ADR-12 | Public-release quality is the design standard for v1, though Hamza expects to be the only user; delivery time is not a constraint. Capture starts first, and backups and phone alerts are v1 requirements | Designing for strangers forces easy setup and configuration; capture can't be recovered later but processing can | Council review's split into capture-only, personal v1 and public v2 (13 Oct 2026) | agreed |
| ADR-13 | Log everything by default, with exclusions; selected-chats mode stays available. Processing modules remain opt-in per chat | The archive serves more than university use; opt-in processing keeps personal chats away from API providers unless chosen | Allow-list only | agreed |
| ADR-14 | API engines use one OpenAI-compatible adapter (base URL, key, model); OpenRouter is the suggested default for both translation and transcription | Models become interchangeable from a dropdown; one adapter also covers OpenAI, Groq and local Ollama | A separate adapter per provider | agreed |
| ADR-15 | Never send anything a contact can see. whatsmeow's own protocol traffic is accepted. Enforced by one wrapper package (`internal/wa`), lint bans on send methods and the client import, a source-scan test, and a runtime receipt audit (spike 4) | whatsmeow sends acks, inactive delivery receipts, retry receipts, key uploads and peer messages to the user's own phone by itself, so "no send code in the build" can't be met; what can be proven is no user-visible sends and no presence | ADR-3's literal wording; forking whatsmeow to strip its send code | agreed, from research |
| ADR-16 | One process in v1, with a `JobSource` seam so remote workers can be added later as an adapter | No current need; distributed code adds failure modes to the component that must not lose messages | ADR-8's role split in v1; capture-side buffering | agreed, from research |
| ADR-17 | Remote access through Tailscale installed on the host, documented in a guide | Same result with no networking code in the app | ADR-10's embedded tsnet node | agreed, from research |
| ADR-18 | Modules run one process per job, with optional batch input; no long-running service mode in v1 | Smallest contract an agent can implement and test from one instruction | `per_job` + `service` modes; HashiCorp go-plugin; WebAssembly (Extism); MCP as the module contract | agreed, from research |
| ADR-19 | Server-rendered Go templates with htmx and Server-Sent Events for the UI | One language, one toolchain, one test framework for seven or eight mostly tabular screens | Svelte or React single-page app; PocketBase (pre-1.0, changing fast, its collection model pulls against an insert-only schema) | agreed, from research |
| ADR-20 | `modernc.org/sqlite` (pure Go) for both databases; whatsmeow's session store opened with `_pragma=foreign_keys(1)` | Cross-compiles with no C toolchain; the DSN avoids whatsmeow #454 | CGO `mattn/go-sqlite3`, unless spike 8 shows FTS5 or trigram missing | agreed, from research |
| ADR-21 | Build an own thin core; borrow code and ideas from openclaw/wacli (MIT, with attribution); read mautrix-whatsapp for fixes but copy none of its code (AGPL); fork nothing | Every maintained project ships send code and none keeps an immutable raw layer; their fixes should not be rediscovered | Forking wacli or lharries/whatsapp-mcp; building on mautrix-whatsapp with a Matrix homeserver, WAHA, GOWA or Evolution API | agreed, from research |
| ADR-22 | Phase 0 is hardened around the 2026 failure modes: client-version drift, decryption gaps, LID mapping, history-sync ledger, panic recovery | These, not bans, are what take whatsmeow loggers offline or make them lose messages | A capture logger hardened only for reconnects and logouts | agreed, from research |
| ADR-23 | Identities stored as received, linked by a mapping table and resolved at read time (CON-7) | WhatsApp's move to LIDs finished in mid-2026; normalising on write splits or merges conversations | Normalising JIDs to phone numbers on write | agreed, from research |
| ADR-24 | Arabic search through a Go normaliser feeding FTS5 `unicode61` and `trigram`; semantic search deferred | Deterministic and unit-testable; tokenizer folding of Arabic is unverified | Relying on tokenizer options; sqlite-vec in v1 | agreed, from research |
| ADR-25 | Rules enforced mechanically: `depguard` and `forbidigo` in lint, hooks that run checks and protect tests, separate test and implementation subagents, record and replay of real events, golden files updated only by hand, `CLAUDE.md` under 150 lines with procedures in skills | `CLAUDE.md` is advisory; hooks and lint are deterministic | Rules only in `CLAUDE.md`; a grep-only no-send test | agreed, from research |
| ADR-26 | Session keys are never backed up or restored; recovery is a fresh pairing | A restored stale session file breaks encryption sessions and causes silent gaps | Snapshotting `session.db` with the archive | agreed, from research |

## 17. Glossary and changelog

| Term | Meaning |
| --- | --- |
| Linked device | A WhatsApp session paired to a phone by QR code; receives messages independently of the phone |
| whatsmeow | The Go library that implements the WhatsApp multi-device protocol |
| Capture | The part of the app that is the linked device and writes raw data (`internal/wa` plus `internal/ingest`); v1.0 called it the bridge |
| Scope filter | The check that drops messages from chats that are excluded or not switched on |
| Envelope | The standard JSON a module receives for one job |
| Artifact | One piece of module output (a transcript, a translation, a label) |
| Manifest | A module's self-description file, `module.toml` |
| Job | One module run on one message; its status drives the live UI |
| Lease | A job claimed by the runner until an expiry time; an expired lease returns the job to the queue |
| JobSource | The internal interface (lease, complete, fail) between the job queue and the module runner; the seam for later remote workers |
| LID | WhatsApp's anonymous member identity, now used for most chats, mapped to a phone number where WhatsApp reveals it |
| PN | A phone-number JID, the older identity form |
| Client version cut-off | WhatsApp rejecting an old client version ("client outdated", error 405) without warning; whatsmeow builds carry a version that expires |
| Decryption gap | A message WhatsApp delivered that the device couldn't decrypt; stored as an event until the phone resends it |
| History sync | The backlog the phone pushes to a newly linked device, in chunks that can arrive hours apart |
| History-sync chunk | One delivery of history; each is recorded in the ledger |
| Record and replay | Saving real WhatsApp events from a test group as files, then feeding them back through ingest in tests |
| MCP | Model Context Protocol; how Hermes and Claude read the archive |
| htmx | A small library that lets server-rendered HTML pages update parts of themselves |
| SSE | Server-Sent Events; the server pushes live updates to the browser |
| Engine | What actually does a module's work: a local model or a provider's API |
| Profile | A ready-made setup: main computer, home server, or either with remote access |
| Tailscale | A private network service that connects a user's own devices securely from anywhere |
| Doctor | A command that checks the install and explains any problem with its fix |
| Worker (later) | A separate machine that leases jobs over the network; not in v1 |

| Version | Date | Change |
| --- | --- | --- |
| 0.1 | 13 Oct 2026 | First draft from the design conversation |
| 0.2 | 13 Oct 2026 | Removed machine-specific assumptions; added roles and nodes (section 9b, ADR-8, Nodes screen) |
| 0.3 | 13 Oct 2026 | Added user journeys and wizard (12b), engines (12c), setup profiles and Tailscale (12d), README spec (12e), FR-16 to FR-22, SC-8, phase 6 |
| 0.4 | 13 Oct 2026 | After council review: capture-first phase 0, FR-23 backups, FR-24 phone alerts, Arabic engines added, privacy note in README, ADR-12; full scope kept |
| 0.5 | 13 Oct 2026 | Answered Q-1 to Q-6; log everything by default (ADR-13); FR-25 storage view; engine table checked and priced; consistency fixes to TL;DR, data model and screens |
| 1.0 | 14 Oct 2026 | OpenRouter as default API route through one OpenAI-compatible adapter (ADR-14); all proposed decisions agreed; charter signed off |
| 1.1 | 14 Oct 2026 | Signed off. Revised after the design research: single process with a `JobSource` seam (ADR-16) replacing the role split; Tailscale on the host (ADR-17); per-job modules with batch input (ADR-18); htmx UI (ADR-19); modernc SQLite (ADR-20); build-vs-reuse decision (ADR-21); hardened phase 0 (ADR-22); identities as received (CON-7, ADR-23); Arabic search (ADR-24); mechanical guardrails (ADR-25); no session backups (ADR-26); CON-1 and CON-4 revised; FR-4, 5, 6, 8, 10, 19 to 21, 23, 24 revised; FR-26, SC-9, SC-10, NFR-8, NFR-9 added; phases reordered to capture → modules → UI → search and MCP → release; spikes (13b); risks 8 to 13; Q-7 answered (iPhone); Q-8 deferred (no licence for now) |

## 18. Research adoption map

Where each recommendation from `docs/research/researchoutcome.md` landed. All of it was signed off with v1.1 on 14 Oct 2026.

| Research recommendation | Where it landed | Phase |
| --- | --- | --- |
| Fork nothing; mine wacli (MIT) for code and fixes; read mautrix-whatsapp (AGPL) for ideas only | ADR-21; phase 0 plan Task 0 | 0 |
| Client-version check at startup (`GetLatestVersion`), `client_outdated` state and alert, weekly whatsmeow bump in CI | FR-26, SC-9, NFR-5, RISK-2; phase 0 plan Tasks 1, 4, 9 | 0 |
| Store undecryptable messages as events; turn on re-request from the phone | FR-4, SC-10, RISK-8; phase 0 plan Tasks 3, 5 | 0 |
| Store JIDs as received; persist LID↔PN mappings; dedupe across both forms | CON-7, ADR-23, section 11 views, RISK-9; phase 0 plan Tasks 2, 3, 5, 6 | 0 |
| Full sync with explicit day and size limits; keep running after pairing; chunk ledger; "complete to date X" | FR-6, RISK-10; phase 0 plan Task 6 | 0 |
| Never call `SendPresence` or force active receipts; one wrapper package; depguard + forbidigo; keep the grep test as an extra layer | CON-1, ADR-15, ADR-25; phase 0 plan Tasks 1, 4 | 0 |
| `modernc.org/sqlite` with `_pragma=foreign_keys(1)` | ADR-20, NFR-6; phase 0 plan Tasks 2, 4 | 0 |
| Panic recovery in handlers and workers; JSON logs to journald; one writer; `-race` and goleak | NFR-8; phase 0 plan Tasks 2, 5, 7 | 0 |
| Group, participant and app-state events stored raw | FR-4, section 11 `chat_events`; phase 0 plan Tasks 3, 5 | 0 |
| Timestamps as UTC plus time received; tolerate nonsensical history timestamps | Section 11; phase 0 plan Tasks 2, 6 | 0 |
| Rate-limited media re-requests | FR-5, RISK-5; phase 0 plan Task 7 | 0 |
| Never restore `session.db` over a live pairing | ADR-26, FR-19, FR-23; phase 0 plan Task 8 | 0 |
| Record and replay harness from a test group | ADR-25; phase 0 plan Tasks 10, 12 | 0 |
| `CLAUDE.md` under ~150 lines; procedures in skills; hooks; separate test and implementation subagents; tests locked during green; hand-approved golden files | NFR-9, ADR-25, RISK-13; phase 0 plan Engineering Principles and Task 1 | 0 |
| Defer Telegram (ntfy only), incremental media backup copy, Windows disk-free check, selected-chats mode | FR-23, FR-24; phase 0 plan Global Constraints | 0, then 2 or later |
| Drop hub/capture/worker roles, node tokens and embedded Tailscale; add a `JobSource` seam | ADR-16, ADR-17, section 9b, FR-21 | 1 |
| Per-job JSON subprocess contract, `fixtures/` folder, `archiver module test`; drop service mode | ADR-18, section 10, FR-8 | 1 |
| htmx + SSE instead of an SPA; reject PocketBase | ADR-19, section 12 | 2 |
| Normalised Arabic FTS (unicode61 + trigram); defer sqlite-vec | ADR-24, FR-10, spike 8 | 3 |
| GoReleaser, Homebrew tap, Docker image; Windows service wrapper only when needed | FR-20 | 4 |
| Default local ASR to large-v3 (not turbo); never present transcripts as authoritative | Section 12c, RISK-11, spike 6 | 1 |
| Spikes 1–8 | Section 13b | 0 to 3 |
| Later: semantic search, backup import, Litestream, remote workers | Section 13 "Later" | Later |
