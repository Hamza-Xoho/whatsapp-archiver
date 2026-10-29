# V1 UI specification (W6.0)

Status: approved by Hamza, 29 Oct 2026, with the recommended answers in section 2. This document specifies W6(a)–(g); W6(h) supplies its test data and W6(i) evaluates the finished UI.

Sources: `docs/plans/2026-10-26-v1-plan.md` W6, `docs/v1/schema.md`, ADR-19, ADR-34, ADR-39, and the existing `internal/web` handlers, templates and CSS.

## 1. Product shape

The UI is a private, loopback-only window into one archive. Its three primary areas are **Chats**, **Logging**, and **Pairing**. It is for operating and reading the logger, not administering a general server.

Keep ADR-19:

- Go `html/template` renders complete pages and fragments.
- Vendored htmx adds fragment swaps and progressive loading.
- Server-Sent Events (SSE) update only pages with live information.
- There is no JavaScript toolchain. Small vendored or hand-written scripts are
  allowed for htmx, SSE, theme choice, connection state, and focus repair.
- Every read and essential write works without JavaScript. Infinite scroll has
  paging links, forms submit normally, and errors render as pages.

Reuse the current foundations: host/origin guard, pre-parsed page templates, `pathEscape`, row-level out-of-band swaps, the activity bus, pairing state, media containment checks, cached storage figures, settings forms, and the existing trace view. Replace the old six-item navigation; fold Dashboard, Storage, Backups, and Settings into Logging.

### Information architecture

```text
WhatsApp Archiver
├── Chats                 /chats
│   ├── chat list         /chats
│   ├── global search     /search
│   ├── viewer            /chats/{escaped-jid}
│   ├── message trace     /chats/{escaped-jid}/m/{escaped-id}
│   └── export            /chats/{escaped-jid}/export
├── Logging               /logging
│   └── Settings tab      /logging/settings
└── Pairing               /pair
```

`/` redirects to `/chats`. Old UI URLs may redirect to the matching Logging tab during V1, but do not remain in the primary navigation.

## 2. Decisions

Hamza accepted all recommendations on 29 Oct 2026:

1. UI chrome is English in V1. User content uses `dir="auto"`; full Arabic
   localisation is after V1.
2. Theme follows the operating system by default. A Light / Dark / System
   control is stored in the browser only and is applied before paint.
3. The Logging live feed contains metadata only: time, chat display name, JID
   when needed, event kind, and state. It never contains message text, captions, URLs, or media thumbnails.
4. A community rule applies to current children and is inherited by newly
   discovered children until a child is explicitly overridden.
5. Chat exports include deleted text, edit history, reactions and other
   archived events, labelled as such. They exclude `raw_proto`, secrets, health records and every other chat.
6. HTML exports embed thumbnails and downloaded media within fixed limits;
   oversized items remain authenticated download links and are listed as omitted. WhatsApp ZIP export continues when media is missing and includes a missing-media manifest.
7. There is no service worker or offline archive cache. On connection loss the
   rendered page stays readable, writes are disabled, “Live updates paused” appears, and SSE retries automatically.

Existing schema decisions also apply: a deleted message is a placeholder by default with a “Show deleted text” control; deleted messages remain searchable and are marked deleted; times use the configured archive time zone.

## 3. Shell and shared behaviour — W6(a)

### Desktop wireframe

```text
┌──────────────────────────────────────────────────────────────────────┐
│ WhatsApp Archiver   Chats   Logging   Pairing        Theme: System  │
├──────────────────────────────────────────────────────────────────────┤
│ [Not paired / Live updates paused banner, only when applicable]     │
│                                                                      │
│ page heading                                      page-level action │
│ ┌──────────────────────────────────────────────────────────────────┐ │
│ │ page content                                                     │ │
│ └──────────────────────────────────────────────────────────────────┘ │
│                                                        [toast]       │
└──────────────────────────────────────────────────────────────────────┘
```

- The active primary link has `aria-current="page"` and a visible treatment
  that does not rely on colour.
