# WhatsApp Archiver — Project Charter & Build Plan

Oct 13, 2026 · @Hamza

## 1. TL;DR

WhatsApp Archiver is a self-hosted app that silently records chosen WhatsApp chats into a local database, then runs plug-in processing modules (transcription, translation, OCR, classification) over what it records, all controlled from a simple web UI.

It is built first as Hamza's own tool for Batterjee Medical College, where Arabic WhatsApp groups act as the de facto LMS. It acts as a linked WhatsApp device that never sends anything, and its parts can run together on one machine or be spread across several, chosen at install time rather than built in. It stores raw data untouched and treats every kind of processing as a module that Claude Code can add later without changing the core.

Version 1 is done when the app pairs from the browser, logs only the chats switched on in the UI, saves their files, shows live per-chat processing status, and runs at least one module end to end through the module contract. It will be published on GitHub, with setup simple enough for a basic user on any machine.

**Document status:** draft v0.1. Items marked *agreed* came from Hamza; items marked *proposed* are Claude's calls for him to check.

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

&#91;embedded content: archiver architecture · capture, processing, access\]

Each part does one job: the bridge only records, modules only add artifacts, and agents only read. Any part can be replaced without touching the others.

## 5. Scope, non-goals and constraints

Version 1 is a read-only, local-only archiver with a UI and a module system; everything that sends, syncs to the cloud, or serves other users is out.

**In scope (v1):**

- Pairing as a WhatsApp linked device from the browser, with session status. *(agreed)*
- Per-chat, per-group and per-community control over what is logged. *(agreed)*
- Saving files (documents, images, voice notes, video with a size cap). *(agreed)*
- Processing as plug-in modules: transcription, translation with a chosen language, OCR, classification. *(agreed)*
- Live views of logging and processing, per chat and app-wide. *(agreed)*
- A read-only MCP server and exports so Hermes, Claude or other agents can use the data. *(proposed)*

**Non-goals:**

- **Sending anything to WhatsApp** (messages, reactions, read receipts) — the app must never act as Hamza in a group, and staying silent lowers ban risk.
- **Google Drive or any cloud sync in v1** — storage is local; an upload pipeline for the database and media comes later. *(agreed)*
- **Multi-user accounts, a hosted service or commercial use** — each person runs their own copy; the GitHub release must still be easy for anyone to install. *(agreed)*
- **The digest or agent logic itself** — Hermes or Claude consume the data through MCP; the archiver doesn't decide what to tell Hamza.
- **WhatsApp Business Cloud API** — it can't read Hamza's personal groups.

**Constraints:**

| ID | Constraint | Type |
| --- | --- | --- |
| CON-1 | Never send to WhatsApp; no send code paths exist in the build | safety |
| CON-2 | Raw logged data is immutable; processing only adds, never edits | data |
| CON-3 | Adding a module must never require changing core code, the database schema or the UI | architecture |
| CON-4 | Not tied to any machine or OS: each role (hub, capture, worker) runs on any supported device, and where each runs is a deployment choice, not code | deployment |
| CON-5 | No secrets, session files or personal data in the repo; it must be safe to publish | repo |
| CON-6 | Live database stays on local disk; never inside a cloud-sync folder | data |

## 6. Objectives and success criteria

The app succeeds if nothing in the chosen BMC groups is lost, Hamza can see what it's doing, and new processing is cheap to add.

| ID | Objective | Success criterion (measurable) | Verify via |
| --- | --- | --- | --- |
| OBJ-1 | Complete capture of chosen chats | SC-1: every message in an enabled chat over a 7-day period is in the database, checked against the phone by hand | Manual audit of 2 groups |
| OBJ-1 |  | SC-2: 0 messages from disabled chats are written to disk | Database query after a week |
| OBJ-2 | Files survive expiry | SC-3: at least 99% of enabled-type media in enabled chats downloaded within 5 minutes of arrival | Jobs table stats |
| OBJ-3 | Simple control from a UI | SC-4: pairing, enabling a chat and turning on a module each take under 1 minute with no file editing | Walkthrough |
| OBJ-4 | Visible processing | SC-5: a status change for any job shows in the UI within 2 seconds | Watch the feed while a voice note processes |
| OBJ-5 | Modules are cheap to add | SC-6: Claude Code adds a new module from one instruction, touching only `modules/<name>/`, and it passes the module test harness | Add the OCR module this way |
| OBJ-6 | Reliable unattended running | SC-7: survives a restart and a dropped connection with no data loss and no re-pairing | Kill and restart test |
| OBJ-7 | Anyone can set it up | SC-8: someone with no Linux or terminal experience completes profile A and profile B from the README alone, on fresh machines, without outside help | Hand the README to a non-technical tester before release |

