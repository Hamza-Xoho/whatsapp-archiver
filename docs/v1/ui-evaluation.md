# W6(i) UI evaluation

Status: automated and static evaluation complete at `c04ddae`; interactive browser
evaluation blocked by the T3 preview environment on 31 Oct 2026. This evaluation
used synthetic data and fake WhatsApp boundaries only. It did not pair, import a
session, connect to WhatsApp, access ryzen, or deploy anything.

## Environment and method

- Host: Linux 7.0.0-38-generic x86_64, UTC; Go 1.26.9 linux/amd64.
- Branch/base: `v1/w6i-evaluation` from `origin/main` `41e91d9`.
- Evaluated implementation revision: `c04ddae` (the documentation commit is later).
- Data: ignored `data/` copies of deterministic seed 42: 250 canonical chats,
  exactly 300,000 messages, and 50,000 messages concentrated in one chat. The
  sentinel is `SEED-42-000000000` at timestamp 1761436842.
- Measurement: release build, warm SQLite cache, one local HTTP client, five
  warm-ups then 30 measured runs, as required by `docs/v1/ui.md` section 11.
- Safety: `.claude/launch.json` contains devseed-only launches. Pairing scenarios
  use the in-process fake pairer; live scenarios enter through `ingest.Pipeline`.

Commands and checks used:

```text
make check-w6i
make check
make check-fast
mkdir -p data/ui-evaluation-18090
printf 'web:\n  listen: 127.0.0.1:18090\n' > data/ui-evaluation-18090/config.yaml
./.tools/go/bin/go run ./cmd/archiver devseed --data data/ui-evaluation-18090 --seed 42 --chats 250 --messages 300000 --large-chat-messages 50000 --simulate-live 5/s
preview_status
preview_open http://127.0.0.1:18090
```

`make check-w6i` verifies the exact seed identity, gives each sequential package
its own disposable archive copy, and exercises the devseed, store and HTTP layers.
Every red/green implementation pair ran `make check`; the final implementation
gate passed race tests, the 300k non-race scale test (17.389 s), lint and all five
cross-platform builds.

## Browser tooling limitation

The required T3 flow began with `preview_status` and `preview_open`. Port 8090 was
already occupied, so the synthetic server fallback was 127.0.0.1:18090. T3 tabs
`tab_2` and `tab_3` remained Chrome error pages, and the environment-port client
`preview-eacecf7b02ac81a6dbc8971bee158176` could not attach to the fallback port.
The preview tooling was therefore unavailable, not merely a page failure.

There are no browser screenshots, DOM snapshots, console logs, network-panel
captures, assistive-technology observations, or downloaded-file observations.
Screenshot references are unavailable. Browser-only rows below are `BLOCKED`,
not passed. HTTP/store/static assertions are reported separately so they are not
mistaken for visual acceptance.

## Scenario results

| Scenario | Automated/static result | Browser result |
|---|---|---|
| Chats first page, filters, next page and bulk controls | PASS: exact-seed HTTP timing, 50-row and response-size bounds | BLOCKED |
| Stop logging, reject next simulated message, keep logging, store next message | PASS: fake-only full HTTP/store regression | BLOCKED |
| 50k-message viewer, older/newer and jump to date | PASS: bounded HTTP routes and timing assertions | BLOCKED: scrolling and focus not observed |
| Arabic global search without diacritics | PASS: folded HTTP/store query and 50-result bound | BLOCKED |
| In-chat next/previous search | PASS: exact N-of-M semantics retain only adjacent IDs | BLOCKED: keyboard interaction not observed |
| Synthetic images, audio, video, failed and expired media | PASS: fixture signatures, safe media HTTP paths and markup | BLOCKED: playback not observed |
| Quoted reply navigation | PASS: target and inert-missing-target markup tests | BLOCKED: click/focus outcome not observed |
| JSON, HTML, `_chat.txt` and ZIP-with-media exports | PASS: format, privacy, snapshot, cancellation and ZIP-content tests | BLOCKED: browser downloads not observed |
| Simulated outage, gaps by cause and stalled FULL history | PASS: bounded Logging read models and HTTP rendering | BLOCKED: visual warning timing not observed |
| Fake pairing: unavailable, not paired, start/wait, QR, renewal, failure, paired, re-pair, LoggedOut and cancel | PASS: deterministic HTTP/fake-state test; archive mutation prohibited and asserted absent | BLOCKED |
| Simulated message through Pipeline -> store -> viewer/feed SSE in under 2 s | PASS: deterministic integration assertion; 64 KiB content uses a bounded refetch event | BLOCKED: live paint not observed |
| 375 px mobile layout and touch targets | PASS: CSS/source tests pin card/table behavior and at least 44 px viewer controls | BLOCKED: no screenshot or touch pass |
| Dark and system themes | PASS: CSS and persisted selector contracts | BLOCKED: no visual pass |
| RTL Arabic and mixed-direction names | PASS: `dir=auto`/bidi-isolation contracts for user content, rule names and exports | BLOCKED: no visual pass |
| Keyboard-only operation and focus after htmx swaps | PASS: focus-repair and scoped validation-swap contracts | BLOCKED: no end-to-end tab-order pass |
| Screen-reader semantics and announcements | PASS: labels, roles, stable live regions and reconnect/QR announcement contracts | BLOCKED: no assistive-technology smoke test |
| Console and unexpected network errors | No automated client errors in HTTP tests | BLOCKED: no console/network-panel evidence |