- Content width is 72rem for tables and 60rem for prose. The chat viewer may
  use the full available height.
- The header is sticky only when it does not obscure focused content.
- SSE is opened by Logging, Pairing while pairing is active, and a viewer at
  the live edge. Chats list, search, exports, settings and trace do not open it.
- Every JID and message ID in a path is passed through `pathEscape`. A viewer,
  trace, export, or fragment for an unknown canonical chat returns 404.
- The layout contains no fallback `#feed`; a feed exists only on Logging.

### Middleware and errors

The handler order is: **guard → recover → timing log → router**. Recovery logs a redacted request method and route pattern, never query values, JIDs or form contents, then returns a plain 500 response. Timing logs method, route pattern, status, response bytes and duration. The guard remains outermost so rejected requests do not reach application middleware.

An htmx failure creates one global toast, focuses it only for an action the user initiated, and leaves the replaced content intact:

> That change did not go through. Your previous setting is still shown.
> Try again.

Use the safe server response when it is a short 4xx explanation. Never put a raw database, network or WhatsApp error in the page. Toasts have a dismiss button, use `role="alert"` for failures and `role="status"` for success, and do not disappear automatically while hovered or focused. A normal form POST redirects to a page with the same result copy.

### Shared states and copy

| State | Presentation | Required copy / behaviour |
|---|---|---|
| Initial loading | Complete server page; a local `aria-busy="true"` skeleton only after an htmx request starts | Keep the previous result visible; “Loading…” is visually hidden unless the wait exceeds 300 ms |
| Empty | Calm panel with the next useful action | Never show a blank table or zero-only dashboard |
| Read error | Page or fragment keeps its heading and shows a retry link/button | “The archive could not be read. Try again.” |
| Write error | Existing value remains; toast and inline error next to the form | Never optimistically show an unsaved rule |
| Offline | Persistent amber banner; rendered reads remain; write controls disabled | “Live updates paused. Check the SSH tunnel or archiver. This page will reconnect automatically.” |
| Reconnected | Remove banner; refetch only stale live fragments | One polite status announcement: “Live updates resumed.” |
| Not paired | Global banner links to Pairing | “WhatsApp is not paired. New messages are not being archived.” |

The browser treats an SSE error as “offline” only after two failed reconnects or 10 seconds, so a quick reconnect does not flash a warning. A successful ordinary request also clears an obsolete offline state. Disabling writes is a usability guard, not security; the server still validates every request.

## 4. Responsive, theme, language and accessibility

### 375 px mobile

At 375 CSS pixels:

- Header brand occupies one row; the three navigation links form a second row
  with at least 44×44 px targets. The theme selector is in a labelled menu.
- No page scrolls horizontally. Tables become labelled row cards; do not hide
  status or actions merely with `nth-child` rules.
- Filters open in a native `<details>` panel. Active filters remain visible as
  removable chips outside it.
- Bulk actions use a sticky bottom bar only after a selection. It never covers
  the focused row or the final message.
- Viewer bubbles use at most 88% width; media uses the bubble width; controls
  wrap rather than shrink below 44 px.
- Logging cards are one column. Wide tables become key/value cards, preserving
  source order and headings for screen readers.

### Theme

System is the default and continues using `prefers-color-scheme`. Light and Dark set a `data-theme` attribute. Store only `system`, `light`, or `dark` in `localStorage`; no archive setting is created. All text and controls meet WCAG 2.2 AA contrast. Status uses icon + word + colour. Respect `prefers-reduced-motion`; htmx swaps and toasts do not animate in that mode.

### Arabic and bidirectional content

- Put `dir="auto"` on message text, captions, quoted text, poll text, contact
  names, location names, chat names, search excerpts and event text.
- Keep timestamps, status labels, IDs and controls in the page direction.
- Isolate mixed-direction names and identifiers with `<bdi>`.
- Search highlighting wraps matching text without changing its direction.
- Do not right-align content merely because it contains Arabic; the browser's
  bidi algorithm and `dir="auto"` decide.

