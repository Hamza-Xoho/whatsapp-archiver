Single status record for V1 (max 150 lines, update in place; history lives in git and the archived phase log).

## Done

- Phases 0-2 finished and deployed: a87a30f on ryzen since 2026-10-20 (old log: `docs/reviews/2026-10-26-progress-phase0-2-archive.md`).
- 2026-10-26 V1 plan committed: 325a8e6, `docs/plans/2026-10-26-v1-plan.md`.
- Week review report: `docs/reviews/2026-10-26-week-review-issues.md`.
- W0 step 1 done: 7041f7c, pushed.
- W0 step 4 done: check-fast / test-quiet / Stop hook (6278eff); PROGRESS moved + this file (2b88677); skills `ryzen-evidence`, `deploy-ryzen` (f713d23).
- W0 step 2 done: ryzen evidence, Appendix A of the week review (ce81d4d). Findings: ntfy configured, only alert attempt failed on DNS; 403 = 24,536 of 25,358 failed media (no 404/410); practice CPU ~15% not explained by media backlog; `attempts` up to 20 vs text "after 5 attempts" (check in W4).
- W1 done and signed 26 Oct: charter v1.3 (V1 re-scope, ADR-34 to ADR-39); `docs/phase2-acceptance.md` practice path fixed.

- W2 schema design signed off 26 Oct (`docs/v1/schema.md`, e1611f1 + decisions): spool blobs cleared 7 days after import; deleted = placeholder + toggle; not_running counts as outage minutes.

- W2(b) migration and store rewrite (branch main, base eb9d4d4): `0001_v1.sql`, `Apply(Batch)`, kinds, ledger/spool, control tables, ports of ingest/media/web/cmd. Golden review, 26 Oct: `internal/ingest/testdata/golden/synthetic.sql` regenerated with `-update` after reading all 13 changed lines against the old file: raw_proto hex, ts, ids and values are byte-identical; the only differences are the new columns (participants push_name/contact_name; messages from_me, push_name, kind, text/caption split, quoted_sender, mentions `[]`, forward_score, view_once, ephemeral_secs, details `{}`; events source_id/cause/source; ledger stage `imported`, spool_id 0). `cmd/archiver/testdata/status.golden` differs in one line: data folder 109.8 KB to 249.8 KB (the V1 schema has about 35 more b-trees, one page each).

- W2(c) normaliser fills every typed column (base 12ca198): all 16 kinds with `details`, context (quote, mentions, forwarding, expiry, view-once), media facts, `source_id` and `cause` on events, history reactions and poll updates (R14), live poll-vote naming (R15). Golden review, 26 Oct: created with `-update` after reading every line of the 19 new dumps (`kind-*`, `events-messages`, `events-undecryptable`, `events-history`; each INSERT checked column by column against its generator input in `internal/ingest/golden_kinds_test.go`, raw_proto checked by length and leading bytes, because the raw bytes are the unchanged marshalled input) and the 5 changed lines of `synthetic.sql` (read against the old file): the only differences are `quoted_sender` of SYN-REPLY-1, `cause='other'` on the undecryptable, `source_id` on revoke, edit and reaction, and the reaction's `event_id`, which now derives from its own message ID (so the live and the history copy of a reaction are one row).

- W2(c) fix round 1 (review: approved with one Important), red `test:` then fb95bc7 (cause for other unavailable types, vote-less history update, absent place fields omitted), docs in the next commit. Golden review: `kind-location` and `kind-live-location` regenerated with `-update` after reading every changed line. The diff is 4 added INSERT lines and no changed line: K-LOC-3 (`{"name":"A place without coordinates"}`), K-LIVE-2 (`{"lat":24.6,"lon":46.6}`) and Bob's participant and mapping rows; K-LOC-1/2 and K-LIVE-1 are byte-identical.

## Known divergences (W2(c), accepted; commits 9d4c85d and fb95bc7)

- Undecryptable `cause` is mapped in `internal/ingest/events.go` (it reads only `types/events` fields), not in `internal/wa`. `untrusted_identity` is never produced: whatsmeow's event has no error value. `IsUnavailable` -> `no_sender_key` is an approximation (whatsmeow also sets it for "no ciphertext at all"); a non-view-once `UnavailableType` is `other`.
- A poll vote on a poll that is not in the archive, a vote that cannot be decrypted, and every replayed vote keep `value=''`; a vote that selects nothing is `{"options":[]}`; a history poll update without a vote is `''`.
- `new_text` on reaction and pin events still holds the emoji / pin type (pre-existing, pinned by `TestEventsForUnknownTargetAreStored`). Task (d) must index only `kind='edit'` text under R13.
- The `system` kind covers `call`, `callLogMesssage`, `scheduledCallCreationMessage`, `scheduledCallEditMessage`, `bcallMessage`: a judgement call, everything else unrecognised is `unknown` with `fields`.
- A reaction's or poll vote's `event_id` derives from its own message ID, so a live and a history copy are one row only when the chat JID has the same form in both; CON-7 (JIDs as received) otherwise keeps both.
- Absent proto fields are absent keys in `details` (coordinates, accuracy, speed, sequence).

## In flight

- W0 step 3 (Hamza): done 26 Oct: practice instance unlinked on the phone and its process stopped (PID 288900); only the `SaudiSpyWithMyWifi` Wi-Fi profile left; stale DNS 192.168.68.1 gone. Reboot test still open (optional).
- `sudo perf` on the practice PID is moot: the instance is stopped.

## Rulings

- (a) The `add-message-kind` skill (replaces `add-event-kind`) was written in W2(c).
- (b) W0 is done on main without a worktree.
- (c) No Co-Authored-By trailer on commits.
- (d) Router reservation / static IP for ryzen skipped by Hamza (26 Oct): the connection is only needed for coding, so the address may change; `deploy-ryzen` and docs use 192.168.0.105 and must be rechecked if it does.

## Known divergences (W2(b), accepted)

- ChatList takes rules/seen/people from `canonicalOf` (smallest LID mapped directly) but aggregates from `chat_canonical`; `Aliases` still reads `id_mappings` (H1 test). Task (f) must switch all of these to `chat_canonical` together.
- Decide-before-Apply: a media-rules read failure now holds the message (accepted; same DB, so Apply would fail too).
- Read changes accepted: storage "by type" shows a separate `voice` row; activity detail says `voice`; replay stores media rows as `skipped`.
- R8 tie-break: "smallest LID" is by string order ("10@lid" < "9@lid"), same as the old `canonicalOf`; task (d) `RebuildDerived` must use the same comparison.

## Open questions

- (none open from W1; ADR-39 and charter v1.3 signed by Hamza 26 Oct.)
- W3 and W4 run in parallel after W2 merges.
