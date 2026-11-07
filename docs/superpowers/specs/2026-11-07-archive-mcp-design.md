# Standalone archive MCP bridge

Date: 7 November 2026. Status: approved for implementation by Hamza's
"go ahead with production" instruction; Linux installation and Hermes setup
documentation explicitly requested. No remote deployment is performed here.
This document authorizes no deployment and contains no live archive data.
Builder check: an independent software-engineering reviewer reported
"nothing important left" on 7 November 2026. This is design review, not
verification of implemented behavior or today's Ryzen installation.

## Intent and agreed constraints

Hamza wants an MCP bridge installed on Ryzen, alongside the already-running
WhatsApp archiver. Hermes will later run on the same machine and consume it.
Hamza chose the separate database-reader approach on 7 November 2026.

The bridge must work without reinstalling, replacing, restarting, re-pairing,
or migrating the existing archiver. It must not change the archive schema,
add indexes, run checkpoints, or become another database writer. Capture
continues independently of whether Hermes or the bridge is running.

The bridge supplies evidence; Hermes supplies reasoning, summaries, digests,
and any connection to OpenRouter. Neither Hermes nor an API key is needed
to install or test the bridge with a local MCP client.

## Evidence from this checkout

- `docs/v1/STATE.md` and the 26 October week review record build `a87a30f`
  on Ryzen. This is historical evidence, not verification of today's server.
- The deployed pre-V1 schema has `messages.media_type`,
  `participants.display_name`, and no built-in FTS tables. Its SQL is preserved
  in `internal/store/testdata/pre_v1/`.
- This checkout instead has the V1 schema in
  `internal/store/migrations/0001_v1.sql`, with typed messages, canonical
  identities, chat statistics, and English/Arabic search indexes.
- The current `store.OpenReadOnly` deliberately refuses pre-V1 databases.
  Calling it directly would defeat the no-upgrade requirement.
- Current web handlers return HTML, not a public JSON/MCP read API.
- The service account normally owns a private data directory containing
  both `archive.db` and the forbidden `session.db`.

Do not assume the server has this checkout's schema. Discover its layout
through a metadata-only inspection during a separately authorized install.

## Approaches and decision

1. **Selected: a separate Go MCP executable reading local SQLite.** Build it
   from this repository, with independent read adapters for deployed legacy
   and V1 layouts. Reuse pure normalization where appropriate. Advantages:
   no change to capture, no network API, no archive redeploy. Costs: support
   two read layouts, provide file permissions, and accept slower legacy scans.
2. **Not selected: an archiver read API plus an MCP connector.** Centralizes
   database access but requires changing/redeploying the running archiver,
   an API contract, and access control. Reconsider for remote consumers.
3. **Deferred: a separately maintained search index.** Could accelerate the
   legacy archive without modifying it, but duplicates private content and
   needs reliable reconciliation of edits, aliases, and backfilled history.
   Not necessary for the first bridge; no hidden indexer or polling daemon.

This is a companion extension to the after-V1 MCP goal (FR-11), not execution
of the V1 upgrade plan or its fresh-archive trial. It deliberately departs
from the older charter's same-binary MCP preference to meet Hamza's chosen
deployment constraint. It does not adopt the anytopdf trial or module system.

## Scope

First release: discover permitted chats, search stored message text, retrieve
messages and surrounding conversation, inspect attachment metadata, and report
what the database can and cannot establish about coverage.

Excluded: sending or pairing, WhatsApp access, SQL supplied by a client,
settings changes, bulk exports, attachment bytes/paths, OCR, transcription,
translation, embeddings, cloud calls, web UI changes, scheduling, and Hermes
installation. These can be separate future features; text search does not
search inside a PDF or audio recording merely because it is attached.

## Process and source layout

Proposed additions: `cmd/archiver-mcp/`, `internal/archiveread/`,
`internal/mcpbridge/`, synthetic tests, dedicated build/check targets,
operator documentation, and bridge-specific installation helpers.
The existing `cmd/archiver` and capture packages need no behavior changes.

Use the official Go MCP SDK and existing pure-Go SQLite dependency, with
versions pinned during implementation. Do not hand-roll MCP negotiation.
The standalone executable offers `serve`, `inspect`, `catalog`, and `version`:

- `serve --config PATH`: MCP over stdio; Hermes starts it as a subprocess.
- `inspect --config PATH`: metadata-only readiness report, including schema,
  permission/WAL availability, policy validity, and supported capabilities.
- `catalog --config PATH`: an operator-only local command listing available
  chat names and identifiers for choosing the policy; never message text.
  This is not exposed as an MCP tool or permitted by the Hermes launcher.
- `version`: bridge revision and supported schema families.

Only protocol messages go to stdout in `serve`. Redacted diagnostics go to
stderr. EOF, cancellation, and SIGTERM close transactions and exit. No
background indexing, browser launch, outbound socket, or idle database read
loop is required. One bridge process and one in-flight archive request are
allowed per installation; a local process lock rejects a second process.

Use stdio initially, with no listening port or always-on MCP web service.
Other MCP clients can use the same command contract. Remote HTTP is deferred.

## SQLite compatibility and capture protection

Implement a dedicated read-only opener; do not weaken the existing store's
schema rejection or invoke its migration/open-for-writing functions.
Open a properly escaped local SQLite URI with `mode=ro`, enable connection-
local `query_only`, disable extension loading, and allow only compiled,
parameterized SELECTs and explicitly allowlisted introspection operations.
No `ATTACH`, arbitrary PRAGMAs, DDL, repair, checkpoint, or migration path.

Inspect `sqlite_schema`, required column names, and the schema marker:

| Layout | Recognition | Reader behavior |
| --- | --- | --- |
| Legacy | No V1 marker; expected legacy tables and columns present | Legacy adapter; scans where needed; unavailable facts are null |
| V1 | `archive_meta.schema=v1` and required V1 columns present | V1 adapter; optional FTS only when its expected structure is present |
| Unknown/incomplete | Neither supported fingerprint, including an unknown explicit marker | `UNSUPPORTED_SCHEMA`; stop without modifying anything |

Version numbers alone are insufficient because both families can record
migration version 1. Do not decode `raw_proto` to fill missing legacy fields.

Live WAL access must include the actual `archive.db-wal` and `archive.db-shm`
siblings when present. Never copy `archive.db` alone or open a changing
database with `immutable=1`. Use a fresh short read connection/transaction
per request, including identity/policy checks and result reads in one
snapshot; close it before replying. This avoids a pinned snapshot while an
agent thinks. A stopped/transitioning WAL archive that cannot be opened
read-only returns `ARCHIVE_UNAVAILABLE` and may be retried; no writable or
immutable fallback. Do not create missing database/WAL/SHM files.

Start with a 2-second total request deadline, a 250 ms busy timeout, and one
reader. The deadline covers authorization, queries, iteration, and formatting;
cancellation must interrupt the SQLite operation. Handle a database replaced
at the configured path by checking its file identity and schema again before
the next request; invalidate cursors. A changed mapping/policy also invalidates
pagination rather than silently widening the result set.

Legacy scans may exceed the deadline for broad searches or conversation
sorts. Return `QUERY_TOO_BROAD` with suggestions to narrow chat/date ranges;
never label an interrupted scan as an exhaustive empty result. Benchmark
before promising latency. No installation step may add an index to fix this.

## Identity, message state, and search semantics

Compute alias connected components from `id_mappings` for legacy; do not rely
on its old `chat_aliases` view for multi-LID cases. Prefer the smallest LID
as the displayed canonical ID, matching V1. Preserve original chat/sender
IDs and deduplicate a message by component and message ID, choosing the
earliest received row then stored chat ID for ties. An input chat or sender
alias resolves through the same component calculation. Apply policy to the
whole component; any explicit deny takes precedence over an allow.

Project current message state using edits and revocations across aliases.
Latest edit order is `(ts, received_at, event_id)`; a known revocation wins.
Return a deletion placeholder without its prior text, caption, attachment
details, or event bodies. Historical versions and deleted content are not
exposed in the first release. Reactions are optional metadata, never evidence
that an event or promise was completed. Quoted references outside permitted
chats must not cause expansion or disclose their content.