**Definition of done (v1):** SC-1 to SC-8 pass; FR-1 to FR-12 and FR-16 to FR-22 implemented; transcription and translation run as modules.

## 7. Functional requirements

FR-1 to FR-12 and FR-16 to FR-22 make up v1; FR-13 to FR-15 are later work.

| ID | Requirement | Priority | Acceptance | Refs |
| --- | --- | --- | --- | --- |
| FR-1 | Pair as a linked device by scanning a QR code shown in the UI; show connection status and session age | must | Pair, restart, still connected | OBJ-3, OBJ-6 |
| FR-2 | List every chat, group and community; toggle logging per item; communities expand to their sub-groups | must | Toggling changes what is written within one message | OBJ-1, OBJ-3 |
| FR-3 | New groups appear in a pending tray; content from pending groups is buffered 48 hours, then kept on approval or deleted on rejection | must | Join a test group, approve, see buffered messages | OBJ-1 |
| FR-4 | Log messages, edits, deletions, pins, polls and group info changes; skip typing, presence, receipts, Status and calls | must | Edited message keeps both versions | OBJ-1 |
| FR-5 | Download media on arrival by per-chat rule: documents, images, audio, video up to a size cap; skip stickers | must | SC-3 | OBJ-2 |
| FR-6 | Import history-sync backlog through the same filters, with a progress bar | should | Backlog of enabled chats appears; others don't | OBJ-1 |
| FR-7 | Discover modules from their folders and generate their settings page, per-chat toggle and pipeline pill from the manifest | must | New module appears after restart with no UI change | OBJ-5 |
| FR-8 | Run modules through a job queue with retries, chaining and versioned re-processing | must | Bump version, re-run over archive | OBJ-5 |
| FR-9 | Live views: per-chat pipeline strip, activity feed, queue view with retry/skip, per-message trace, health panel | must | SC-5 | OBJ-4 |
| FR-10 | Browse and search the archive by chat, sender, date and text, including module output | should | Find a message by its English translation | UC-3 |
| FR-11 | Read-only MCP server over messages and artifacts | should | Hermes or Claude can list and search | UC-4 |
| FR-12 | Ship transcription and translation (target language chosen) as the first two modules | must | Voice note → Arabic transcript → English translation | OBJ-5 |
| FR-13 | OCR and classification modules | could | — | OBJ-5 |
| FR-14 | Export to JSON, Markdown, CSV | could | — | UC-4 |
| FR-15 | Usage meter: API calls and estimated cost per module | could | — | OBJ-4 |
| FR-16 | First-run wizard: password, pairing, chat selection, module and engine setup, optional remote access | must | A new user records a first chat in about 15 minutes with no file editing | OBJ-3 |
| FR-17 | Engine choice per module, local model or API key, with cost, privacy and speed guidance, model download management, spending cap, and a test before use | must | No jobs run until the chosen engine passes its test on a sample | OBJ-3, OBJ-5 |
| FR-18 | Bulk actions: enable a whole community; apply settings to many chats at once | should | Ten chats configured in one action | OBJ-3 |
| FR-19 | Re-pairing flow for unexpected unlinks and for moving capture to another machine, with an inactivity warning beforehand | must | Unlink the device on the phone; re-pair from the UI with no data loss | OBJ-6 |
| FR-20 | Installers for each setup profile and a doctor command that checks the install and explains fixes in plain words | must | SC-8 | OBJ-7 |
| FR-21 | Optional built-in Tailscale so the dashboard and nodes are reachable from the user's devices anywhere | should | Dashboard opens on a phone away from home after pasting one auth key | OBJ-3 |
| FR-22 | README and guides as specified in section 12e | must | SC-8 | OBJ-7 |

## 8. Non-functional requirements

Reliability and safety matter most; speed matters only for the live UI.

