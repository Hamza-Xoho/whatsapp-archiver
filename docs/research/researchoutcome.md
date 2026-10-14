# Design Research Report: WhatsApp Archiver

Build it, but cut it down: keep the Go + whatsmeow + SQLite capture core, add a large amount of capture-hardening work to phase 0, and drop or defer the multi-node role split, embedded Tailscale, the long-running "service" module mode and the separate JavaScript SPA.

**TL;DR**
- **Reuse:** no existing project meets the fixed constraints. Every serious candidate ships send code and none keeps an immutable raw layer. The closest, openclaw/wacli (MIT, Go, whatsmeow, SQLite + FTS5), should be mined for code and field fixes rather than forked. mautrix-whatsapp is the best reference for protocol edge cases but is too heavy to build on.
- **Capture risk:** the biggest threat is not bans. It is "client outdated" (405) breakage and silent gaps. On 5 Oct 2026 WhatsApp rejected the client version built into whatsmeow builds from June 2026, and many downstream projects went dark until they bumped the library. Phase 0 must treat dependency updates, undecryptable messages, LID mapping and history-sync continuation as core features.
- **Scope:** collapse the three-role topology into one process with clean internal packages. Make modules plain per-job subprocesses that take JSON on stdin and write JSON to stdout, run from a SQLite job table. Replace the plan's grep-based `nosend` test with a single wrapper package plus lint-enforced import bans, because whatsmeow itself always sends protocol-level stanzas. A "no send code in the build" guarantee is impossible.

## Verdict at a glance

Adjust the design; don't replace it. The capture core (Go, whatsmeow, a separate session DB, an insert-only archive, a media queue, backups, alerts) is the right shape. It closely matches what wacli and mautrix-whatsapp converged on. The single biggest change is to reprioritise phase 0 around the failure modes that actually take whatsmeow loggers offline in 2026: client-version expiry, undecryptable messages, LID/phone-number identity splits, and history sync that arrives late and in pieces. Then defer the distributed topology, Tailscale, service-mode modules and the SPA until real use proves a need.

## The design as understood

- **Capture:** a whatsmeow linked device on the personal number. It requests full history at pairing, logs everything except excluded chats, skips status/typing/presence/receipts/calls, and keeps the raw protobuf of every message.
- **Storage:** SQLite in WAL mode with three layers. The raw layer is insert-only, with edits, deletes and reactions stored as event rows. The derived layer is one generic `artifacts` table. The control layer holds modules, settings and jobs. Session keys live in their own DB, media sits in a folder tree, and backups are nightly `VACUUM INTO` copies.
- **Media:** downloaded on a separate worker queue with retry/backoff and media re-upload requests.
- **Modules:** separate programs, each with a manifest. They are triggered by message type, MIME type or artifact kind, run per job or as a service, chain through artifact kinds, and support versioned re-runs. Adding a module never touches core code.
- **Topology:** one binary with hub, capture and worker roles that talk over an authenticated API, with optional embedded Tailscale.
- **UI:** a Svelte or React SPA embedded in the Go binary, with SSE live updates, seven screens, a wizard, a `doctor` command and installers.
- **Alerts:** ntfy and Telegram.
- **Key bets:**
  1. whatsmeow stays usable for months on a personal number.
  2. Full history can be pulled at pairing.
  3. A grep test can prove "never send".
  4. A home-grown module and job system can stay simple.
  5. Distributed roles are worth building in v1.

## Build vs. reuse