### Keyboard and screen reader contract

- A “Skip to main content” link is first in focus order. Focus indicators are
  never removed. Heading levels do not skip.
- Native links, buttons, checkboxes, `<details>`, dialogs and form fields are
  preferred. Icon-only buttons have visible tooltips and accessible names.
- After an htmx row swap, focus returns to the initiating control (or its new
  counterpart). After prepending viewer messages, focus and scroll position do not move. Search next/previous moves focus to the matched message.
- Infinite-scroll sentinels are not the only controls: “Load older messages”
  and “Load newer messages” buttons are keyboard reachable.
- The live feed uses `role="log"`, `aria-live="polite"`, and
  `aria-relevant="additions"`. It does not steal focus. Health panels are not live regions; only a newly appearing inactivity warning has a dedicated polite region and an unchanged warning is not re-announced.
- Counts and table updates have a short `role="status"` summary, e.g.
  “12 chats shown” or “Logging stopped for 3 chats.”
- QR replacement does not re-announce its full image alt text every 20 seconds.
  A separate polite status says “A new QR code is ready.”

## 5. Chats list — W6(b)

### Desktop wireframe

```text
Chats                                      [Search messages]
[Find a chat________________] [Filters (2)] [Clear]
[Type: Group ×] [Has failed media ×]          42 chats

☐ Select page   [Select all 42 filtered]
┌──┬────────────────────┬─────────┬────────────┬──────────┬──────────┐
│  │ Chat               │ Last    │ Messages   │ Logging  │ Media    │
├──┼────────────────────┼─────────┼────────────┼──────────┼──────────┤
│☐ │ Family        New  │ 10:42   │ 12,410     │ [On ▾]   │ Default  │
│  │ 12345@g.us         │         │ 2 gaps     │          │ 1 failed │
└──┴────────────────────┴─────────┴────────────┴──────────┴──────────┘
[Previous]  Page 2  [Next]

Selected: 3  [Keep logging] [Stop logging…] [Media rules…] [Mark seen]
```

The server reads the paged `chat_stats` view and joined control rows; it never calls `StatusReport`. Default order is newest activity, then canonical JID. Page size is 50. Pagination uses a stable keyset cursor, not an offset.

Search here matches display name and JID. Filters combine with AND:

- type: direct, group, community;
- logging: on or off;
- New;
- has open gaps;
- has failed media.

The URL owns query, filters and cursor, so reload/back/share preserve the view. Filters are real labelled controls in a GET form. “Clear filters” returns to the first page. A result count precedes results.

Each row links to its viewer and shows display name, canonical JID when useful, last activity, message count, New, gap/failed-media warnings, logging state and media-rule summary. Community rows disclose their child count and expand in-place; children remain direct links.

### Rule actions

- Row controls swap only that row. The response uses its stable hashed DOM id.
- “Stop logging” requires confirmation:

  > Stop logging **Family**? New messages will not be saved. The messages
  > already archived stay available.

- Starting logging first shows the FR-25 estimate: projected text and media per
  week, basis/window, chosen media rule, and “Estimate unavailable” when there is not enough history. Confirmation is “Keep logging”.
- Media rules offer Default, Text only, or Custom types and video cap. A child
  with an explicit override is labelled “Own rule”; otherwise it says “Inherited from Community name” or “Default”.
- A community update applies to current children and creates inheritance for
  future children. An explicit child rule wins until “Use community rule”.
- After a successful logging change, refresh `Rules.Replace` before returning.
  The new rule applies within one incoming message.

Bulk selection distinguishes “this page” from “all N filtered”. An all-filtered action sends the filter predicate plus a server-issued snapshot token; it does not trust thousands of browser-provided JIDs. The confirmation names the count and, for communities, the number of affected child chats. If the filtered set changed, re-show the estimate and count before applying.

### States

- Empty archive: “No chats yet. Pair WhatsApp to begin, then history and new
  chats will appear here.” Button: “Open Pairing”.