| ID | Category | Requirement | Why |
| --- | --- | --- | --- |
| NFR-1 | Reliability | Runs as a background service; restarts itself; reconnects after network loss without re-pairing | It runs unattended |
| NFR-2 | Reliability | Writing a message never waits on processing; a failing module can't block logging | Capture must not depend on slow or broken modules |
| NFR-3 | Observability | A health warning fires when no event has arrived for a set period, and before WhatsApp's roughly 14-day phone-inactivity logout | Silence otherwise looks like "no new messages" |
| NFR-4 | Privacy | Data stays on the box; only modules Hamza enables send content to third-party APIs | Classmates' and staff messages are in the archive |
| NFR-5 | Maintainability | The whatsmeow version can be upgraded on its own when WhatsApp changes its protocol | Protocol breaks are the most likely failure |
| NFR-6 | Portability | One binary per platform with the UI built in, able to run any role; Python needed only on machines that run Python modules | Easy to run and to publish |
| NFR-7 | Responsiveness | UI status updates within 2 seconds (SC-5) | The live views are a core feature |

## 9. Technical direction

A Go core with the UI built in, SQLite on local disk, and modules as separate programs, mostly Python. All *proposed*; see ADR-2 to ADR-5.

| Layer | Choice | Why |
| --- | --- | --- |
| WhatsApp client | whatsmeow (Go) | Mature multi-device library; lharries/whatsapp-mcp shows the logging pattern |
| Core | Go: bridge, scope filter, writer, media downloader, job queue, module runner, web server, MCP server | One process, one binary |
| Storage | SQLite (WAL mode) plus a media folder on local disk | Simple and fast; can be uploaded later |
| UI | Svelte or React built with Vite, embedded in the binary; live updates over Server-Sent Events | Opens at a local address, no separate install |
| Modules | Any language; Python by default (Whisper, OCR, model SDKs) | The ML ecosystem lives in Python |
| Agent access | MCP server, read-only | Hermes and Claude both speak MCP |
| Deployment | One binary with three roles (hub, capture, worker), run all together or split across machines; native builds for macOS, Linux and Windows plus Docker images | CON-4 |

**Repo layout (intended):**

```
whatsapp-archiver/
  core/            # Go: bridge, filter, store, queue, runner, api, mcp
  web/             # UI source, built into core
  modules/
    _template/     # working example module to copy
    transcribe/
    translate/
  fixtures/        # sample envelopes for each message type
  docs/            # this charter, MODULES.md
  data/            # session, database, media, keys (gitignored)
  CLAUDE.md        # rules for Claude Code, incl. 'never edit core to add a module'
```

**Conventions:** all config editable through the UI and stored in the database; secrets only in `data/`; the module contract in `docs/MODULES.md` is the source of truth for module work.

## 9b. Design: where things run

The app is split into roles that talk to each other only over the network, so which machine runs which role is a setting, not code. Running everything on one computer is the default; splitting later needs no rebuild. *(proposed)*

&#91;embedded content: deployment roles · one hub, one capture, any number of workers\]

The same binary runs every role; a command or config line picks which ones a machine runs.

| Role | What it does | How many | Where it can run |
| --- | --- | --- | --- |
| Hub | Owns the database and media, the job queue, the hub API and the web UI | Exactly 1 | Any always-on machine with disk |
| Capture | The linked WhatsApp device; filters and pushes events to the hub; buffers locally if the hub is unreachable | Exactly 1 (one WhatsApp session) | Any always-on machine |
| Worker | Pulls jobs from the hub, fetches media through the API, runs modules, sends artifacts back | 0 or more | Any machine; each reports what it has (GPU, Python, local models) |
| MCP server | Reads the archive through the hub API for an agent | 0 or more | Wherever the agent runs |
| Browser | The UI | Any | Any device that can reach the hub |

**Rules that make this work:**

- **The hub is the only stateful role.** Nothing else touches the database file or media folder directly; everything goes through the hub API, even when all roles share one machine. This keeps SQLite safe and makes moving a role a config change.
- **Every other role connects out to the hub** with a node token issued in the UI, so nodes behind home routers work. Connections are encrypted, and the UI asks for a login when opened from anywhere but the hub itself.
- **Workers pull, the hub routes.** Each worker announces which modules it has installed and what it can do; a module's needs field decides which workers may take its jobs. Add a GPU machine later and Whisper jobs move to it automatically.
- **A Nodes screen in the UI** lists every connected machine, its roles, health, installed modules and current jobs, and issues or revokes node tokens.
- **Packaging:** native builds for macOS, Linux and Windows, plus Docker images per role. Install steps never assume a particular machine.

## 10. Design: the module contract

Every module receives the same envelope, returns the same artifact shape, and describes itself in one manifest; the core never changes when a module is added. *(agreed in principle; details proposed)*