## Performance results

All hard server-time assertions passed on the exact seed. Values below are from
the final captured release runs; `PASS` also means the response status and
required bounded shape were asserted, not only elapsed time.

| Operation | Captured result | Budget/result |
|---|---:|---|
| Shell / pairing page | pair 0.070/0.266 ms p50/p95 | <100 ms, PASS |
| Pair SSE fragment | devseed 0.139/0.226 ms p50/p95; 738 bytes | <50 ms, PASS |
| Chats first / filtered / next | 11.171/12.334; 14.003/26.267; 12.075/20.473 ms p50/p95 | <150 ms, PASS |
| Row swap / bulk preview | 21.229/30.649; 15.778/21.969 ms p50/p95 | <50 / 150 ms, PASS |
| Viewer initial / older / newer / jump | 131.018/139.298; 24.537/29.217; 23.915/31.244; 14.827/16.813 ms p50/p95 | <150 ms, PASS |
| In-chat / Arabic global search | 264.395/271.013; 8.043/10.461 ms p50/p95 | <300 ms, PASS |
| Logging overview / slowest section | 15.104/20.532; feed 3.361/4.424 ms p50/p95 | <150 ms, PASS |
| Health / feed fragments | 0.154/0.184; 5.025/6.907 ms p50/p95 | <50 ms, PASS |
| Export headers | 62.817 ms p95 | <150 ms, PASS |
| Validated 20 MiB local ZIP | 618.857 ms, 32.3 MiB/s | >=20 MiB/s, PASS |

Additional bounds:

- Normal Chats HTML is 102,270 bytes, below the 100 KiB (102,400-byte) aim while
  retaining 50 rows, statuses, actions and visible media-rule labels.
- Viewer live SSE for the large-message refetch case is 772 bytes; health, feed,
  pair and viewer events remain below 16 KiB after exact SSE wire encoding. A
  2,300-line message regression includes every `data: ` prefix in that bound.
- Viewer pages contain at most 100 messages; Chats/search fragments contain at
  most 50 rows.
- Dense export coverage crosses two 1,000-row boundaries with 2,201 colliding
  media names in exactly three pages (1,000/1,000/201). Text export keeps one
  bounded page and per-page sender/media facts; filename collision detection uses
  a fixed 8 KiB snapshot Bloom filter. Snapshot startup is exactly three queries
  (known chat, aliases and indexed insert-only bounds), with an executable EXPLAIN
  assertion that no startup query touches media. A held read transaction freezes
  mutable media rows; ZIP uses one media traversal and a private on-disk missing
  manifest spool. Export cancellation and snapshot upper bounds are asserted.
- The slowest measured Chats SQL was filtered ordered membership at 2.327 ms p95;
  the slowest Logging read model was gaps at 2.816 ms. The slowest full HTTP
  operation was in-chat search at 271.013 ms p95. No page hides server work behind
  an unmeasured loading fragment.

## Accessibility and design review

Static review initially found keyboard focus loss after htmx swaps, unsafe
out-of-band settings errors, unstable reconnect announcements, QR renewal that
could re-announce the image, undersized mobile controls, missing visible media
labels and insufficient bidi isolation. Red/green fixes now provide:

- focus restoration for row/settings swaps and an inline, scoped 422 form that
  preserves entered or previous values;
- delayed-loading and local-error regions for global and in-chat search, with one
  announcement per real offline/reconnect cycle;
- a dedicated stable polite QR status, visible expiry and disabled unavailable
  pairing actions;
- 375 px trace cards, 44 px viewer actions, visible Photos/Videos/Audio/Documents
  labels and bidi isolation for user-controlled names;
- generic production pairing copy; fake/devseed wording is not exposed;
- bounded SSE events measured after exact wire framing that require refetch for
  large or highly multiline archived content rather than presenting truncation as
  complete.

No Critical or Important static/spec finding remains. Browser-dependent contrast,
layout, focus order, media playback, console/network cleanliness and announcement
quality remain ungraded because the preview client was unavailable. The existing
Minor finding remains: Logging storage may perform up to ten bounded chat-name
reads for its top-ten projection.

## Outcome and remaining work

The deterministic devseed, store, HTTP, export, query-shape, accessibility-markup
and release-budget portions of W6(i) pass. Interactive full-stack browser
acceptance is blocked by T3 preview availability and must be rerun before claiming
the W6(i) browser scenarios passed. W7 deployment and W8 live acceptance remain
Hamza-only work on his Mac/ryzen and were not attempted.