- No filter result: “No chats match these filters.” Button: “Clear filters”.
- Loading fragment: retain rows, mark the table `aria-busy`, and disable only
  the submitted filter or action.
- Error: keep old rows and show “Chats could not be refreshed. Try again.”
- Offline: existing rows and viewers remain readable; rule, acknowledge and
  bulk controls are disabled.

## 6. Viewer — W6(c)

### Desktop wireframe

```text
← Chats   Family                         [Search] [Jump to date] [Export]
          Logging on · Default media · History back to 4 Mar 2021
[Sender ▾] [Media type ▾] [From date] [To date] [Clear]

                    28 October 2026
        ┌──────────────────────────────┐
        │ Fatima · 10:32               │
        │ Reply to Ali: …              │
        │ مرحباً بالجميع               │
        │ 👍 3   ❤️ 1      Edited      │
        └──────────────────────────────┘
                                  ┌──────────────────────────────┐
                                  │ You · 10:34                  │
                                  │ [thumbnail / media control]  │
                                  │ Caption                      │
                                  └──────────────────────────────┘
[Load older messages]                         [Jump to newest]
```

Messages are chronological, grouped with day separators, and styled as incoming/outgoing bubbles. Colour is not the sole distinction. Sender names use the resolved participant name and expose the mapped identifier in Details. Every user-authored text container has `dir="auto"`.

Keyset cursors load in both directions. At the live edge, new messages append through SSE; away from it, a “N new messages” button appears rather than moving the reader. `hx-trigger="revealed"` may preload, but explicit paging controls remain. Prepending preserves the first visible message and pixel offset.

Viewer features:

- quoted block shows resolved sender and a short inert excerpt; activation
  loads and focuses the original, or says “Original message is not archived”;
- reactions aggregate identical emoji with count and accessible label;
- edited messages show the latest text plus “Edited”; Details shows the full
  edit timeline and original;
- revoked messages show “This message was deleted” by default. “Show deleted
  text” is per viewer session, clearly labels revealed text, and is not saved;
- polls show question, options, counts and total archived votes, with
  “Results may be incomplete” when votes could not be decrypted;
- locations show name/address and an external “Open map” link with
  `rel="noopener noreferrer"`; no third-party map is embedded;
- contacts show display name and a collapsed, inert vCard text view;
- view-once says “View once”; ephemeral shows the original duration. The
  archive copy is not implied to disappear;
- images use stored thumbnail first and lazy-load the original; video and
  audio use native controls and `preload="metadata"`; documents show safe filename, type and size and download as attachments;
- unavailable media shows state-specific copy (“Waiting to download”,
  “Expired; retry requested”, “Download failed”) and links to Details;
- the existing message trace remains the Details destination and includes raw
  facts rendered as escaped text, never active HTML/SVG.

Sender, media type and inclusive date-range filters live in the URL and apply to both directions. Jump to date opens the nearest archived message and says when there was no message on that date. Unknown chat and foreign message IDs return 404 rather than an empty viewer.

### In-chat search

Search opens in the viewer header. Results use the same Arabic folding and FTS routing as global search. The current match gets `aria-current="true"`; next and previous load across cursors, focus the message heading, and update “3 of 18”. Highlights are computed in Go from the stored text because the contentless FTS tables cannot produce snippets. Never inject highlight markup into unescaped content.

### Viewer states

- Empty: “No messages are archived for this chat yet.” Keep chat rule/history
  summary visible.
- Filter empty: “No messages match these filters.” Button: “Clear filters”.
- Loading older/newer: skeletons at the requested edge; current messages stay.
- Error: edge-local retry; never replace the whole conversation.
- Offline: archived page remains readable; live-edge status says “Live updates
  paused”; paging already in the browser works, server reads may fail normally.

## 7. Global search — W6(d)

```text
Search messages
[____________________________________________] [Search]
[All chats ▾] [Sender ▾] [From date] [To date] [Media type ▾]

18 results in 5 chats
Family · 4 results
  28 Oct 10:32  Fatima  …highlighted context…  [Open message]
Work · 7 results
  …
[More results]
```