| Project | What it covers | Maturity (Oct 2026) | License | Fit with fixed constraints | Recommendation |
|---|---|---|---|---|---|
| **openclaw/wacli** (formerly steipete/wacli) | Linked-device pairing; `sync --follow` continuous capture; separate session and index SQLite DBs; FTS5 search; media download; history backfill; `doctor`; read-only mode; GoReleaser + Homebrew + Docker | About 2.8k stars, 455 commits, latest release v0.19.0; created by @steipete, now maintained by @dinakars777; 6 open issues; has an AGENTS.md | MIT | Includes send, chat-state and presence commands (blocked only at runtime by `--read-only`); uses CGO mattn/go-sqlite3; no immutable event layer, no module system | **Borrow ideas and code** (MIT allows it). Covers roughly 40–50% of phase 0, about 15% of the whole project |
| **mautrix-whatsapp** + homeserver + client | The most complete whatsmeow consumer: backfill, LID migration, app-state recovery, media re-requests, edits, polls | Monthly releases (v26.01–v26.09, latest 16 Sep 2026); led by Tulir Asokan, who also maintains whatsmeow | AGPL-3.0 | Bridges in both directions by design; needs a Matrix homeserver (Synapse etc.), which is too heavy for a non-technical installer; archive and search live in Matrix, not in your schema | **Read its changelog and code for fixes; don't build on it.** Don't copy code into an MIT-licensed project |
| **lharries/whatsapp-mcp** | Go bridge to SQLite plus a Python MCP server | One release (v0.0.1, 6 Apr 2025); issue #164 (10 Feb 2026) reports 10+ unmerged PRs fixing the 405 error; effectively abandoned; forks (verygoodplugins, FelixIsaac, rodrigopg, payana-apps) carry the fixes | MIT | Send is a core feature; minimal schema | **Ignore**, apart from a warning: it shows what happens when a pinned whatsmeow goes stale |
| **WAHA** (devlikeapro/waha) | REST API with WEBJS/NOWEB/GOWS (Go)/WPP engines, dashboard, MCP server (2026.4), LID API (2026.8.1), passkey pairing (2026.9) | Frequent releases (2026.9.2 latest seen); all Plus features free since 2026.6.1 | Apache-2.0 (per a third-party project page; verify) | A send/automation API; Docker-first; storage is its own; bot-oriented | **Ignore** as a base; useful only as a LID-handling reference |
| **GOWA** (aldinokemal/go-whatsapp-web-multidevice) | whatsmeow REST API, UI, MCP, webhooks, chat storage, opt-in full history sync (PR #855) | v8.3.x in early 2026 (v8.3.0 on 12 Feb 2026, v8.3.2 on 27 Feb 2026); mostly one maintainer | Not verified in this review | Send-centric (auto-reject calls, presence on connect, ghost mentions), all of which break the constraints | **Ignore** as a base |
| **Evolution API** | Multi-tenant WhatsApp API (Baileys-based) | Not verified in this review | Not verified | Send/CRM-oriented, Node.js | **Ignore** (no evidence it fits; not researched further) |
| **WhatsApp-Chat-Exporter** (KnugiHK) | Decrypts Android/iOS backups to HTML/JSON | Not verified in this review | Not verified | Offline, one-shot; useful only for backfill older than the sync window | **Optional later import path**; run a spike first |

The bottom line on reuse: the project is unusual because it is receive-only and has an immutable raw layer. Every maintained whatsmeow project is a send-capable API or bridge. Building your own thin capture core is justified. Rewriting fixes that wacli and mautrix have already made is not.

## Findings by question

### 1. Build vs. reuse

**Current plan:** write everything from scratch.

**Evidence:**
- **wacli is the nearest match.** It already does "pairs as a linked device, mirrors messages into a local SQLite store", keeps "the WhatsApp session and its own searchable message index in separate SQLite databases", and offers `sync --follow`, FTS5, `doctor`, media download and GoReleaser/Homebrew packaging (openclaw/wacli README, fetched Oct 2026).\[1\]
- **wacli's open PRs are a field manual for your phase 0.**
  - PR #461 (30 Sep 2026) documents full-history behaviour on a real account.\[2\]
  - PR #441 found that `events.UndecryptableMessage` "had no case in the sync handler, so no row was stored", and that whatsmeow leaves `AutomaticMessageRerequestFromPhone` off.\[3\]
- **wacli conflicts with the constraints.** It includes `send text`, `send file`, reactions, `mark-read` and presence commands, and its read-only mode is a runtime flag. That makes the never-send guarantee harder to prove if you fork it.
- **mautrix-whatsapp is the reference implementation.** Its 2026 changelog records fixes this project would otherwise rediscover:
  - "Fixed backfill sometimes racing with receiving LID mappings" and "duplicate portals if history sync contains both LID and phone number DM data" (v26.01);\[4\]
  - "automatic recovery for WhatsApp app state sync issues" (v26.02);\[4\]
  - "initial backfill to happen even if WhatsApp doesn't send full history" (v26.04);\[5\]
  - "new message edit encryption scheme" (v26.05);\[6\]
  - "newer versions of poll messages" (v26.07);\[4\]
  - "handling history sync events where the phone sends nonsensical timestamps" (v26.09).\[7\]
- **lharries/whatsapp-mcp is a cautionary tale.** Its single release is v0.0.1 from April 2025, and a community issue (10 Feb 2026) complains that "10+ open PRs all fixing the same critical issue: the pinned whatsmeow version gets rejected by WhatsApp with a 405".\[8\]\[9\]

**Recommendation: keep building your own core, but change how you source it.**
- Seed the Claude Code specs with wacli's design. It is MIT, so you may copy code with attribution: event handling, history-sync limits, the media and FTS approach, `doctor` checks.
- Seed the edge-case list from mautrix-whatsapp's changelog. Read the code for ideas only, because it is AGPL.
- Fork nothing.

**Confidence:** high.

### 2. Capture-layer reliability and safety

**Current plan:** whatsmeow as a silent linked device, with phase 0 hardening limited to reconnects and logout alerts.

**What the evidence says, item by item:**

- **whatsmeow cadence and breaking changes.**
  - whatsmeow has no tagged releases. It ships as pseudo-versions on `main`, with commits several times a week. The latest seen is v0.0.0-20261007111105-c386243a72ba (7 Oct 2026), and pkg.go.dev shows a newer one published 13 Oct 2026. License is MPL-2.0.\[10\]\[11\]\[12\]
  - Recent commits change the API surface. In early October 2026 projects had to adapt to `SetStatusMessage` now taking `types.SetStatusInput` and to a changed blocklist API. New commits also require Go 1.26.\[13\]\[14\]\[15\]\[16\]
  - **WhatsApp expires client versions with no warning.** On 5 Oct 2026, WhatsApp began rejecting client version 2.3000.1040847988, the one built into the 11 June whatsmeow. Goberna-Lab reported that all their lines "went down within half an hour with `client_outdated`, without anything changing on our side" (release v0.7.0-goberna.7, 7 Oct 2026). The same outage appears in PRs dated 5–10 Oct 2026 across rodrigopg, marksmeed, payana-apps, fereinaux/wuzapi and NIXKnight forks. A separate cut-off hit on 14 Sep 2026 (janfabian/claude-whatsapp PR #3: "Pinning whatsmeow to a commit means this will recur whenever WhatsApp retires that client version").\[12\]\[13\]\[14\]\[17\]\[18\]\[19\]\[20\]
  - **Runtime mitigation:** Goberna's fix queries `whatsmeow.GetLatestVersion` at startup and uses the newer version, with an operator override.\[21\] They verified that the old binary got `client_outdated` while the patched one reached the QR code.\[17\]
  - This repeated pattern, at least two cut-offs in four weeks, is the single most important operational fact for this design.

- **Bans and restrictions.**
  - Documented whatsmeow ban reports are overwhelmingly about sending. whatsmeow Discussion #199 lists ban reasons as "you sent too many messages to people who don't have you in their address books", "you sent the same message to too many people" and similar, and one user was banned after messaging 30 non-contacts with 30-second delays.\[22\]
  - whatsmeow issue #810 reports "Your account may be at risk… using unauthorized tools" warnings even for clients "only replying to incoming messages", and says the same was seen with Baileys.\[23\] That is a repeated pattern of warnings for low-volume senders, not for receive-only clients.
  - I found **no documented ban of a purely receive-only linked device**. Treat the risk as low but not zero, and as unproven either way. WhatsApp's own wording targets "unauthorized tools", which covers any whatsmeow client.

- **History sync.**
  - By default, whatsmeow asks only for the "recent" window, "roughly three months, with the exact window set by the phone" (Tauri-EPO/whatsapp-mcp issue #645).\[24\]
  - wacli PR #461 is the best single dataset found, from one account with an Android primary (Sep 2026):
    - An ordinary pairing gave exactly 90 days.\[2\]
    - `RequireFullSync` with 7,300 days requested stopped at about 50 MB, 22 months back.\[2\]
    - Adding `FullSyncSizeMbLimit=2048` reached exactly three years back (694,280 messages), pausing on mobile data and resuming hours later.\[2\]
    - Chunks "arrived after `auth` had exited and were stored by the next connection".\[2\]
    - `FULL_HISTORY_SYNC_ON_DEMAND` "is refused by a personal primary (`ERROR_REQUEST_ON_NON_SMB_PRIMARY`)".\[2\]
    - mautrix notes the days limit "seems to be around 3 years".\[2\]
  - whatsmeow Discussion #1033 (Dec 2025) confirms full-sync events come only at pairing, not on reconnect.\[25\]
  - **iOS vs Android:** no reliable 2025–2026 comparison found. This is a spike item.

- **Inactivity logout.** Green-API's "Device Pairing Setup Guide" FAQ says linked devices "will be disabled if you don't use your phone for more than 14 days. If linked devices are inactive for 30 days, they will be automatically disabled", and Gulf News (2026) reports the same two rules. An always-on capture node avoids the 30-day rule. The 14-day phone rule still applies, for example during holidays when the phone isn't opened.

- **LIDs.** mautrix's July 2026 post says WhatsApp's "migration from phone numbers to LIDs appears to be more or less finished". In August 2026 mautrix switched to "prefer LIDs for all chats, including DMs". WAHA 2026.8.1 added a server query that resolves unknown `@lid` to a phone number.\[26\]\[27\]\[28\] The archive must store the JID exactly as received (LID or PN), store whatsmeow's PN↔LID mappings as their own insert-only rows, and resolve identities at read time. Keying chats on a normalised phone-number JID will split or merge conversations. Tauri-EPO issue #538 shows a media-retry receipt built from a normalised JID may fail to match a LID-addressed chat.\[29\]

- **Undecryptable messages.** whatsmeow emits `events.UndecryptableMessage`; "the library will automatically ask the sender to retry".\[30\]\[31\]\[32\] It sends up to five retry receipts, and from the second retry on these include a fresh prekey bundle.\[33\] Re-requesting from the phone is off by default (`AutomaticMessageRerequestFromPhone`, delayed by 5 seconds when on).\[3\]\[33\] Persist these events as rows, otherwise the gaps can't be seen.\[3\]

- **What whatsmeow sends without being asked.** Per the subagent's source reading of receipt.go, message.go, retry.go and connectionevents.go (Oct 2026), whatsmeow automatically sends:
  - delivery receipts. These are `type="inactive"` unless `SendPresence(available)` or `SetForceActiveDeliveryReceipts(true)` is called.\[10\] The docs say inactive receipts are "transmitted to the sender, but not rendered in the official WhatsApp apps".\[34\]
  - acks and nacks for all stanzas;\[34\]
  - retry receipts;\[33\]
  - `SetPassive(false)` after connecting;\[35\]
  - prekey uploads;\[35\]
  - `hist_sync` and `peer_msg` receipts;\[32\]
  - app-state fetch IQs;\[32\]
  - an immediate "unavailable message" request to your own phone when `<unavailable>` stanzas arrive.\[32\]
  
  Read receipts are never automatic (`MarkRead` must be called).\[34\] So a silent client still sends protocol traffic, but none of it is visible to contacts as long as you never send presence. The fixed constraint holds in spirit. The literal goal of "no send code in the build" cannot be met, because the send code lives in whatsmeow.

- **Expired media.** Use `SendMediaRetryReceipt` (already allowed in the plan), and for history-sync blobs that return 404/410, `SendHistorySyncServerErrorReceipt`.\[10\]\[32\]\[36\] mautrix issue #932 (17 Jun 2026) asks to "throttle automatic old media requests after history sync/backfill".\[37\] Rate-limit bulk re-requests.

- **Backfill older than the sync window.** No 2025–2026 evidence was gathered on merging decrypted phone backups. Plan it as an optional import with `source=backup` and a dedup key. A spike is below.

**Recommendation: keep whatsmeow, and change phase 0 to add six things.**
1. A runtime client-version check using `GetLatestVersion` plus a weekly dependency-bump pipeline.\[21\]
2. Persistence of `UndecryptableMessage` events and turning on phone re-request.\[3\]
3. Storage of LID/PN mappings and JIDs exactly as received.
4. A full-history request with an explicit size limit, and a history-sync progress record so late chunks are accepted and visible.
5. Never calling `SendPresence`. Assert this in tests: `sendActiveReceipts` must stay off.\[34\]
6. Rate-limited media re-requests.

Baileys and whatsapp-web.js are not better choices for a Go/Python developer. Baileys has the same ban profile (issue #810), and a browser engine adds Chromium.

**Confidence:**
- High on version breakage, the history-sync limits (one detailed Android dataset) and auto-sent stanzas.
- Low on ban risk for receive-only use, since there is no direct evidence.

### 3. Module system shape

**Current plan:** manifest-driven subprocess modules in per_job or service mode; jobs keyed by (message, module, version); artifacts chained by kind.

**Evidence:**
- I did not gather primary sources this round on Immich, Paperless-ngx, Karakeep, ArchiveBox, Frigate, Home Assistant or Datasette. **No good evidence was collected here**, so the advice below rests on design reasoning rather than maintainer post-mortems.
- For the queue, **goqite** (maragu.dev/goqite v0.4.0, 20 Nov 2025, MIT, one maintainer) is a "persistent message queue Go library built on SQLite and inspired by AWS SQS". It "brings your own SQLite driver" and has a `jobs` runner with "limit on how many jobs can be run simultaneously", "automatic message timeout extension while the job is running" and "graceful shutdown". Its README notes that "SQLite only allows one writer at a time, so the parallelism just creates write contention."\[38\]\[39\]\[40\]

**Recommendation: change.** Make v1's contract the simplest one that an AI agent can implement from one instruction:
- **Contract:**
  - One folder per module containing `module.toml` (name, version, triggers, produces, settings schema, command).
  - One process per job. The input is one JSON object on stdin: the message, the paths of media it may read, the upstream artifacts and the settings.
  - The output is one JSON object on stdout holding the artifacts. Exit code 0 means success, non-zero means retryable, and a structured `{"error":...,"retry":false}` means permanent failure.
  - Each module has a `fixtures/` folder with input/expected pairs, plus a `archiver module test <dir>` command that runs them. That gives Claude Code a closed TDD loop for each module.
- **Drop `service` mode from v1.** Model warm-up for Whisper is the only real reason for it. Get the same effect inside one job by letting a module accept a batch (`{"jobs":[...]}`), or add service mode later behind the same JSON schema.
- **Queue:** a single `jobs` table with a unique key `(target_id, module, module_version)`, status, attempts, a `lease_until` column and a `next_run_at` column. Either hand-roll it (about 150 lines, fully under your tests) or use goqite for leasing. I lean towards hand-rolling because idempotency has to be tied to your artifacts table in the same transaction.
- **Not worth it:** a workflow engine, HashiCorp go-plugin, WASM/Extism (Python inside WASM is a poor fit for Whisper), and MCP-as-module-contract (heavy, session-oriented, and meant for agents rather than batch jobs).

**Confidence:** medium.

### 4. Multi-machine role split

**Current plan:** hub, capture and worker roles over an authenticated API with node tokens, capability routing, local buffering and embedded tsnet.

**Evidence:** no primary sources were gathered this round on Immich remote ML, Frigate detectors, Tdarr nodes or tsnet's current issues. **No good evidence was collected**, so this rests on reasoning.

The strongest case **for** building the split now:
- The capture node and the heavy compute might live on different machines (the Mac for Whisper speed).
- Defining the hub API early stops in-process shortcuts that make a later split painful.

The strongest case **against**:
- The user has one always-on Ryzen server and no GPU anywhere that matters, so routing work by capability buys nothing today.
- Capture-side buffering duplicates SQLite's durability.
- Node tokens, capability routing and Tailscale each add failure modes to the one component that must not lose messages.
- A remote worker is just a job consumer. If v1's jobs live in SQLite behind an internal `JobSource` boundary and modules are subprocesses, a later remote worker is an HTTP adapter over the same lease, complete and fail calls. That is additive, not a rework.

**Recommendation: replace.** Build one process with internal packages `capture`, `store`, `media`, `jobs`, `web` and `mcp`. The jobs package exposes lease, complete and fail through a Go interface, which is the seam for later. For remote access, document running Tailscale on the host instead of embedding tsnet.

**Confidence:** medium-high.

### 5. Hub, UI and data stack

**Evidence:**
- **PocketBase** is very active but changes fast. There were about 14 stable releases from v0.38.0 (7 May 2026) to v0.39.10 (29 Jul 2026). It is still pre-1.0, and the v0.37.0 UI was "rewritten from scratch", with its new UI-extension APIs "intentionally" left undocumented.\[41\]\[42\] Its collection model and admin UI pull against an insert-only raw schema that you control.
- No practitioner evidence was gathered on templ/htmx/Datastar reliability with agents. Assessment: (c), Go templates or templ with htmx plus SSE, keeps one language, one toolchain and one test framework (`httptest` on HTML responses). For seven mostly tabular screens and an activity feed, that is the lowest-maintenance option for a solo Go developer.
- **Driver.** whatsmeow's `sqlstore` works with modernc.org/sqlite when foreign keys are enabled. whatsmeow issue #454 shows "failed to upgrade database: foreign keys are not enabled" when the mattn-style `_foreign_keys=on` DSN is used with modernc. modernc's syntax is `_pragma=foreign_keys(1)`, and a 2026 whatsmeow guide uses `sqlstore.New(ctx, "sqlite", "file:session.db?_pragma=foreign_keys(1)", ...)`. wacli, by contrast, needs CGO for FTS5 ("wacli uses go-sqlite3, so cgo + a C compiler are required").\[43\]\[44\]\[45\]\[46\]
- **Arabic search.** Do not rely on tokenizer options to fold Arabic. Store a `search_text` column normalised in Go: strip tashkeel (U+064B–U+065F, U+0670), remove tatweel (U+0640), fold أ/إ/آ to ا, ة to ه, and ى to ي. Index it with FTS5 `unicode61`, plus a `trigram` index for substring and dialect-spelling matches. Apply the same normaliser to queries. This is deterministic and unit-testable. Tokenizer behaviour was not verified against primary docs here.
- **Semantic search (sqlite-vec):** defer until the MCP use shows that keyword search fails.
- **Litestream vs `VACUUM INTO`:** not researched this round. Nightly `VACUUM INTO` with an integrity check is adequate for one machine. A message lost between snapshots is still on the phone and can be recovered via re-pair or history sync, as long as the live DB isn't corrupted.

**Recommendation: change** to (c), server-rendered Go with htmx and SSE, and modernc.org/sqlite. Reject PocketBase.

**Confidence:** medium (low on the agent-reliability claims).

### 6. Vibe-coding practice

**Evidence:** practitioner guides from 2026 converge on four points:
- CLAUDE.md is advisory while hooks are deterministic. "CLAUDE.md advises. Hooks overrule." Creeta's "Claude Code Best Practices 2026" guide says CLAUDE.md is "followed roughly 80% of the time" ("practitioners report roughly 80% compliance, not 100%"), and DataCamp's Claude Code best-practices tutorial puts it lower, at "about 70% of the time".
- Anthropic's Claude Code docs ("How Claude remembers your project") say to "target under 200 lines per CLAUDE.md file" because "files over 200 lines consume more context and may reduce adherence"; move procedures into `.claude/skills/` or path-scoped `.claude/rules/`.
- Use Stop hooks to block test deletion. Note that Claude Code ends a turn after 8 consecutive Stop-hook blocks, so a hook is not a wall.\[47\]\[48\]
- Anthropic's own "Claude Code: Best practices for agentic coding" (Boris Cherny, April 2025) recommends documenting commands, style and test instructions.\[47\]

wacli ships an AGENTS.md and `.agents/skills`, and its PRs are machine-reviewed (ClawSweeper).\[1\]\[2\] On testing whatsmeow apps, the subagent found no replay-fixture suite in mautrix-whatsapp. whatsmeow ships `mdtest`, a manual tool used with a real account.\[49\] So there is no off-the-shelf fake server, and you must build your own fixture corpus.

**Recommendation: change** the guardrails and testing approach. Details are in the playbook below.

**Confidence:** medium.

### 7. Minimal reliable capture (phase 0 critique)

**Keep in phase 0:**
- pair/run/status/backup;
- the insert-only store with raw protobuf;
- a single `ingest()` path;
- the media queue;
- logout and disconnect alerts;
- the systemd unit;
- the 7-day zero-missing audit, which is excellent.

**Missing, and needed, in order of importance:**
1. **Client-version drift handling.** Map `ClientOutdated`/405 to a distinct health state and alert. Run the `GetLatestVersion` override at startup.\[21\]\[35\] Use a Renovate/Dependabot weekly whatsmeow bump in CI, and a documented "update the binary" path. This is the most frequent real outage (5 Oct and 14 Sep 2026).
2. **Undecryptable messages.** Store `UndecryptableMessage` as a `message_events` row (kind `undecryptable`, with the decrypt-fail mode). Turn on `AutomaticMessageRerequestFromPhone`.\[3\] Let a later successful decrypt of the same ID become a normal message row. Count these in `status`.
3. **LID handling.** Store JIDs exactly as received, persist PN↔LID mapping events, and dedupe the key `(chat_jid, id)` across the LID and PN forms of the same chat. Otherwise reason (1) in the plan's review focus fails silently.
4. **History-sync accounting.** Record every HistorySync chunk (type, progress, chunk order, oldest timestamp) in a `history_sync_chunks` table. Request a full sync with an explicit `FullSyncSizeMbLimit` (for example 4096) and days limit. Keep running after pairing, because chunks arrive hours later. Report "history complete to date X" in `status`.
5. **Never-online assertion.** Add a test that the bridge never calls `SendPresence` or `SetForceActiveDeliveryReceipts`.\[34\]
6. **Group participant and metadata events**, with app-state events stored raw (pins, archive and mute come through app state). Plan for app-state desyncs: mautrix added automatic recovery in v26.02 and needs `!wa sync appstate` for some cases (v26.09).\[7\]
7. **Time.** Store WhatsApp timestamps as UTC epoch plus server-received time. Display in Asia/Riyadh. Tolerate "nonsensical timestamps" from the phone, which mautrix fixed in v26.09.
8. **Logging.** Write JSON logs to journald (systemd handles rotation), so no in-app log rotation is needed. Add a panic-recover in event handlers: wacli "recover[s] panics in event handlers and media workers instead of crashing the process", and mautrix PR #955 (6 Oct 2026) fixed a logout-race nil-pointer panic during backfill.\[50\]\[51\]
9. **Session-DB backup safety.** A restored stale session.db can break Signal sessions. Document "never restore session.db over a live pairing; re-pair instead".

**Can wait:**
- Telegram (keep only ntfy);
- incremental media copy in backups (rsync or restic can do it);
- disk-free on Windows;
- "selected chats" mode (exclude list only).

**Confidence:** high.

### 8. Arabic engines (secondary)

**Speech-to-text:**
- **NADI 2025** (the first multidialectal Arabic speech shared task, Sep 2025) reports a best overall average of 35.68 WER / 12.20 CER across eight dialects. A participating system reports zero-shot Whisper averaging 93.90 WER on dialectal speech, falling to about 40 WER after dialect fine-tuning.\[52\]\[53\]
- **arXiv 2504.12254** (Apr 2025) reports a multi-dataset average WER of 36.86 for Whisper large-v3 and 40.05 for large-v3-turbo.\[54\]
- **SADA** (Saudi Audio Dataset for Arabic; the SADA paper on IEEE Xplore describes "668 hours of high-quality audio… sourced from 57 television shows provided by the Saudi Broadcasting Authority") remains hard: NVIDIA's Developer Blog post "Fine-Tuning NVIDIA Nemotron for Saudi Arabic Dialects" reports that fine-tuning cut WER "from 55.05% to 29.96%" on a Najdi/Hijazi split and from 58.84% to 35.61% on the full SADA test set, while a separate SADA study reports 40.9% WER for MMS 1B with a 4-gram language model. An Islamic University journal paper (2026) found a best of 0.4031 with Whisper-large plus GPT post-correction, and only modest gains for Hijazi.
- **What this means:** expect off-the-shelf Whisper to make an error in roughly 1 word of every 3 to 2.5 on Saudi voice notes.
- **Not gathered:** CPU speed figures and hosted per-hour pricing.
- **Recommendation:** run large-v3 (not turbo) via faster-whisper or whisper.cpp on the Ryzen as the default. Keep an API adapter, and run the bake-off spike below on 30 real voice notes.

**Translation:** no benchmark was gathered. An LLM API is likely to beat CPU-local models on informal Saudi Arabic, but treat that as unverified until the spike.

**Confidence:** low-medium.

### 9. Packaging (secondary)

wacli is a working example. It ships "prebuilt archives for macOS, Linux, and Windows" through GoReleaser, a Homebrew tap and a Docker image, and its official macOS binaries "require macOS 15".\[1\] A pure-Go (modernc) build makes cross-compiling with GoReleaser trivial, while wacli's CGO choice forces per-platform toolchains. kardianos/service and installer tooling were not researched. Use GoReleaser, a Homebrew tap and a Docker image first. Add a Windows service wrapper only when a real Windows user exists.

**Confidence:** medium.

## Recommended architecture

| Component | Choice | Change from plan | Why |
|---|---|---|---|
| Capture | whatsmeow (latest `main`, Go 1.26+), wrapped in one `internal/wa` package | Add version-drift handling, an undecryptable-event store, LID mapping store, history-chunk ledger, never-online assertion | These are the observed 2026 failure modes |
| Session store | whatsmeow `sqlstore` on modernc.org/sqlite, `_pragma=foreign_keys(1)` | Same | Pure Go; known DSN pitfall (#454) |
| Archive | SQLite WAL, insert-only raw + `message_events` + `id_mappings` + `history_sync_chunks` | Add the two tables; JIDs kept exactly as received | LID migration finished mid-2026 |
| Search | Normalised `search_text` + FTS5 unicode61 + trigram | New | Deterministic Arabic folding |
| Media | Worker queue, rate-limited media re-requests | Add throttle | mautrix #932 |
| Jobs | One SQLite `jobs` table with lease (hand-rolled or goqite v0.4.0) | Simplified | One writer, transactional with artifacts |
| Modules | Per-job subprocess, JSON stdin/stdout, fixtures folder, `module test` command | Drop service mode and capability routing | Smallest contract an agent can follow |
| Topology | Single process, internal `JobSource` seam | Remove hub/capture/worker roles, node tokens, tsnet | No current need; additive later |
| UI | Go `html/template` or templ + htmx + SSE | Replace SPA | One language, no JS toolchain |
| MCP | Read-only server over SQLite views, same binary | Same | — |
| Backups | Nightly `VACUUM INTO` + integrity check | Same; never auto-restore session.db | Adequate for one box |
| Alerts | ntfy only in phase 0 | Defer Telegram | Less code |
| Release | GoReleaser + Homebrew tap + Docker; Renovate weekly whatsmeow bump | New pipeline | Client-version expiry recurs every few weeks |

## Vibe-coding playbook

- **Repo layout:**
  - `cmd/archiver`
  - `internal/{wa,ingest,store,media,jobs,modules,web,mcp,health}`
  - `modules/<name>/` with `module.toml` and `fixtures/`
  - `testdata/events/` containing recorded protobuf fixtures
- **CLAUDE.md:** keep it under about 150 lines, covering commands, invariants and package boundaries. Move procedures ("add a module", "add an event kind", "bump whatsmeow") into `.claude/skills/`.
- **Enforce the never-send rule mechanically, not by grep:**
  1. Only `internal/wa` may import `go.mau.fi/whatsmeow`. Enforce this with golangci-lint `depguard`.
  2. `internal/wa` exposes a receive-only interface (`Events()`, `Download()`, `RequestMediaRetry()`).
  3. `forbidigo` bans `SendMessage|SendPresence|SendChatPresence|MarkRead|SendReaction|RevokeMessage|BuildEdit|SetForceActiveDeliveryReceipts|SendPeerMessage` repo-wide, with a single allow-listed media-retry file.
  4. A PreToolUse/Stop hook runs `golangci-lint` and `go test ./...`, and blocks deletion of `_test.go` files.
  
  Keep the existing grep test as a cheap extra layer.
- **Testing without a fake server:**
  - Build a **record mode** (`archiver run --record testdata/events/`) that writes each raw `events.*` value, in protobuf form, to golden files from your own test group.
  - Build a **replay harness** that feeds them through `ingest()` and compares the DB state with golden SQL dumps.
  - Go-constructed protobufs are fine for unit tests, but real recordings catch wrapper and LID shapes you won't guess. Scrub phone numbers before committing.
  - Add a dedicated test for every mautrix changelog item you adopt (LID race, nonsensical timestamps, new edit/poll formats).
- **Against test gaming:**
  - Have the plan step write tests in a separate subagent from the one that implements.
  - Lock test files on implementation tasks with a hook that rejects edits under `*_test.go` during a "green" step.
  - Use golden-file diffs that need human approval to update (`-update` flag only by hand).
- **Go suits agent-written code** here: one toolchain, strong typing and stdlib `httptest`. Removing the SPA removes the weakest language boundary.
- **Typical agent-built Go failures to guard against:**
  - goroutine leaks on reconnect;
  - unbounded channels;
  - swallowed errors in event handlers;
  - panics in handlers that kill the process;
  - context misuse;
  - more than one SQLite writer.
  
  Add `-race` to CI, a single writer goroutine for the archive, and `goleak` in tests.
- **Phase plan:** run phase 0 as "capture hardening" (below) and gate it on the 7-day audit. Phase 1 becomes jobs, modules and transcribe. Phase 2 is the minimal htmx UI, search and MCP. The wizard and installers come last.

## Red flags and the bear case

- **The whole project depends on an unofficial protocol that breaks on WhatsApp's schedule, not yours.** Two client-version cut-offs in four weeks (14 Sep and 5 Oct 2026) took down unmaintained whatsmeow apps within minutes. A public release for non-technical users means you become their maintainer. If you stop shipping updates for a couple of months, every install stops capturing, and anything missed past the history window is gone.
- **Terms-of-service risk is real and unmeasured.** WhatsApp warns about "unauthorized tools" even for reply-only clients (whatsmeow #810).\[23\] Nobody has published evidence that receive-only is safe, and a ban would cost the student his course channel.
- **History is incomplete by design.** A personal primary refuses on-demand full history (`ERROR_REQUEST_ON_NON_SMB_PRIMARY`), so the archive reaches back at most about three years.\[2\] Re-pairing is the only refill path, and each re-pair risks LID/PN duplicates.
- **"Never send" can't be proven at the binary level.** whatsmeow sends receipts, retries, prekeys and peer messages to your own phone on its own.\[32\]\[33\] What you can prove is narrower: no user-visible sends and no presence.
- **Arabic ASR quality may make transcription marginal.** Even after fine-tuning, NVIDIA's Nemotron work only reached 35.61% WER on the full SADA test set, and a separate SADA study reports 40.9% WER for MMS 1B with a 4-gram language model, so voice-note announcements (room changes, quiz dates) may be mis-transcribed. Never present transcripts as authoritative.
- **Scope risk.** Even trimmed, this is a capture daemon, a job system, a UI, an MCP server and an installer, maintained by one person through an agent. The installer and the public release may cost more than everything else combined.
- **Residual risks even with the changes:** the 14-day phone-inactivity logout; app-state desyncs; media expiry for history older than the phone keeps; and the agent quietly weakening tests.

## What to change, in order

**Before phase 0 starts:**
1. Add version-drift handling: a `GetLatestVersion` override at startup, a `client_outdated` health state and alert, and a weekly Renovate bump with CI.\[21\]
2. Add tables for `undecryptable` events, `id_mappings` (LID↔PN) and `history_sync_chunks`. Keep JIDs exactly as received.
3. Set full-sync device props with explicit days and size limits. Keep running after pairing.
4. Replace the grep-only `nosend` with depguard + forbidigo + a single wrapper package. Add the never-online assertion.
5. Use modernc with `_pragma=foreign_keys(1)`. Add panic recovery in handlers and JSON logs to journald.
6. Build the record and replay harness, and record a fixture corpus from a test group.
7. Read the mautrix-whatsapp v26.01–v26.09 changelog and wacli PRs #441 and #461, and turn each relevant item into a test.

**Before the UI and module phases:**
1. Drop the hub/capture/worker roles, node tokens and tsnet. Add an internal `JobSource` seam.
2. Use a per-job JSON subprocess contract, a fixtures folder and a `module test` command. Drop service mode.
3. Build the UI in htmx with SSE instead of a SPA. Add normalised Arabic FTS.

**Later or optional:**
- Telegram alerts;
- semantic search (sqlite-vec);
- backup imports (WhatsApp-Chat-Exporter);
- Litestream;
- a Windows service wrapper;
- remote workers.

## Spikes to run

1. **History reach.** Pair a test companion with `RequireFullSync`, days=3650 and size=4096 against the real phone, and keep it running for 48 hours. Measure the oldest message per course group and the time until progress reaches 100%. If the course groups reach the start of term, drop backup import entirely.
2. **iOS vs Android.** If the phone is an iPhone, repeat spike 1. If iOS stops much sooner, prioritise backup import.
3. **Version drift drill.** Build with a deliberately old whatsmeow and confirm that the `client_outdated` state, the alert and the `GetLatestVersion` override all work. If the override fails, ship auto-update instructions instead.
4. **Receipt audit.** Using a second phone in a test group, send messages and confirm that no double grey ticks or "online" status ever appear for the archiver device over 24 hours.
5. **LID dedup.** Re-pair the same account twice and count duplicate `(chat, id)` pairs across LID and PN forms. A count above zero means the dedup key must change.
6. **ASR bake-off.** Transcribe 30 real Saudi voice notes with faster-whisper large-v3 (int8, CPU), large-v3-turbo and one hosted API. Measure manual WER on 10 of them, real-time factor on the Ryzen, and cost. If the local model's real-time factor is above 1.0 or its WER is much worse, make the API the default.
7. **Translation bake-off.** Translate 50 informal messages with an LLM API and a CPU-local model, and have a native speaker rank them blind.
8. **modernc FTS5 + trigram.** Confirm that the modernc build has FTS5 and the trigram tokenizer, and that Arabic normalisation queries hit. If not, switch to CGO mattn with `sqlite_fts5`.

## Sources

Sources are named and dated inline throughout the report: the openclaw/wacli README and PRs #441 and #461; the mautrix-whatsapp releases v26.01–v26.09 and mau.fi blog posts for July and August 2026; the whatsmeow pkg.go.dev docs and issues #454, #807 and #810 plus Discussions #199 and #1033; Goberna-Lab release v0.7.0-goberna.7 and the October 2026 client-outdated PRs; lharries/whatsapp-mcp issue #164; WAHA release notes; GOWA releases; the goqite docs; the PocketBase release history; NADI 2025, arXiv 2504.12254 and the SADA-based studies; and 2026 Claude Code practitioner guides. The citation pass attaches the links.

## Sources

1. [GitHub - openclaw/wacli: WhatsApp CLI: sync, search, send](https://github.com/openclaw/wacli)
2. [feat(auth): ask the primary for its full history with --full-history by zarmat99 · Pull Request #461 · openclaw/wacli](https://github.com/openclaw/wacli/pull/461)
3. [fix(sync): ask the primary for messages this device cannot decrypt by zarmat99 · Pull Request #441 · openclaw/wacli](https://github.com/openclaw/wacli/pull/441)
4. [Releases · mautrix/whatsapp](https://github.com/mautrix/whatsapp/releases)
5. [whatsapp/CHANGELOG.md at main · mautrix/whatsapp](https://github.com/mautrix/whatsapp/blob/main/CHANGELOG.md)
6. [Release v26.05 · mautrix/whatsapp](https://github.com/mautrix/whatsapp/releases/v0.2605.0)
7. [Release v26.09 · mautrix/whatsapp](https://github.com/mautrix/whatsapp/releases/tag/v0.2609.0)
8. [Releases · lharries/whatsapp-mcp](https://github.com/lharries/whatsapp-mcp/releases)
9. [10+ PRs fixing the 405 error are sitting unmerged since September · Issue #164 · lharries/whatsapp-mcp](https://github.com/lharries/whatsapp-mcp/issues/164)
10. [whatsmeow package - go.mau.fi/whatsmeow - Go Packages](https://pkg.go.dev/go.mau.fi/whatsmeow)
11. [\[watch\] whatsmeow update available: v0.0.0-20260611094716-089932318bc2 -\> v0.0.0-20261005123207-68df7a5ec7b5 · Issue #13 · Goberna-Lab/whatsmeow-node](https://github.com/Goberna-Lab/whatsmeow-node/issues/13)
12. [fix(bridge): update whatsmeow client version by NIXKnight · Pull Request #2 · NIXKnight/WhatsApp-MCP-Server](https://github.com/NIXKnight/WhatsApp-MCP-Server/pull/2)
13. [Bump whatsmeow: el pin de junio da 405 client outdated by nmoreno-cs · Pull Request #3 · payana-apps/whatsapp-mcp](https://github.com/payana-apps/whatsapp-mcp/pull/3)
14. [Atualiza whatsmeow (ClientOutdated 405) by fereinaux · Pull Request #1 · fereinaux/wuzapi](https://github.com/fereinaux/wuzapi/pull/1)
15. [Bridge: histórico completo no pareamento (opcional) e whatsmeow atualizado by thiavila · Pull Request #5 · thiavila/whatsapp-mcp](https://github.com/thiavila/whatsapp-mcp/pull/5)
16. [whatsmeow/go.mod at main · tulir/whatsmeow](https://github.com/tulir/whatsmeow/blob/main/go.mod)
17. [Release v0.7.0-goberna.7 · Goberna-Lab/whatsmeow-node](https://github.com/Goberna-Lab/whatsmeow-node/releases/tag/v0.7.0-goberna.7)
18. [fix: update whatsmeow (405 outdated) and keep CDN query in media direct path by rodrigopg · Pull Request #19 · rodrigopg/whatsapp-mcp](https://github.com/rodrigopg/whatsapp-mcp/pull/19)
19. [Fix 405 "client outdated": bump whatsmeow + 0.0.9 by janfabian · Pull Request #3 · janfabian/claude-whatsapp](https://github.com/janfabian/claude-whatsapp/pull/3)
20. [fix: bump whatsmeow to fix "client outdated" (405) connect failure by marksmeed · Pull Request #2 · marksmeed/whatsapp-mcp](https://github.com/marksmeed/whatsapp-mcp/pull/2)
21. [whatsmeow/update.go at main · tulir/whatsmeow](https://github.com/tulir/whatsmeow/blob/main/update.go)
22. [Whatsapp Ban · tulir/whatsmeow · Discussion #199](https://github.com/tulir/whatsmeow/discussions/199)
23. [⚠️ "Your account may be at risk" warning affecting clients using WhatsMeow (also reported with Baileys) · Issue #810 · tulir/whatsmeow](https://github.com/tulir/whatsmeow/issues/810)
24. [P1: bounded initial history sync (days/size at pair time, max age on ingest) and messages.db size metrics with a warning threshold · Issue #645 · Tauri-EPO/whatsapp-mcp](https://github.com/Tauri-EPO/whatsapp-mcp/issues/645)
25. [Full History Sync on reconnection · tulir/whatsmeow · Discussion #1033](https://github.com/tulir/whatsmeow/discussions/1033)
26. [July 2026 releases // New Instagram bridge and OAuth in gomuks - Tulir Asokan](https://mau.fi/blog/2026-07-mautrix-release/)
27. [August 2026 releases // Bandwidthmuks 2.0 and mascots - Tulir Asokan](https://mau.fi/blog/2026-08-mautrix-release/)
28. [Releases · devlikeapro/waha](https://github.com/devlikeapro/waha/releases)
29. [P2: unverified: the media-retry receipt is built from the normalised chat JID and a bare sender; a LID-addressed chat may not match · Issue #538 · Tauri-EPO/whatsapp-mcp](https://github.com/Tauri-EPO/whatsapp-mcp/issues/538)
30. [events package - go.mau.fi/whatsmeow/types/events - Go Packages](https://pkg.go.dev/go.mau.fi/whatsmeow/types/events)
31. [Receiving and Decrypting Messages](https://deepwiki.com/tulir/whatsmeow/3.1-receiving-and-decrypting-messages)
32. [whatsmeow/message.go at main · tulir/whatsmeow](https://github.com/tulir/whatsmeow/blob/main/message.go)
33. [whatsmeow/retry.go at main · tulir/whatsmeow](https://github.com/tulir/whatsmeow/blob/main/retry.go)
34. [whatsmeow/receipt.go at main · tulir/whatsmeow](https://github.com/tulir/whatsmeow/blob/main/receipt.go)
35. [whatsmeow/connectionevents.go at main · tulir/whatsmeow](https://github.com/tulir/whatsmeow/blob/main/connectionevents.go)
36. [whatsmeow/mediaretry.go at main · tulir/whatsmeow](https://github.com/tulir/whatsmeow/blob/main/mediaretry.go)
37. [Issues · mautrix/whatsapp](https://github.com/mautrix/whatsapp/issues)
38. [GitHub - maragudk/goqite: Go queue library built on SQLite and inspired by AWS SQS. · GitHub](https://github.com/maragudk/goqite)
39. [goqite package - maragu.dev/goqite - Go Packages](https://pkg.go.dev/maragu.dev/goqite)
40. [jobs package - maragu.dev/goqite/jobs - Go Packages](https://pkg.go.dev/maragu.dev/goqite/jobs)
41. [Releases · pocketbase/pocketbase - GitHub](https://releasealert.dev/github/pocketbase/pocketbase)
42. [pocketbase/CHANGELOG.md at master · pocketbase/pocketbase](https://github.com/pocketbase/pocketbase/blob/master/CHANGELOG.md)
43. [wacli module - github.com/steipete/wacli - Go Packages](https://pkg.go.dev/github.com/steipete/wacli)
44. [panic: failed to upgrade database: foreign keys are not enabled · Issue #454 · tulir/whatsmeow](https://github.com/tulir/whatsmeow/issues/454)
45. [sqlite package - modernc.org/sqlite - Go Packages](https://pkg.go.dev/modernc.org/sqlite)
46. [whatsmeow Guide: Go WhatsApp Library Tutorial - Whatsapp Checkleaked](https://whatsapp.checkleaked.cc/blog/whatsmeow-go-whatsapp-libraries)
47. [The Definitive Claude Code Playbook (June 2026 Edition)](https://www.rohitcodes.com/blogs/the-definitive-claude-code-playbook-2026)
48. [Claude Code Best Practices 2026: Slash Commands, Hooks, Subagents](https://news.creeta.com/en/claude-code-best-practices-2026-hooks-subagents/)
49. [mdtest command - go.mau.fi/whatsmeow/mdtest - Go Packages](https://pkg.go.dev/go.mau.fi/whatsmeow/mdtest)
50. [backfill: prevent panic when converting history sync messages by Fizzadar · Pull Request #955 · mautrix/whatsapp](https://github.com/mautrix/whatsapp/pull/955)
51. [wacli/CHANGELOG.md at main · steipete/wacli](https://github.com/steipete/wacli/blob/main/CHANGELOG.md)
52. [Effective Dialectal Arabic Speech Processing under Data ...](https://aclanthology.org/2025.arabicnlp-sharedtasks.102.pdf)
53. [NADI 2025: The First Multidialectal Arabic Speech Processing Shared Task](https://arxiv.org/html/2509.02038v1)
54. [Advancing Arabic Speech Recognition Through Large- ...](https://arxiv.org/pdf/2504.12254)
