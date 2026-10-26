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

- W2(c) fix round 1 (review: approved with one Important), red `test:` then fb95bc7 and 9bfed38 (cause for other unavailable types, vote-less history update, absent place fields omitted), docs in the next commit. Golden review: `kind-location` and `kind-live-location` regenerated with `-update` after reading every changed line. The diff is 4 added INSERT lines and no changed line: K-LOC-3 (`{"name":"A place without coordinates"}`), K-LIVE-2 (`{"lat":24.6,"lon":46.6}`) and Bob's participant and mapping rows; K-LOC-1/2 and K-LIVE-1 are byte-identical.

- W2(d) derived tables, FTS and RebuildDerived (base 9bfed38): `Fold` (FR-10), `chat_stats`/`search_fts`/`search_tri` maintained inside `Apply`, the media state moves, the mapping merge, `SetChatRules`/`MarkChatSeen`/`ImportConfigOnce`; `Store.RebuildDerived` (one transaction) and `archiver rebuild-derived --data <dir>`. Rebuild equals incremental is pinned by a mixed-workload test (chat_canonical, chat_stats, fts5vocab of both FTS tables); 26 hand-made mutations of the maintenance paths are each killed by a test (one is equivalent, see below). Rebuild of 300k messages: 8.7 s (`BenchmarkRebuildDerived`, set `ARCHIVER_BENCH_MESSAGES`). Insert cost test (100 inserts, no -race): 22 to 24 ms before, 24 ms into a 20k-row table now (1.4x); 50 ms into an empty archive (2.3x, because each of its 50 chats is new and a new chat's row is computed once from the raw tables).

- W2(e) contact names into `participants.contact_name` (base 828dac0): `Store.SetContactNames` (one transaction, `... WHERE differs`, `refreshIdentity` per changed name), `wa.ContactSyncer` (reads `Store.Contacts.GetAllContacts` after `Connected`, the `critical_unblock_low` app-state patch, `events.Contact`, `events.BusinessName`; 5 s debounce, requests coalesce, errors logged and retried at the next event), `wa.ContactNames` (full name, else first name, else business name; never the push name), wired in `serve` (`newContactSync`, `withContactSync`, `contacts.Run` among the loops). Prior-art rows added first (3914d1d). 43 hand-made mutants of the store method, the name preference, the triggers, the debounce and the wiring: 41 killed (one only after the second test commit), 2 equivalent or unkillable (see below).

- W2(f) status and stats on the derived tables (base 7170e71): `StatusReport` is one read of `chat_stats` (`WHERE messages>0`) plus the ledger query; `ChatList` is one query over `chat_stats` with rules and sightings aggregated through `chat_canonical`; `ChatName`, the chat-page sender names, `Trace`, `ChatMedia`, `ChatPage` and `Usage` resolve forms through `chat_canonical` (`chatForms`), names use the `nameExpr` order (chat, contact, push). Red commits d1561ee, e9e145d, and the scale-test split (see git log). Measured, no `-race`, 300k messages over 250 chats: StatusReport median 0.69 ms (max 1.9 ms), ChatList p95 1.1 ms (seed 8 s, derive 1.2 s). Under `-race` the same 300k run (one-off, before the split) gave StatusReport 22.7 ms median and ChatList p95 39 ms but seeding took 2 m 48 s, so `make test` (and so `make check`) runs 50k under `-race` (22.4 ms and 38.4 ms) and then 300k without it (`go test -run AtScale ./internal/store`, second step of the target). Plan targets: 100 ms and 150 ms. No golden changed (`status.golden`, `chats.golden`, `synthetic.sql` and the kind dumps pass unchanged).

- W2 final review fixes (base 1d5b40d, red `test:` bd81d06, green 27777a0): I1 `SetContactNames` calls `ensureCanonical` for every non-empty name (a contact-only JID no longer gets an extra `alias|alias` row in the rebuild; `participants.jid` stays a rebuild source for sender-only push names); M1 `mergeStats` always recomputes the merged chat (a stand-alone media row under an alias with no stats row was missed; case added to `workload`); M2 the scale test asserts the plan's 100 ms for `StatusReport` without `-race` (`statusMedianLimit`, 250 ms under `-race`); M4 the pre-settings-table fallback in `Setting` is gone (R16 refuses such archives; no test used it); M5 `clearMessageSecrets` also clears `ProtocolMessage.EditedMessage`; M3 docs (AGENTS.md, charter `chat_aliases`/`messages_resolved` -> `chat_canonical`, ryzen-evidence skill, `untrusted_identity` comments, README `rebuild-derived` and fresh data folder). No golden file changed.

## W3/W4 brief rules

- Every new media state write goes through a store method that calls `mediaMoved` in the same transaction, and the equivalence `workload` (derived_test.go) gets one case per new transition.
- W3 and W4 both edit `cmd/archiver/main.go`, `internal/store/kinds.go` and `docs/v1/STATE.md`: agree the merge order up front (one merges first, the other rebases and re-runs `make check`).

## Known divergences (W2(f), accepted)

- `Aliases` still reads `id_mappings` (not `chat_canonical`), deviating from the brief. Reason: the H1 test (`TestAliasesUnionForPhoneWithSeveralLIDs`) pins `Aliases("2@lid")` = only the phone number; `chat_canonical` would also return `1@lid` (the same chat), so the observable result is not identical and the test may not be weakened. Every other read uses `chat_canonical` (`chatForms`). Consequence to decide: `Aliases` is used by the ingest filter and the rule and acknowledge handlers (`web/chats.go`, `cmd`), so a rule set on one LID of a person with two LIDs does not reach the other LID's rule row; a one-line switch to `chat_canonical` plus changing that H1 assertion to `[1@lid, 9@s.whatsapp.net]` fixes it. Also: `ChatList` aggregates rules over the whole canonical group while `Aliases` (used by the ingest filter) does not reach a sibling-LID rule, so the list and the filter can disagree for a person with two LIDs sharing a phone. Controller ruling: parked for Hamza; switching `Aliases` to `chat_canonical` and changing the H1 assertion to `[1@lid, 9@s.whatsapp.net]` is the proposed fix.
- `ChatList` had no N+1 `Aliases` calls (it used `canonicalOf`, now deleted); the `Aliases` calls in the POST handlers are per selected chat and stay.
- `ChatStat.Name` (status, chat list, dashboard event names) is now `chat_stats.name`: chat name, else contact name, else push name, over all JID forms (it was the chat name of the form a message was stored under). `archiver status` shows a person's name for a direct chat with no chats row.
- `chat_stats` times are min/max over all copies of a duplicated id (W2(d)); the oracle tests `TestChatStatsMatchesPreviousQuery` and `TestStatusReportEqualsSeparateQueries` were updated in a second `test:` commit (e9e145d): names from all forms, and 9001@lid oldest 4000 / oldest history 4000 (was 4500), each asserted explicitly. `TestStatusReportScansMessagesOnce` bound changed from `== 1` to `<= 1`; the new `TestStatusReportTouchesOnlyDerivedTablesAndLedger` pins zero (opcode check of the b-trees EXPLAIN opens, plus no SCAN of messages/media/message_events in the plan).
- `OpenGapCount` keeps `COUNT(*) FROM open_gaps` (a view over `chat_canonical`); its cost is O(undecryptable events), not O(chats); `SUM(gaps_open)` would miss a chat known only from events (no `chat_stats` row, W2(d)). `StatusReport` carries gaps per chat from `chat_stats.gaps_open`.
- `Usage.ByChat` is grouped by canonical JID (was per stored JID form) and still sums `media` rows: a media row alone has no `chat_stats` row, and `TestUsageSumsDoneMediaByChatAndType` stores media for chats with no message.
- `StatusReport` lists only chats with messages (as before); `ChatList` adds the quiet chats (rules, sightings, directory) from the same table, ordered newest first, quiet after message chats at equal `last_ts`. The name filter stays in Go (case-insensitive over a few hundred rows).
- Scale test derives only `chat_canonical` and `chat_stats` (`rebuildCanonical` + `statsSelect(false)`, the code RebuildDerived runs): the FTS build took 5 m 18 s at 300k under `-race`. `make check-fast` now passes `-short`, which skips the scale test; `make test` runs it twice (50k under `-race`, 300k without).
- The first red scale run on the old code was killed after 15 min under `-race` (the old query rescans messages); the plan-shape test is the red evidence for it.

## Known divergences (W2(e), accepted)

- An empty contact name never erases a stored one (same rule as `push_name`): a contact removed from the address book keeps its last name. A JID that is not a participant yet gets a row (so a chat that appears later is named at once); this puts every contact of the phone into `participants`, not only those who wrote.
- `updated_at` of a participant is the time of its last push-name OR contact-name change.
- Done in W2(f): `ChatName`, `ChatList` and the chat page sender names use the `nameExpr` order.
- Equivalent or unkillable mutants: ignoring the commit error in `SetContactNames` (no way to make a SQLite commit fail in a test); showing an event to the syncer before instead of after the handler (`Notify` only queues a request that waits out the debounce).
- `whatsmeow`'s `ContactStore.PutContactName` names its parameters `(fullName, firstName)` but the SQL store and the app-state caller use `(first, full)`; the columns read back are right (a test against a real SQL contact store pins it). The syncer calls only `GetAllContacts`; nothing is sent and `session.db` is not copied (CON-1).
- A second `test:` commit (fa1b3c4) was made during green with the lock lifted for it only (the hook refuses test files while the lock exists); it only adds a test for a surviving mutant.

## Known divergences (W2(d), accepted)

- No service lock or pid file exists, so `rebuild-derived` takes SQLite's exclusive lock (`store.OpenExclusive`, `busy_timeout(0)`, `locking_mode=EXCLUSIVE`): it fails with `ErrInUse` while any connection (the service) has the archive open, and nothing can open it meanwhile. Checked across processes with the `sqlite3` CLI as the holder.
- `chat_stats` times are over all message rows of the canonical (min/max of `ts`), `messages` is `COUNT(DISTINCT id)`. The old status query picked one row per duplicated id; they differ only for a merged duplicate whose two copies carry different `ts`.
- A media row alone (no message, chat row, rule or sighting) makes no `chat_stats` row, in any state: R20's four sources only. Its counts are read when the chat becomes seen. A chat known only from events has no row either until then; its gaps are counted then.
- The document filename in the FTS body comes from the message's `media` row (same batch, or a later stand-alone row, which re-indexes the message). The `contact`/`contacts` body adds `details.display_name` and the listed contacts.
- `rebuild-derived` holds SQLite's exclusive lock for the whole run: a service started meanwhile fails `Open` after its 5 s busy timeout, read-only tools (`status`, `chats`) block until it ends (correct), and `OpenExclusive` runs `migrate` like `Open`. The no-argument usage text says to stop the service first.
- No production FTS query helper exists yet. The search UI task must fold the query with `Fold`, phrase-quote it (escape `"` by doubling), and route queries whose terms are all 3+ characters to `search_tri` and any term of 1-2 characters to `search_fts` (spike 8). `ids` in `derived_helpers_test.go` shows the quoting.
- Task (e) called `refreshIdentity` after writing `participants.contact_name`: done in W2(e), see below.
- Done in W2(f): the status report reads `chat_stats`.
- The red commit has stubs (`Fold`, `RebuildDerived`, `OpenExclusive`) so the tests fail on assertions. A second `test:` commit (made during green, lock lifted for it only) adds tests for mutants that survived the first round (merge identity and gaps, single contact name). One mutant is equivalent: dropping and recreating the FTS tables behaves like `DELETE FROM` them (a test pins the DDL).

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

- ChatList takes rules/seen/people from `canonicalOf` (smallest LID mapped directly) but aggregates from `chat_canonical`; `Aliases` still reads `id_mappings` (H1 test). Done in W2(f) except `Aliases` (see the W2(f) divergences).
- Decide-before-Apply: a media-rules read failure now holds the message (accepted; same DB, so Apply would fail too).
- Read changes accepted: storage "by type" shows a separate `voice` row; activity detail says `voice`; replay stores media rows as `skipped`.
- R8 tie-break: "smallest LID" is by string order ("10@lid" < "9@lid"), same as the old `canonicalOf`; task (d) `RebuildDerived` must use the same comparison.

## Open questions

- (none open from W1; ADR-39 and charter v1.3 signed by Hamza 26 Oct.)
- W3 and W4 run in parallel after W2 merges.