The input label is “Search archived messages”; placeholder: “Words or part of a word”. Fold with `store.Fold`, quote phrases safely by doubling quotes, use `search_tri` when every term has at least three characters and `search_fts` when any term has one or two. Apply chat/sender/date/media filters after canonical resolution. Empty and whitespace-only queries do not run FTS.

Results are grouped by chat, ordered by newest matching message, then result time. Each result has date/time, resolved sender, at most two lines of escaped context, kind, and deleted/edited badges. A result jumps to and focuses the message in the viewer. Pagination is keyset-based, 50 matches per response; group headings may repeat across pages with “continued”.

- Initial empty: “Search text and captions across all logged chats.”
- No result: “No archived messages match **query**.” Offer spelling/filter
  changes; do not claim that WhatsApp has no such message.
- Loading: keep prior results until replacement succeeds.
- Error: “Search is unavailable right now. Try again.”
- Offline: prior results remain; submitting a new search produces the shared
  offline message.

## 8. Export one chat — W6(e)

Export is reached from a viewer and always names the chat and applied date range. Default range is the entire archived chat. Sender/media filters may be carried over only after the user explicitly selects “Export current filtered view”; otherwise the dialog says “All archived messages”.

Formats:

| Format | Contents |
|---|---|
| Typed JSON | Versioned top-level metadata and typed messages/events in stable chronological order; no `raw_proto` |
| Self-contained HTML | Escaped, readable transcript with inline CSS, thumbnails and capped embedded media; no script or remote resources |
| WhatsApp text | UTF-8 `_chat.txt` lines in WhatsApp import/export style; multiline messages continue safely |
| WhatsApp ZIP | `_chat.txt`, available downloaded media with collision-safe names, and `missing-media.txt` |

All formats include deleted archived text and edit/event history with explicit markers. They exclude data from other chats, connection/health data, secrets, internal database row IDs and raw protobufs. JSON declares schema version, archive timezone, canonical JID, aliases included, generated time, filters and counts. HTML carries the same provenance visibly.

HTML embeds each media file up to 10 MiB and at most 100 MiB total. Thumbnails do not count toward the per-file limit but do count toward the total. Once a limit is reached, the message contains filename, MIME, size, SHA-256 when known, the reason it was omitted, and an authenticated link to the live archive. This limitation is stated before export; text and thumbnails remain self-contained. ZIP has no artificial per-file cap and streams without first holding the archive in memory.

The response streams with `Content-Disposition: attachment`, a safe filename, `X-Content-Type-Options: nosniff`, and cancellation when the client leaves. Before headers, validation errors render normally. After streaming starts, a server failure is logged and the incomplete download ends; formats that allow it put a manifest/header first. Export never writes a persistent server copy.

Copy:

- Start: “Preparing export…” with format and scope. This page may be closed
  after the browser download begins.
- Empty range: “There are no archived messages in this date range.”
- Missing media: “The transcript is complete. 12 media files were unavailable
  and are listed in `missing-media.txt`.”
- Error before download: “The chat could not be exported. Try again.”

The `_chat.txt` formatter has round-trip tests for English, Arabic, bidi text, multiline bodies, attachments, deleted messages, edits, duplicate filenames, and timestamps around daylight-saving changes.

## 9. Logging and Settings — W6(f)

Logging defaults to the **Overview** tab. Tabs are links with proper current state, not an ARIA tab widget: Overview, Outages, Gaps & media, History, Alerts, Storage & backups, Live feed, Settings.

### Overview wireframe

```text
Logging
[Overview] [Outages] [Gaps & media] [History] [Alerts]
[Storage & backups] [Live feed] [Settings]

┌ Connection ──────────────────┐ ┌ Capture ─────────────────────┐
│ Connected                    │ │ Last event 12 seconds ago    │
│ Session age 18 days          │ │ 250 chats · 300,000 messages│
│ Client 2.x · Phone active 4m │ │ 7 open gaps · 3 media failed│
└──────────────────────────────┘ └──────────────────────────────┘
[inactivity warning, only when newly applicable]
```