Use `store.Fold` semantics for English case and Arabic spelling/diacritics.
`search_messages.query` is literal text, not SQL, regex, or FTS grammar.
Match the complete folded query as a substring of the current permitted
body/caption; no stemming or semantic similarity is promised. The legacy
adapter can evaluate folded candidates in Go under the request budget.
The V1 adapter can use FTS to generate candidates, but must correctly handle
positive message and negative edit row IDs, then verify matches against the
current projected text. Old text alone cannot make a revoked/edited message
match. A scan fallback supplies equivalent semantics for short strings or
queries FTS cannot represent; partial scans return the explicit budget error.

Search covers text/captions and, when available as structured fields,
attachment filenames. It does not parse vCards, raw protocol payloads, or
attachment contents. Legacy `text` may already contain a media caption;
report only what structured columns establish. Unknown `from_me`, media
filenames, or coverage facts remain null rather than guessed.

## MCP tool contract

Expose six tools, each marked read-only and non-destructive. Reject unknown
arguments. Tool annotations describe intent; enforcement is in the reader.

| Tool | Arguments | Result |
| --- | --- | --- |
| `archive_status` | none | Schema family, supported features, effective policy mode, query limits, and coverage caveats; no unrestricted totals or chat names |
| `list_chats` | `query?`, `limit?`, `cursor?` | Permitted chat IDs, names, types; page sorted by canonical ID, without expensive message counts |
| `search_messages` | `query`, `chat_id?`, `sender_id?`, `from?`, `to?`, `all_time?`, `limit?`, `cursor?` | Matching current messages, snippets, source references and exact applied scope |
| `read_conversation` | `chat_id`, `from?`, `to?`, `limit?`, `cursor?` | A chronological conversation window, current state and source references |
| `get_message` | `message_ref` | Current projected message, sender, timestamps, quoted reference, and safe attachment metadata |
| `get_context` | `message_ref`, `before?`, `after?` | Up to 10 messages on each side of the anchor in the same permitted chat |

Dates are RFC3339 instants with explicit offsets; bounds are `from <= ts < to`.
Invalid/reversed bounds are rejected. Search with no bounds defaults to the
last 30 days. `all_time=true` is exclusive with explicit bounds. Conversation
reads with no bounds return the most recent page, presented oldest first;
their cursor retrieves the next older page. Date-bounded conversation reads
start at the lower bound and page forward. Both modes state their direction.

Defaults: 20 search/chat results or 50 conversation messages; hard maximum
100 items except context (21 including anchor). Queries and name filters are
limited to 512 Unicode characters. Search ordering is message timestamp,
stored chat ID, message ID, descending. Conversation ordering uses the same
keys ascending within the returned window; expose no offset pagination.

Each successful result includes `schema_version` (response contract, initially
`1`), `data`, `scope`, `observed_at`, `warnings`, `next_cursor`, and `has_more`.
The scope records effective chat/date/sender restrictions. Cursors encode
ordering keys, filter hash, policy/mapping version, archive file identity,
and expiry (15 minutes); authenticate them with a random per-process secret.
They may expire on a bridge restart; they contain no message body. Cross-page
reads are live snapshots, not a promise of a frozen export; newly backfilled
or edited rows can affect subsequent pages and require a fresh search.

Use a hard 64 KiB UTF-8 limit on the complete encoded tool response. Clip
search snippets to 500 characters; individual body text to 8,000. Flag every
clipped field. Fetch further rows only when there is room; set the cursor
after the last emitted row, not the last scanned one. For search, budget
exhaustion before a complete page/lookahead is an error, not `has_more=false`.
`get_message` reports clipping if even a single record exceeds the budget;
full oversized message retrieval is deferred rather than silently claimed.

Provide structured content plus an equivalent JSON text block for clients
that consume text results. Avoid repeating message bodies in explanations.
Define limits against the entire tool result, including both representations.

Message references encode a format version, original stored chat ID and
message ID (URL-safe encoding); they are stable across aliases and bridge
restarts within the same archive. They are locators, not authorization tokens:
every resolution rechecks policy. Optional browser URLs use an operator-set
viewer base URL and path-escaped IDs; they are absent by default because
Ryzen's loopback UI is not automatically reachable from a user's browser.