**Envelope (what a module receives, as JSON):**

| Field | Contents |
| --- | --- |
| message | id, chat (id, name, course tag), sender (id, name, role), timestamp, original text, quoted message id, forwarded flag |
| media | type, MIME type, local file path, size, duration or page count; empty when there is no file |
| artifacts | everything other modules have already produced for this message |
| settings | this module's settings for this chat (e.g. target language) |

**Artifact (what a module returns):** a list of items, each with a `kind` (e.g. `transcript`, `translation`, `ocr_text`, `label`, `event`), a language, text, and optional structured data. The core stores all of them in one generic table, so new modules never need new tables.

**Manifest (one per module folder):**

| Field | Purpose |
| --- | --- |
| name, version, label, icon, description | Identity and UI display; bumping version enables re-processing |
| triggers | What wakes it: message type, MIME type, or an artifact kind another module produces |
| produces | Artifact kinds it outputs |
| settings | Typed fields (text, number, select, multiselect, toggle); the UI form is generated from these |
| requires\_env | API keys it needs; the UI asks for them. A needs field lists what a worker must have (GPU, Python, a local model), and jobs only go to workers that have it |
| run | Command to start it, any language |
| mode | `per_job` (a new process per message, JSON in and out) or `service` (stays running, jobs streamed line by line, for heavy models like Whisper) |

**Chaining:** triggers on artifact kinds let modules feed each other. Transcribe produces `transcript`; translate triggers on message text, `transcript` and `ocr_text`; classify triggers on `translation`; extract-events triggers on certain labels. Adding OCR later means translate picks up PDF text with no change.

**What the core guarantees:** jobs keyed by message + module + version so nothing runs twice; retries with the error kept; every status change sent to the UI; versioned re-runs over the archive; raw data never touched.

**Making it Claude-friendly:** `modules/_template/`, real fixtures for each message type, a CLI test command that runs a module against fixtures or a real message and validates its output, and `docs/MODULES.md` plus `CLAUDE.md` stating the contract and the rule never to edit core for a module.

## 11. Design: data model

The database has a raw layer that only the bridge writes, a derived layer that only modules write, and a control layer for settings and jobs. Field-level schema is left to the builder. *(proposed)*

| Layer | Entity | Holds |
| --- | --- | --- |
| Raw | chats | Chat id, name, type (DM, group, community), parent community, logging state (on, off, pending), course tag |
| Raw | participants | Identity (phone or anonymous LID), display name, role set by Hamza (staff, class rep, student) |
| Raw | messages | Id, chat, sender, timestamp, original text, quoted id, forwarded flag, media type and local path |
| Raw | message\_events | Edits, deletions, pins, reactions, poll votes, group info changes, each with time and new value |
| Raw | media | File path, MIME type, size, hash, download state |
| Derived | artifacts | Message, module, module version, kind, language, text, structured data, created time |
| Control | modules | Installed modules, enabled state, global settings, API key references |
| Control | chat\_module\_settings | Which modules run on which chat, with per-chat settings |
| Control | jobs | Message, module, version, status (queued, running, done, failed, skipped), attempts, error, timings, cost |
| Control | pending\_buffer | Messages from groups awaiting approval, with expiry time |

The `jobs` table drives every live view in the UI. Rebuilding derived data means deleting artifacts for a module and re-queuing its jobs; raw tables are never touched.

## 12. Design: UI screens and live views

The UI is a local web dashboard with seven screens; simplicity and seeing what is happening are the two priorities. *(agreed)*

| Screen | What it does |
| --- | --- |
| Dashboard | Connection status, chats logging, queue size; every enabled chat with its pipeline strip; live activity feed |
| Chats | Every chat, group and community with toggles for logging, file types, and each module; pending tray for new groups; community expand |
| Modules | Installed modules from their manifests, each with an enable switch, generated settings form, API key entry, and a re-process button when the version changes |
| Queue | Jobs by stage and status, throughput, failed jobs with error text and retry or skip |
| Archive | Browse and search by chat, sender, date and text; open a message to see its trace: original, each module's output, timings, cost |
| Settings | Pairing and re-pairing QR, media caps, storage paths, health thresholds, MCP on or off |
| Nodes | Every connected machine with its roles, health, installed modules and current jobs; issue or revoke node tokens (section 9b) |

**Live views (FR-9):**