Overview shows connection state including reconnecting and degraded, client version/reason, session age, last event, phone last active, recovered panics, archive counts, outage minutes today, longest recent outage, history summary, SC-3 media percentage and last backup. “Logged out” is red and links to Pairing; it is never rendered as an amber title-cased internal enum.

Only the health values that changed swap on `health` SSE. The panel itself is not live. The phone inactivity warning has one stable ID and is announced only when it first appears or its severity meaningfully changes.

### Operational sections

- **Outages:** newest-first `connection_periods` timeline with start/end,
  duration, state, safe cause class, error count, offline preview messages and completed backlog count. Open period says “In progress”. Summary gives outage minutes by day and longest outage; `not_running` is its own cause and counts in totals.
- **Gaps & media:** open gaps grouped by cause and post-outage status, with
  links to trace/chat. Media shows counts by state, error class and redacted reason, plus live media completed within five minutes over 24 hours and seven days. Never expose media URLs or encryption material.
- **History:** ledger per sync type with received/imported chunks, progress,
  oldest/newest dates, spool backlog and error count. Stalled copy is “Stalled at 99% — no new chunk for 48 hours.” Per-chat rows link to viewers and say “History complete back to …”, “History received back to …”, or “No history messages archived”.
- **Alerts:** durable outbox rows with kind, safe title, raised, delivered,
  attempts and state. A late delivery says “Delivered 2h 14m late”. Do not render sink URLs, topics, or raw last errors.
- **Storage & backups:** reuse cached database/media/backup/free-space figures,
  growth and projection, media by type, largest chats, backup schedule and latest result. Restore remains instructions, never a destructive button.
- **Live feed:** `role="log"`, newest event visibly at the top but additions are
  announced in chronological arrival order. Filters: chat and kind. Keep 200 items in the DOM and offer “Pause updates”. Metadata only per decision 3.

Each section has its own empty copy: “No outages recorded”, “No open gaps”, “No media failures”, “No history chunks received yet”, “No alerts raised”, “No backup has run yet”, and “Waiting for archive activity”. Loading and errors are section-local. Offline freezes live values but keeps all rendered facts.

### Settings

Settings reuses the existing database-backed controls and adds heartbeat:

- default media download types, video cap, workers and retry rate;
- alert thresholds, including disconnected/no-events/phone-quiet/disk-free;
- backup time and retention count;
- heartbeat URL, with the saved value masked to origin plus final path segment;
- test alert with its existing throttle and redacted error handling.

Each section is a separate form and fragment. Success copy names what changed and when it takes effect. Fields read only at process start say “Takes effect after restart” before submission. Lowering backup retention repeats the existing warning. Heartbeat URL is write-only after saving: blank means keep the stored secret; a separate “Remove heartbeat” confirmation clears it.

Config-only values remain a read-only “Installation” panel. Never echo ntfy topic URLs or credentials. Settings has no SSE connection.

## 10. Pairing — W6(g)

```text
Pairing
┌─────────────────────────────────────────────┐
│ Not paired                                  │
│ Link this archiver like WhatsApp Web.       │
│ Nothing is sent to your contacts.           │
│ [Pair WhatsApp]                             │
└─────────────────────────────────────────────┘

Pairing in progress
1. Open WhatsApp on your phone
2. Settings → Linked devices → Link a device
3. Scan [ QR ]       A new code appears every 20 seconds
[Cancel pairing]
```

States:

- unavailable: “Pairing is unavailable in this process.”;
- not paired: explanation and primary “Pair WhatsApp”;
- starting/loading: “Starting pairing…”;
- waiting for QR: “Waiting for WhatsApp to provide a QR code…”;
- QR ready: numbered instructions, QR with concise alt text, renewal status;
- paired: “Paired. Capture is running.” plus session age, history status and
  linked-device advice;