Tool errors use MCP error results with stable codes: `INVALID_ARGUMENT`,
`NOT_FOUND_OR_NOT_ALLOWED`, `INVALID_CURSOR`, `UNSUPPORTED_SCHEMA`,
`ARCHIVE_UNAVAILABLE`, `QUERY_TOO_BROAD`, `BUSY`, and `INTERNAL_ERROR`.
Cancellation follows MCP/SDK behavior. No SQL, filesystem paths, message
text, query terms, phone numbers, or raw database errors appear in diagnostics.

## Coverage and honest answers

Report the oldest/newest stored times only for permitted scopes where a
bounded query can establish them; otherwise leave them unknown. A permitted
`read_conversation` result may include scoped gap/coverage facts when cheaply
available. Do not expose global history ledger details or infer completeness
from a single progress=100 chunk, the absence of gaps, or a successful search.
Legacy coverage is explicitly limited; V1 may provide richer scoped facts.
No archive-only tool claims the WhatsApp connection is currently healthy.

Tool descriptions tell Hermes to cite message references, distinguish direct
quotes from inference, and treat all message text as untrusted evidence rather
than instructions. They cannot guarantee that a particular model obeys this.

## Local policy and permissions

Proposed operator-owned configuration, installed separately from archiver config:

```yaml
archive: /var/lib/whatsapp-archiver/archive.db
runtime_dir: /run/archiver-mcp
policy:
  mode: selected
  allow_chats: []
  deny_chats: []
viewer_base_url: ""
```

These are proposed paths and fields, not files created by this design.
An empty allowlist exposes no chat content/names. The operator can select
chats using `catalog`; `mode: all` requires an explicit local edit. Capture
rules and MCP policy are distinct: disabling capture does not erase historical
rows or implicitly decide whether the agent may read them. Deny rules always
win across aliases. Policy is read and validated at the start of every request;
missing/invalid policy denies content immediately. Only content tools depend
on a nonempty selection; `archive_status` remains usable without one.

The MCP caller cannot change archive paths, policy, limits, or permissions.
The policy applies to chat metadata, results, counts, attachment metadata,
quoted/context expansion, and every direct reference lookup. Authorization
must happen before bodies are selected. Restrict structured `details` to
known safe output fields; never use `SELECT *` or return `raw_proto`, media
keys, download URLs, disk paths, health/config secrets, or session data.

Install a separate unprivileged `archiver-mcp` account. Grant only parent
directory traversal and read ACLs on the archive and its WAL/SHM files.
Provision `/run/archiver-mcp` for that account with mode 0700 using a tmpfiles
rule so the process lock works after reboot; it stores no archive content.
Do not make the data directory world-readable, grant the Hermes account
archive access, or use a directory default ACL that also exposes session.db.
A small root-owned systemd path helper maintains read ACLs on these three
exact regular filenames when the archiver recreates them. It refuses symlinks,
never opens message content, and never modifies data bytes or the archiver
unit. A missing file means temporarily unavailable, not a wider permission
grant. Fail closed until the ACL is restored; verify this on WAL recreation.

Hermes uses a root-owned fixed launcher running `serve` as that account through
an exact sudoers command. The launcher accepts no caller arguments, clears
unneeded environment, uses fixed absolute executable/config paths, and cannot
run `catalog`, a shell, or arbitrary commands. The bridge and config are not
writable by Hermes or the reader account. The helper is installation work,
not something this design session runs. Keep its access/cleanup reversible.

This controls the bridge's access. Hermes independently given shell/file tools
and broad host permissions could bypass it; MCP policy is not a sandbox for
the whole agent. Likewise stdio does not detect a cloud-backed client: enabling
chat access permits that MCP client to receive its contents. OpenRouter
privacy/provider selection belongs in the later Hermes configuration.

## Installation, verification, and rollback contract

The eventual installer must never invoke `archiver pair`, `archiver run`,
`rebuild-derived`, migrations, service restart/reload, or the V1 trial.
No container, network route, remote deployment, or PIN change is needed.

1. Record current service PID/start time, binary revision, archive schema
   fingerprint and file ownership/mode, using metadata only. Confirm paths
   locally; do not trust the historical server address in repository notes.
2. Install the separate stamped/checksummed binary, root-owned config/launcher,
   reader account, targeted ACLs, and ACL-maintenance unit. Store permission
   changes needed for exact rollback; do not copy or inspect session.db.