- **Pipeline strip per chat:** one pill per enabled stage, showing count and state (done, running, queued, failed, off). Clicking a pill opens that chat's jobs at that stage.
- **Activity feed:** rolling log of every event and job change, filterable by chat and stage.
- **Message trace:** step-by-step view of everything that happened to one message.
- **History-sync progress bar** during first pairing and after approving a group.
- **Health panel:** session age, time since last event, disk use, and the inactivity-logout warning.

A first mockup of the dashboard was shown in chat on 13 Oct 2026; the visual design is left to the builder.

## 12b. First run and user journeys

A new user should go from install to recording their first chat in about 15 minutes, guided by a wizard rather than by separate screens. *(agreed)*

**First-run wizard (FR-16):**

1. **Welcome and password.** Create the UI login. Show where the app is storing data and how much free space there is.
2. **Pair WhatsApp.** QR code with phone instructions (WhatsApp → Linked Devices → Link a Device). Confirm with "Connected as +…".
3. **Choose chats.** Search, a "suggested" strip from name matches, communities that enable all their groups at once, and bulk settings for everything selected (FR-18).
4. **Choose processing.** For each module the user turns on, pick local or API and complete its setup right there (section 12c). No module runs until its setup passes a test.
5. **Remote access (optional).** Offer Tailscale so the dashboard works on the user's other devices (section 12d).
6. **Done.** Land on the dashboard with the backlog import already running.

**Everyday use:** nothing to do. New groups appear as pending with an approve button; the archive is searchable in the original language and in translation; agents read through MCP if enabled.

**Adding a module:** Claude Code creates it under `modules/`; after a restart it appears on the Modules screen and as a toggle on every chat; "Run on existing messages" backfills it.

**Adding a machine:** "Add node" on the Nodes screen gives a one-line install command with a token; the new machine appears with what it can do, and matching jobs start going to it.

**Recovery (FR-19):** re-pairing is a first-class flow, both for unexpected unlinks and for moving capture to another machine. The health banner warns before WhatsApp's inactivity unlink, and failed jobs show their error with a retry button.

## 12c. Processing engines: local or API

Transcription and translation each let the user choose an engine: a model running on their own hardware, or a provider's API with their own key. The UI guides the choice and tests it before any jobs run. *(agreed)*

The engine is a setting of the module, not a separate module: the transcribe module contains adapters for each engine, so chaining and the rest of the contract stay the same.

**Engine options (as at Oct 2026; confirm current models and pricing during the build, see Q-5):**