- failed: safe reason, “Try again”, and link to Logging when appropriate;
- offline: existing QR is visibly expired and scan/start controls are disabled.

For a paired device, show “Pair a different device…” as a secondary danger action. Confirmation says:

> Re-pair WhatsApp? This signs this archiver out of its current linked-device
> session and starts a fresh pairing. The archive is not changed, but no new
> messages are logged until pairing finishes.

On confirmation the pairer stops the connection, resets the device store as whatsmeow requires, and starts a fresh QR flow without restarting the process. The archive database and media remain untouched. A LoggedOut health state uses the same reset path and presents “Pair again”; it does not require a restart.

Show session age and this advice when paired:

> Open WhatsApp on your phone regularly. WhatsApp may unlink inactive linked
> devices after 14 days.

When the phone-quiet threshold is crossed, use the shared inactivity warning, not a second competing alert. Pairing uses SSE only while starting, waiting, paired-from-this-page, or recovering from LoggedOut; otherwise it is static.

## 11. Performance budgets

Measure server time from handler entry to the final response write on the W6(h) 300,000-message / 250-chat seed, warm database cache, release build, one local client, 30 measured runs after five warm-ups. Report p50/p95 and the slowest query. Network transfer and browser rendering are reported separately. A feature does not pass by hiding server time behind a loading fragment.

| Operation | Budget (p95 server time) | Additional bound |
|---|---:|---|
| Shell / Pairing page | < 100 ms | no archive-wide query |
| Chats first/filter/next page | < 150 ms | 50 rows; no `StatusReport`; bounded queries |
| One row rule swap | < 50 ms | includes DB write and `Rules.Replace` |
| Bulk preview | < 150 ms | estimate may use cached aggregates |
| Viewer initial/older/newer page | < 150 ms | 100 messages; no N+1 aliases, names, events or media |
| Viewer jump to date/message | < 150 ms | indexed keyset seek |
| In-chat/global search | < 300 ms | 50 matches including safe context/highlight |
| Logging overview/section | < 150 ms | storage walk uses its five-minute cache |
| Health/pair/live-feed fragment | < 50 ms | no full-page render |
| Export start | < 150 ms to headers | stream at least 20 MiB/s for local files; O(page) memory |

HTML first response should be useful without waiting for htmx. Aim for less than 100 KiB uncompressed HTML for any normal page and no more than 100 viewer messages or 50 search/chat rows per fragment. Images use thumbnails and lazy loading. Native audio/video use metadata preload. SSE payloads contain only the changed fragment or event and remain under 16 KiB except the QR panel.

## 12. W6(a–g) acceptance checklist

- **(a) Shell:** three-item navigation, correct current state, middleware order,
  scoped SSE, global htmx error handling, escaped paths, unknown-chat 404, single feed, 375 px layout and theme selector all have automated tests.
- **(b) Chats:** paged derived-table list, every filter, row/bulk/community
  rules, inheritance, estimate, confirmations and sub-50 ms row budget pass.
- **(c) Viewer:** every typed kind and event renders; both-direction cursors,
  date jump, filters, search navigation, media safety, RTL and trace pass with JavaScript enabled and disabled.
- **(d) Search:** Arabic folding, one/two-character routing, phrase escaping,
  grouping, deleted/edit labels and message jump pass on the seed.
- **(e) Export:** all four outputs stream; scope/privacy assertions pass;
  `_chat.txt` round-trips; missing/large media follows section 8.
- **(f) Logging:** all required health, outage, gap, media, history, alert,
  storage, backup, feed and settings states render without secret disclosure or repeated screen-reader announcements.
- **(g) Pairing:** initial pair, QR renewal, success, safe failure, LoggedOut
  re-pair and voluntary re-pair work without a process restart or archive mutation.
- Empty, delayed, failed and offline scenarios are browser-tested at desktop
  and 375 px; keyboard-only and screen-reader smoke scenarios pass.
- Performance measurements use the stated seed and method and meet every hard
  budget before W6(i) evaluation.
