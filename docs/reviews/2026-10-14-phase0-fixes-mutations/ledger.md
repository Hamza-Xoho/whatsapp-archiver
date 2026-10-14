# SDD ledger — plan: docs/prompts/claude-code-phase0-fixes.md
Branch: phase0-fixes (from main 6c3a6d9). Spec: docs/reviews/2026-10-14-phase0-review.md + docs/plans/2026-10-14-phase0-capture-logger.md + charter v1.1.
Ruling: Checkpoints after each stage override the skill's continuous-execution rule — the user's prompt demands them — cost if wrong: slower progress.
Ruling: Work on branch phase0-fixes in-place (no worktree) — .tools/.cache are repo-relative and large — cost if wrong: none, branch is isolated.
Ruling: Briefs are hand-written per finding (plan is a prompt, not Task-numbered) — cost: none.
Stage 0 layout: complete (6c3a6d9), approved by Hamza; repo to be made private by Hamza before push.
M1: fix round 1/1 (1 addressed, 0 open — clean-clone .tools mkdir; commits b4c9e06..44a2d05). Re-review done by controller (1-line diff + plain clean-env make check exit 0, 5 archs).
M1: minor (deferred): `$(LINT) --version 2>/dev/null` probe in tools target; shasum fallback unexercised; setup-go now redundant in CI.
M1: complete (commits ff44ba5..44a2d05, review clean after round 1)
- Repo confirmed private by Hamza 2026-10-14 (push still needs his ask).
Ruling: Red commits that fail only on compile of new exported API (WithClock, ErrConstraint, Policy.Permanent) are accepted, provided behavioural failure was shown with stubs in scratch — Go cannot express red tests for new API otherwise — cost if wrong: weaker red evidence.
B1+H2: complete (commits f069ad5..dcb10ef, review clean; 11 mutants killed: rf5a rf5b b1-map b1-enq b1-ledger rf1c rf1d h2-sleep h2-perm h2-classify h2-retry-perm)
B1+H2: minor (deferred → carry into H6, which touches UpsertChat): UpsertChat/UpsertParticipant/InsertMedia/alias-check errors bypass store.classify, so a constraint error there loops forever at 160ms.
B1+H2: minor (deferred → H6): persist's forever-loop should switch to the permanent path if isPermanent(err) becomes true mid-loop.
B1+H2: minor (deferred → H6): TestOtherErrorsAreNotConstraintErrors uses a non-sqlite error; add a real non-constraint *sqlite.Error negative + a positive through insert().
B1+H2: minor (deferred → M5/serve): Handle uses context.Background(); shutdown with a persistent write failure blocks the handler. Wire a shutdown ctx.
B1+H2: minor (deferred): permanent failure logged twice at Error.
Ruling (B2): temp ban with Expire==0 → pause 24h then retry — whatsmeow gives no expiry — cost: one handshake/day during an indefinite ban.
Ruling (B2): stream replaced → 5m doubling to 1h cap, reset after 6h quiet — avoids a tug-of-war with another client — cost: up to 1h capture gap if the other client goes away.
Ruling (B2): connect error → 5s doubling to 5m cap — cost: none significant.
B2+M3: complete (commits dcb10ef..5f6f23d, review clean; 10 mutants killed)
B2: minor → carried into H18: logout test doesn't prove "keeps blocking"; forwarding switch + sleepCtx untested; unknown *events.ConnectFailure not forwarded → idle forever; loop test with changed=false.
B2: minor (deferred): Client.version not synchronized (no race today, fragile); spurious refresh-failed log on shutdown; walog test mutates globals; comment on ctl-buffer invariant.
Ruling: Hamza said 'Keep going' (2026-10-14) — treat as go-ahead to continue through later stages, posting a summary at each stage boundary without blocking — cost if wrong: he wanted to approve each stage; work is on a branch and revertible.
Ruling (H8): non-edit/non-revoke protocol messages stored as message_events kind "protocol" (Value = type) — keeps raw data without false "deleted" claims — cost: extra rows.
Ruling (H5): history parser built in internal/wa on whatsmeow ParseWebMessage with an unconnected client; edits detected before parse — cost: if NewClient has side effects, fall back to a *wa.Client method.
H18+M13(a,b)+B2 carry-overs: complete (commits 5f6f23d..b5206fd, review clean; 10 mutants killed). Controller verified b43e550 fixture-only + commit separation.
H18: minor (deferred): handler pairLine print untested & writes os.Stdout not `out`; IsPaired error → exit 1 (restart loop applies); ntfy unreachable → up to 1 min to exit; QR timeout keeps pair running until Ctrl-C.
STAGE 1 complete at b5206fd.
Stage 1 full mutation re-run at b5206fd: 31/31 KILLED (mutations/stage1-run.txt).
H5+H6+H9: complete (commits b5206fd..4d36883, review clean; 14 new mutants killed, 45/45 total)
Ruling (H9): ledger ID = content hash; an identical redelivered chunk is one ledger row (same chunk; messages dedupe anyway) — Task 12 step 9 counts distinct chunks, not deliveries — cost if wrong: a redelivery isn't visible in the ledger count.
H6b follow-up (dispatched to the H5 implementer): pin H9 dedup with a test; marshal error handled (AllowPartial + wrap); excluded-chat test asserts participants+media empty; LID-DM own-sender test; UpsertParticipant keeps a known display name on empty push name.
H5: minor (deferred): unparsable conversation ID → chat row under raw string vs messages under RemoteJID; wa.NewHistoryParser exported for tests only; own LID↔PN mapping for LID-addressed groups (check later, CON-7).
H6b: complete (commits 4d36883..75fc7bb, review clean; 47/47 mutants). minor (deferred): marshal-error branch untested; no mutants for items 4/5 pinning tests.
H1: implementer DONE_WITH_CONCERNS (d678734..6d08e0f). Ruling: scaling test resized to N=4000/500-row windows in a new test: commit, provided it still fails against the old view query — 66s under -race is unacceptable for make check; the deterministic plan assertion stays the primary guard — cost: weaker ratio signal.
Ruling (H1): store.Aliases (called per event by Filter.Keep) must use the same indexed alias resolution as dedupeQuery (DRY), with a plan-assertion red test — same scaling bug on the live path.
Ruling (M4): media transient failures — 5 attempts/round at 1s base, re-queue after 15 min, up to 20 attempts then failed — supersedes plan "after 5 failed attempts → failed" per review M4 — cost: a bad file retried ~45 min.
Ruling (H3/M4): media table gains from_me and next_attempt_at columns by editing 0001_raw.sql (no deployed DB; M11 deferred).
Ruling (H10): alertGate semantics — condition rules clear with one 'recovered'; state alerts clear on connected; event alerts gated only; backup clears on next success — cost: minor noise/no-noise tradeoffs.
H1: complete (commits 75fc7bb..fb8310b, review clean; 51/51 mutants at 3f73b22)
H1: minor → queued task "filter-fail-closed": main.go discards Aliases error → exclusion filter fails OPEN on read error (privacy). Ruling: Filter.Keep returns (bool, error); Ingest runs the filter check inside persist so a read error retries until it succeeds (neither leaks an excluded chat nor drops a kept one). Also wrap Aliases errors. Add test pinning multi-LID union of Aliases.
H1: minor (deferred): messages_resolved emits a PN-chat message once per LID when a PN maps to several LIDs (pre-existing view behaviour); PROGRESS H1 first bullet superseded.
H7+H8: review Needs fixes (I1 GroupInfo fields dropped → distinct changes collapse; I2 repeat JoinedGroup collapses; I3 encrypted edits). Fix round 1 queued until the health implementer finishes (shared green.lock).
Ruling (H7 I3): SecretEncryptedMessage with SecretEncType MESSAGE_EDIT → KindEdit, target TargetMessageKey.ID, empty NewText, raw kept, no message row — Table 1 "edit encryption changed" — cost: none.
Ruling (H7 M2/M3): stableID uses ts in milliseconds; messageText falls back to media captions — cheap, prevents collapse/loss.
Ruling (H8 M6): APP_STATE_SYNC_KEY_SHARE (and any protocol type carrying key material) stored as a protocol event WITHOUT raw proto — archive.db is backed up and session keys must never be (ADR-26 spirit) — cost: raw of those messages not archived. FLAG FOR HAMZA.
H7: minor (deferred): FromFullSync in ID gives at most one extra row; SenderPN dropped from group events; keyless identical protocol messages collapse.
H10+M2: implementer DONE (d6092ee..6d0284d; 85/85 mutants). Review dispatched. H7/H8 fix round 1 sent to original implementer (I1, I2, I3, M2, M3, M6).
H10+M2: review Needs fixes (Important: Notify errors swallowed). Fix round 1 queued after H7 fix round (lock): log Notify errors (red test); fired recorded only when enqueued.
Ruling (H10): skip no_events while state != connected (the disconnected rule covers it) — cuts redundant alerts — cost: none.
Ruling (H10): temp-ban body reads "for <duration> (until <now+duration in configured zone or UTC>)" — detail is a duration.
H10: minor (deferred): go.sum not tidy (M6 — run `go mod tidy` with network later); serve doesn't join mon.Run (M5); recovered messages in map order.
H7+H8: fix round 1/5 (implementer: 6 addressed; commits 6d0284d..16fc183; 93/93 mutants). Re-review dispatched.
Ruling (H7): changing an assertion in TestNonRevokeProtocolMessageIsNotARevoke (a test written on this branch in H8) to expect empty raw for key-carrying types is accepted — requirement changed by the M6 ruling, not a weakening of reviewed behaviour.
Ruling (ingest follow-up): other SecretEncryptedMessage types (POLL_EDIT, EVENT_EDIT, POLL_ADD_OPTION, MESSAGE_SCHEDULE) → message_events kind "protocol"-style "secret_encrypted" with type name, target TargetMessageKey.ID, raw kept, no message row. Queue with filter-fail-closed task.
Deferred → H14 (recorder/scrub): recorder writes key-share raw protos to fixture files; scrub must strip key material.
H7+H8: re-review round 1: 6/6 addressed, no new breakage. complete (commits fb8310b..16fc183; 93/93 mutants)
H7: minor (deferred): keyMaterial keyed on Type only; group struct JSON tied to whatsmeow field names; edit re-delivered with different envelope → second event.
STAGE 2 complete at 16fc183 (H5 H6 H6b H9 H1 H7 H8) — open follow-up: ingest-followup (filter fail-closed, secret-encrypted types).
Hamza 2026-10-14: repo is private; M6 key-material ruling delegated to controller — ruling stands (strip raw for key-carrying protocol types).
H10: fix round 1/5 (4 addressed; 16fc183..eb0fe2b; 98/98). Ruling (round 2): never-connected startup counts as disconnected since monitor creation — closes a silent-startup gap in my earlier ruling — cost: one alert 15 min after a start that never connects.
Repo verified private via GitHub API (2026-10-14). Push still only when Hamza asks.
Pushed phase0-fixes to origin at f48e20f (Hamza: push whenever appropriate; branch only, main untouched).
H10: fix round 2/5 done (6483e86..e60600a; 99/99). Re-review of rounds 1+2 dispatched. Media BASE=e60600a.
H10+M2: re-review rounds 1+2: 5/5 addressed. complete (commits d6092ee..e60600a, 99/99 mutants)
H10: minor (deferred): clear() ignores dropped "recovered"; empty ban detail wording; stale PROGRESS line; after reconnect old LastEvent can raise no_events on first check (anchor no_events at connected time — cheap follow-up).
STAGE 3 health done; media in progress (BASE e60600a).
Briefs ready: h12, backup (H15/H16), h17, h14. Remaining to write: h13+m5, m8.
Briefs ready: h13-m5, m8.
Hamza 2026-10-14 approved parallel worktrees. Ruling: independent tasks run in worktrees ../wt-<task> (branch fix/<task> off phase0-fixes; .tools/.cache/gomod/.cache/gocache symlinked); each writes mutation specs to mutations/specs-<task>.json and runs with MUT_REPO=<worktree>; controller merges into phase0-fixes one at a time and re-runs make check — cost: merge conflicts in PROGRESS.md (append-only, keep both).
Parallel wave 1: H12 (wt-h12), backup H15/H16 (wt-backup), base a67c164; media continues in main checkout.
backup: implementer DONE (fix/backup a67c164..3d8bdb3; 6/6 own specs). Ruling: retention failure after a verified snapshot keeps it and returns the error — a good backup must never be deleted by housekeeping — cost: none. Fix round 1 sent.
backup H15+H16: complete on fix/backup (a67c164..f0db190, review clean; 7/7 own specs). Merge pending until media finishes in main checkout.
backup: minor (deferred): retention by name time (clock step back edge); Run opens archive.db rw-create (use mode=ro); test name overstates "not writable"; drift test ignores systemd path prefixes.
H12: implementer DONE_WITH_CONCERNS (fix/h12 a67c164..b8cdcb6; 5/5 own specs; 7 shared NOMATCH = media code not in branch). Review dispatched.
H12: review Needs fixes (Important: interface/generic wrapper in a wa file not importing whatsmeow bypasses both guards; raw client escapes wa via embedding/alias). Ruling: widen ruling 3(b) — archtest prefix rule applies to every non-test file under internal/wa; in internal/wa also flag interface method declarations and generic-constraint methods with banned-prefix names (allow-list excepted; SendMediaRetryReceipt only in rerequest.go), any embedded field or type alias of whatsmeow.Client, and any exported func/method/field/var whose type is or contains *whatsmeow.Client — closes every non-reflection path found — cost: possible false positives inside wa, which fail safe.
Ruling (H12 minors in same round): anchor rerequest path regex; SendMediaRetryReceipt in forbidigo explicit list with rerequest-only exclusion; record word-boundary deviation and reflection as a known limit in PROGRESS + AGENTS.md CON-1 line.
H12: fix round 1/5 implementer done (b8cdcb6..8df2f3e; 9/9 own). Shared rf3a/rf3d SURVIVED + 13 NOMATCH at branch base — verify after merge.
PAUSED 2026-10-14 for model switch; handover docs/prompts/2026-10-14-phase0-fixes-handover.md. Agents stopped: media wrap-up (full mutation run at e85b743 unverified), H12 re-review (unfinished).