3. Run `inspect` as the reader. Test initialize/list-tools/status with a local
   MCP client and an empty policy. This works before Hermes is installed.
4. Let Hamza choose permitted chats locally. An operator can verify actual
   content queries; an implementation agent uses synthetic data and must not
   read live message text. Confirm capture's PID/start time stays unchanged.
5. Later add this proposed Hermes configuration (not applied now):

   ```yaml
   mcp_servers:
     whatsapp_archive:
       command: /usr/local/bin/archiver-mcp-launch
       args: []
   ```

Hermes launches the bridge when connecting; installation alone does not leave
an MCP process polling the archive. Removing the Hermes entry and bridge
files/account/helper/ACL additions leaves the archiver and its data intact.
Preserve any pre-existing ACLs rather than resetting permissions wholesale.

## Acceptance and builder discretion

Implementation requires synthetic tests for both schema families, including:

- MCP initialization, discovery, structured/text results, error handling,
  cancellation, EOF cleanup, stdout purity, size limits, and cursor validation.
- WAL reads alongside active synthetic inserts/edits and a stopped/restarted
  synthetic writer; observe newly committed rows without reader restarts.
- Attempts to write, create a missing archive, migrate, checkpoint, open a
  session file, load an extension, or bypass policy fail. Verify unchanged
  schema/rows on a quiescent fixture; with a concurrent writer distinguish its
  expected changes from bridge activity rather than comparing a whole-file hash.
- Legacy and V1 mappings, duplicate IDs, edits/revocations, literal punctuation,
  Arabic variants, short queries, null fields, dates/ties, and backfilled history.
- Every tool rejects denied aliases and direct references without revealing
  whether forbidden content exists. Policy removal takes effect next request.
- Policy enforcement before fetching bodies; safe attachment fields; query
  and message contents absent from captured diagnostics even on errors.
- Legacy and V1 300k-message fixtures: record search/window latency, bounded
  cancellation, writer progress and WAL growth. Bridge failure must not
  interrupt capture. Tune SQL or report the legacy limit; never change the
  live archive to make this benchmark pass.
- ACL helper symlink rejection, exact-file scope, WAL recreation, fixed-launcher
  argument rejection, and rollback that preserves pre-existing permissions.

Use the repository's red/green workflow and full `make check` before code
commits. New bridge tests/builds must be integrated into these gates; the
existing archiver's cross-platform builds must remain supported. The first
bridge install target is Ryzen/Linux amd64; other bridge platforms are later.

Builder chooses SDK version, internal interfaces/file splits, the concrete
cursor encoding, and SQL plans consistent with these contracts. Default
limits, selected-chat policy, legacy support and no-upgrade behavior above
are the designer's proposals for Hamza to approve, not previously agreed facts.
If broad legacy search cannot be useful within capture-safe budgets, report
the measured limitation and propose the deferred side index as a new design.

## Future module artifacts

Treat module artifacts as an optional reader capability, following charter
section 10. Add a generic artifact reader interface and MCP tools
`list_artifacts(message_ref)` and `get_artifact(artifact_ref)`. Before an
artifact schema is implemented in the archiver, advertise
`artifacts: false` and return `FEATURE_UNAVAILABLE` from these tools rather
than inventing a database layout. Artifact DTOs preserve kind, language,
text/structured content, source message, module/version and creation time.
Future adapters implement these two generic operations; new module kinds do
not require a tool per module. They must apply message/chat policy and
revocation protection to derived outputs too. Artifact-aware search is a
future additive capability, not claimed by the first release. An unknown
archive schema still needs an explicitly tested adapter; compatibility is
not automatic. Read-only access never schedules processing jobs.

## References

- Project: `AGENTS.md`, V1 plan/schema, `docs/v1/STATE.md`, legacy SQL fixtures,
  and charter ADR-1, ADR-28, ADR-34, CON-1/2/7, FR-11.
- [Hermes MCP configuration](https://hermes-agent.nousresearch.com/docs/user-guide/features/mcp).
- [MCP stdio transport](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports).
- [Official Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk).
- [SQLite WAL and read-only access](https://sqlite.org/wal.html).