| Task | Engine | Type | Runs best on | Notes |
| --- | --- | --- | --- | --- |
| Transcription | whisper.cpp | Local | Apple Silicon Macs (uses the Mac's GPU); also CPU | Easy single install; good default on a Mac |
| Transcription | faster-whisper | Local | NVIDIA GPU; CPU works but is slow with large models | Good default on Linux and Windows |
| Transcription | OpenAI transcription API | API | Anywhere | Paid per minute of audio |
| Transcription | Groq (hosted Whisper) | API | Anywhere | Paid, typically fast and low cost |
| Transcription | Other providers (ElevenLabs, Deepgram) | API | Anywhere | Adapters can be added later |
| Translation | Ollama with a multilingual model | Local | A machine with enough RAM or a GPU | Free, private; quality depends on the model chosen |
| Translation | NLLB-200 | Local | CPU or GPU | Dedicated translation model; weaker on informal dialect |
| Translation | Claude, OpenAI or Gemini API | API | Anywhere | Paid per token; handles informal Arabic and context well; can also classify in the same call |
| Translation | DeepL or Google Translate API | API | Anywhere | Paid per character; literal, no classification |

Whisper comes in sizes from tiny to large; larger sizes are much better at Arabic and at dialect, and much slower without a GPU. The UI recommends a size based on the worker's hardware.

**Guidance in the UI when a user turns a module on:**

- **A choice card per engine** showing cost (free or paid), privacy (stays on your machine or is sent to the provider), expected speed on *this* user's workers, and a one-line quality note.
- **Local:** shows the model's download size and disk space before downloading; the worker downloads and manages it; a progress bar shows the download.
- **API:** a key field with a link to where to get that provider's key, a short explanation of what a key is, and an optional monthly spending cap after which jobs pause.
- **Test before use:** a "Test" button runs the engine on a built-in Arabic sample voice note or message and shows the output and time taken. Jobs only start after a passing test.
- **Switch any time:** changing engine never deletes existing output; the user can re-run old messages with the new engine.

## 12d. Setup profiles

The installer asks one question, "where should this run?", and sets up one of four profiles; all four use the same binary and roles from section 9b. *(proposed)*

| Profile | Who it suits | What runs where | Main trade-off |
| --- | --- | --- | --- |
| A. Main computer | Trying it out; light use; one laptop or desktop (macOS, Windows or Linux) | Everything on one machine | Only records while the computer is on and awake; uses its disk |
| B. Home server | Reliable 24/7 capture; not enough space on the main computer (Hamza's case) | Everything on an always-on Linux machine; dashboard opened from other devices | Needs a spare machine and a short Linux setup |
| C. Split | Server for capture and storage, plus a stronger machine for local models | Hub and capture on the server; worker on a GPU PC or Apple Silicon Mac | Two machines to keep updated |
| D. Any of the above + remote access | Wants the dashboard on phone, tablet and other computers, at home or away | Adds Tailscale to the hub | Needs a free Tailscale account |

**Profile A on a Mac or Windows PC:** an installer package; the app starts at login and lives in the menu bar or system tray; the data folder can point to an external drive. The wizard warns that recording stops when the computer sleeps and offers to keep it awake while plugged in.

**Profile B for users new to Linux (README walkthrough, no prior knowledge assumed):**

1. **Hardware:** any spare PC, old laptop or mini PC that can stay plugged in, with enough disk for the user's media.
2. **Install Linux:** Ubuntu Server 26.04 LTS, the current long-term support release, supported until April 2031. Download it, write it to a USB stick with a tool like balenaEtcher, boot from it, and follow the installer, ticking "Install OpenSSH server".
3. **Connect from the main computer** over SSH, with screenshots for macOS and Windows.
4. **Run the one-line installer.** It checks the system, installs what is needed (Python and ffmpeg for audio, optionally Docker), creates a service that starts on boot and restarts on failure, offers automatic security updates, and prints the setup link.
5. **Open the setup link** from the main computer's browser and follow the wizard.

**A doctor command** checks everything at any time (disk space, service running, WhatsApp connected, workers reachable, engines tested) and explains each problem in plain words with the fix.

**Remote access with Tailscale (profile D, FR-21):** the hub can join the user's private Tailscale network directly, using Tailscale's library built into the app, so no separate Tailscale install is needed on the hub. The user pastes an auth key from their Tailscale account into the UI, and the dashboard gets a private HTTPS address reachable from any of their devices running Tailscale. Other nodes can connect to the hub over the same network. Users who already run Tailscale can skip this and use their existing setup. Without Tailscale, the dashboard is available on the home network only.

**Storage:** the user picks the data folder during setup; the UI shows disk use, growth per week and a warning before space runs out. Media download rules (FR-5) are the main lever.

## 12e. README and docs requirements

The GitHub README must get a basic user from nothing to a working setup without outside help, while letting an advanced user skip to the parts they need. *(agreed)*

**Structure:**

1. **What it is, in three sentences,** plus a dashboard screenshot.
2. **Read this first:** it is an unofficial WhatsApp client with a small ban risk; it never sends messages; your data stays on your machines unless you turn on an API engine.
3. **Pick your setup:** the four profiles from section 12d as a short decision guide ("Is your computer on all the time? Do you have a spare machine?").
4. **Step-by-step guide per profile,** each self-contained, with screenshots, expected output after every step, and "if this fails" notes.
5. **First-run wizard walkthrough.**
6. **Choosing engines:** local versus API in plain words, what an API key is and where to get one, rough costs, and privacy.
7. **Remote access with Tailscale,** from creating an account to opening the dashboard on a phone.
8. **Updating, backing up and moving to another machine.**
9. **Troubleshooting,** organised by symptom, starting with the doctor command.
10. **For developers:** architecture, the module contract, and how to add a module with Claude Code.

**Rules:**

- **Assume nothing:** explain every term the first time it appears (terminal, SSH, API key, Tailscale), with a glossary at the end.
- **Every command is copy-paste ready** and says which machine to run it on.
- **Advanced users can skip:** a short "quick start" for each profile at the top, with Docker Compose and config-file options in a separate section.
- **Tested before release:** each profile's guide is followed from a fresh machine before each release (SC-8).

## 13. Build plan

Seven phases, each leaving a working app; phase 0 fixes the module contract before any feature code. *(proposed)*

| Phase | Goal | Delivers | Main work | Exit criterion |
| --- | --- | --- | --- | --- |
| 0 | Foundations | — | Repo skeleton, `CLAUDE.md`, `docs/MODULES.md` with the contract in section 10, database layers, `.gitignore` for `data/` | Contract reviewed and approved by Hamza |
| 1 | Pair and log | FR-1, FR-2, FR-4 | whatsmeow bridge with no send paths, scope filter, raw writer, minimal UI with QR pairing and chat toggles, all roles running in one process, background service on macOS, Linux and Windows | SC-2 holds; messages from one test group appear in the database; survives restart |
| 2 | Files and backlog | FR-3, FR-5, FR-6, FR-18 | Media downloader with per-chat rules and caps, pending tray with 48-hour buffer, history-sync import with progress | SC-3 holds on a test group |
| 3 | Jobs and live UI | FR-9 | Jobs table, queue, hub API that capture and workers use even when on the same machine, Server-Sent Events, dashboard, pipeline strips, activity feed, queue view, health panel | SC-5 holds with a dummy module |
| 4 | Module system | FR-7, FR-8, FR-12, FR-17 | Manifest loader, generated settings and pills, worker role pulling jobs over the hub API with per\_job and service runners, needs-based job routing, a Nodes screen, chaining, re-processing, test harness, `_template`, fixtures; then transcribe and translate built as ordinary modules | SC-6 holds: translate added only under `modules/` |
| 5 | Outputs and archive | FR-10, FR-11 | Archive browse, search and message trace; read-only MCP server; connect Hermes and run a test digest | Hermes answers "what changed in anatomy this week?" from the archive |
| 6 | Setup and release | FR-16, FR-19, FR-20, FR-21, FR-22 | First-run wizard, re-pair flow, installers for each profile, doctor command, built-in Tailscale, README and guides with screenshots | SC-8 holds for profiles A and B |
| Later | Extras | FR-13 to FR-15 | OCR, classification, exports, usage meter; database and media upload pipeline | — |

Run phases 1 to 3 against a throwaway test group before enabling real BMC groups.

## 14. Risks

The biggest risk is Hamza's own WhatsApp number, since the app runs on it.

| ID | Risk | Likelihood | Impact | Mitigation |
| --- | --- | --- | --- | --- |
| RISK-1 | WhatsApp restricts or bans the number for using an unofficial client | low | high | Never send (CON-1); no read receipts; normal linked-device behaviour; accept the residual risk knowingly |
| RISK-2 | WhatsApp protocol change breaks the bridge | med | med | Keep whatsmeow upgradable on its own (NFR-5); health alert; re-pair from the UI |
| RISK-3 | Linked device logged out after the phone is inactive about 14 days | med | med | Health warning before the limit (NFR-3) |
| RISK-4 | Messages missed while the capture node is offline beyond WhatsApp's redelivery window | low | med | Service auto-restart; uptime shown in the health panel |
| RISK-5 | Media links expire before download | med | med | Download on arrival; failed downloads retried and shown in the queue |
| RISK-6 | Third-party APIs see classmates' messages | med | low | Modules off by default; per-chat control; local options (e.g. local Whisper) where practical |
| RISK-7 | Scope creep into digests, agent logic or a hosted product | med | med | Non-goals in section 5; those live in Hermes or a separate project |

## 15. Assumptions and open questions

Five assumptions let building start now; four questions are worth answering before or during phase 1.

**Assumptions:**

| ID | Assumption | If wrong |
| --- | --- | --- |
| AS-1 | The app links to Hamza's personal number, since that is the only account in the BMC groups | A second number can't see the groups; the design still holds |
| AS-2 | At least one machine Hamza owns can stay on 24/7 to run the hub and capture roles | Messages can be missed during downtime beyond WhatsApp's redelivery window |
| AS-3 | "WhatsApp Archiver" is a working name | Rename only |
| AS-4 | English is the default translation target; Arabic is the main source language | Only module settings change |
| AS-5 | Hamza will build with Claude Code, phase by phase, from this document | Plan granularity may need adjusting |

**Open questions:**

| ID | Question | Blocking? | status |
| --- | --- | --- | --- |
| Q-1 | How should nodes reach the hub across networks? Proposed: home network by default, with Tailscale built in as an option (section 12d). | phase 3 | proposed |
| Q-2 | For Hamza's own setup: which transcription engine to start with, local Whisper (free, slower without a GPU) or an API (fast, paid, sends audio out)? Every user chooses for themselves in the wizard. | phase 4 | open |
| Q-3 | Which model provider and budget for translation? | phase 4 | open |
| Q-4 | Should DMs from staff be logged, or only groups? | phase 1 | open |
| Q-5 | Confirm the current engine list, model names, Arabic quality and pricing in section 12c; these change often | phase 4 | open |
| Q-6 | Minimum and recommended hardware for each profile (RAM, disk, CPU), measured on real installs, for the README | phase 6 | open |

## 16. Decision log

These decisions came out of the 13 Oct 2026 design conversation and should not be reopened without a reason.

| ID | Decision | Why | Rejected | Status |
| --- | --- | --- | --- | --- |
| ADR-1 | Separate capture from intelligence: the archiver records and processes; Hermes or Claude reads it through MCP | Hermes's WhatsApp gateway replies in groups and can't listen passively | Running Hermes directly on WhatsApp | agreed |
| ADR-2 | Build a standalone app with a UI rather than a config-file daemon | Simple to use and navigate | Fork lharries/whatsapp-mcp and edit config by hand | agreed |
| ADR-3 | Never send; no send code in the build | Can't post into groups by mistake; lower ban risk | Disabling send by config only | proposed |
| ADR-4 | Raw data immutable; all processing writes to a generic artifacts table | Safe re-processing; modules need no schema changes | Per-module tables; editing messages in place | proposed |
| ADR-5 | Modules are separate programs with a manifest and JSON in and out | Any language, mainly Python; Claude can add one without touching core | Compiled-in Go plugins | proposed |
| ADR-6 | Local storage only in v1 | Live SQLite in a sync folder corrupts; upload pipeline later | Google Drive as primary store | agreed |
| ADR-7 | Go core on whatsmeow | Mature library, single binary | Node with Baileys | proposed |
| ADR-8 | Split into roles (hub, capture, worker) that talk only over the hub API; where each runs is configuration | The app must not assume any particular machine; heavy modules can move to better hardware later | One process tied to one machine; roles sharing the database file directly | agreed in principle |
| ADR-9 | Engines (local or API) are settings inside a module, with adapters, not separate modules | Switching engine never changes chaining or stored output kinds | One module per engine | proposed |
| ADR-10 | Remote access through Tailscale built into the hub, optional; home network only by default | Private access from any device without opening router ports or a separate install | Port forwarding; a public cloud relay | proposed |
| ADR-11 | Recommend Ubuntu Server 26.04 LTS for the home-server profile | Current long-term support release, supported to April 2031, widely documented for beginners | Debian, Fedora (supported, not the guided default) | proposed |

## 17. Glossary and changelog

| Term | Meaning |
| --- | --- |
| Linked device | A WhatsApp session paired to a phone by QR code; receives messages independently of the phone |
| whatsmeow | The Go library that implements the WhatsApp multi-device protocol |
| Bridge | The part of the core that is the linked device and writes raw data |
| Scope filter | The check that drops messages from chats not switched on |
| Envelope | The standard JSON a module receives for one message |
| Artifact | One piece of module output (a transcript, a translation, a label) |
| Manifest | A module's self-description file |
| Job | One module run on one message; its status drives the live UI |
| LID | WhatsApp's anonymous member identity in groups, mapped to a phone number where possible |
| History sync | The backlog the phone pushes to a newly linked device |
| MCP | Model Context Protocol; how Hermes and Claude read the archive |
| Role | One part of the app (hub, capture, worker) that can run on any machine |
| Hub | The single role that owns the database, media, job queue, API and UI |
| Node | A machine running one or more roles, connected to the hub with a token |
| Worker | A role that pulls jobs from the hub and runs modules |
| Engine | What actually does a module's work: a local model or a provider's API |
| Profile | A ready-made setup: main computer, home server, split, or any of these with remote access |
| Tailscale | A private network service that connects a user's own devices securely from anywhere |
| Doctor | A command that checks the install and explains any problem with its fix |

| Version | Date | Change |
| --- | --- | --- |
| 0.1 | 13 Oct 2026 | First draft from the design conversation |
| 0.2 | 13 Oct 2026 | Removed machine-specific assumptions; added roles and nodes (section 9b, ADR-8, Nodes screen) |
| 0.3 | 13 Oct 2026 | Added user journeys and wizard (12b), engines (12c), setup profiles and Tailscale (12d), README spec (12e), FR-16 to FR-22, SC-8, phase 6 |
