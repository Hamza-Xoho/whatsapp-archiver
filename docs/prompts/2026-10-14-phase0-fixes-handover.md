# Handover: phase 0 fixes (paused 2026-10-14)

Paused on request so work can continue on a different model. Every agent was stopped on a clean tree with no `green.lock`. All three branches are pushed to the private repo.

**Read first:** `docs/prompts/claude-code-phase0-fixes.md` (the brief this work carries out), `AGENTS.md`, then this file. The full decision log (every task, commit range, fix round, deferred minor and ruling) is `docs/reviews/2026-10-14-phase0-fixes-mutations/ledger.md`. A live copy is in the git-ignored workspace `.superpowers/sdd/claude-code-phase0-fixes/`, which also holds every implementer report and review diff.

## How the work runs

- **Subagent-driven development** (superpowers skill). For each finding: write a brief, then dispatch one implementer and one task reviewer. Fix rounds resume the same implementer, then a scoped re-review checks only the fix. The controller never edits code itself.
- **Strict red, then green, per finding.**
  - Commit the failing test as `test:` (only `_test.go` or testdata).
  - `touch .claude/green.lock`, write the least code, run `make check`, `rm .claude/green.lock`, commit as `fix:`.
  - The pre-commit hook (`core.hooksPath=.githooks`) blocks test edits while the lock exists.
- **No `Co-Authored-By` trailer.** Push only reviewed work. Hamza said to push whenever appropriate; push branches, not `main`.
- **Never connect to WhatsApp.** Task 12 belongs to Hamza.
- **`make check` works with no PATH prefix.** It bootstraps the pinned Go and golangci-lint into `.tools/` (fixed in M1).
- **Mutations:**
  - Tool: `docs/reviews/2026-10-14-phase0-fixes-mutations/run.py`. The workspace copy is at `.superpowers/sdd/claude-code-phase0-fixes/mutations/run.py`.
  - Command: `python3 <run.py> <specs.json> [names…]`. Each mutant is applied to a fresh `git archive HEAD` copy. Set `MUT_REPO=<worktree>` to mutate a worktree's HEAD.
  - Specs: `specs.json` (main line, 126 entries), `specs-h12.json`, `specs-backup.json`. Merge the latter two into `specs.json` when those branches merge.
  - Every finding's mutations must be KILLED.
- **Parallel worktrees** (Hamza approved):
  - Location `../wt-<task>`, branch `fix/<task>` off `phase0-fixes`.
  - `.tools`, `.cache/gomod` and `.cache/gocache` are symlinked from the main checkout. `/.tools` is in `.git/info/exclude`.
  - Each worktree writes its own `specs-<task>.json`.
  - The controller merges into `phase0-fixes` one at a time and re-runs `make check` after each merge.
  - Never run two implementers in the same checkout: they share one green-lock.

## State of the branches

| Branch | Head | State |
| --- | --- | --- |
| `main` | `6c3a6d9` | Stage 0 layout commit, 1 ahead of `origin/main` (not pushed). |
| `phase0-fixes` (main checkout) | `e85b743` | Stages 0–3. Media is committed but **not yet task-reviewed** (see below). |
| `fix/backup` (`../wt-backup`) | `f0db190` | H15, H16 and the Task 8 tests. **Reviewed and approved; not merged.** |
| `fix/h12` (`../wt-h12`) | `8df2f3e` | H12, CON-1 prefix guard. Fix round 1 is done. **The scoped re-review was stopped before it finished**, so re-run it. |

## Done and reviewed (in `phase0-fixes`)

- Stage 0: layout tidy, M1 (tooling).
- Stage 1: B1, H2, B2 with M3, H18 with M13(a,b).
- Stage 2: H5, H6, H6b, H9, H1, H7, H8.
- Stage 3: H10 with M2 (health).
- The full mutation run at `e60600a` gave 99/99 KILLED. Each later task reported its own runs KILLED.

## Next steps, in order

1. **Review media** (H11, H3, H4, M4; commits `e60600a..e85b743`).
   - Report: `.superpowers/sdd/claude-code-phase0-fixes/media-report.md`.
   - The implementer's final full mutation run was cut off, so run `specs.json` at HEAD first. All entries must be KILLED.
   - Then dispatch the task reviewer with `media-brief.md`, the report, and a diff of `e60600a..e85b743`.
   - Report concerns to weigh:
     - a flaky pre-existing `TestDocumentDownloads` (50 ms real sleep);
     - cancellation mid-round is logged as a failure;
     - a possible expire/request/download repeat cycle.
2. **Re-review H12 fix round 1.** Use the diff `review-h12-b8cdcb6..8df2f3e.diff` in the workspace. Rebuild the canary for the bypasses: interface wrapper, generic constraint, embedding, alias, exported `*whatsmeow.Client`, method value, shadowed import. The findings and rulings are in the ledger (lines starting `H12:`).
3. **Merge `fix/backup` and then `fix/h12` into `phase0-fixes`.**
   - `PROGRESS.md` conflicts are append-only: keep both sections.
   - Merge `specs-*.json` into `specs.json`.
   - Run `make check` and the full mutation run, then push.
   - The H12 worktree reported 2 SURVIVED (rf3a, rf3d) and 13 NOMATCH in shared specs. They are media specs whose tests came after its base, so re-check them after the merge.
4. **Remaining tasks.** Briefs are ready in `docs/reviews/2026-10-14-phase0-fixes-mutations/briefs/`. These can run in parallel worktrees after step 3, except where noted:
   - `ingest-followup-brief.md`: the exclusion filter fails closed on alias read errors (privacy), and the other secret-encrypted message types.
   - `h17-brief.md`: `status` and `chats` open read-only, use the time zone, and get golden tests.
   - `h14-brief.md`: record, scrub and replay for every kind; `DumpRaw`; the synthetic golden; CLI `scrub`/`replay`. This is the largest task.
   - `h13-m5-brief.md`: the gap list of missing tests, goleak, and `serve` joining its goroutines with a shutdown ctx. **Run this after the others merge.**
   - `m8-brief.md`: the setup guide and acceptance log (docs). **Run this last.**
5. **Final checks.**
   - Run the full mutation run and confirm every Review Focus 1–10 row is killed. The original review list is in `docs/reviews/2026-10-14-phase0-review.md` §5.
   - Re-run the probes in `docs/reviews/2026-10-14-phase0-review-evidence/probes/` in a scratch copy.
   - Dispatch the whole-branch review on the most capable model, pointed at the ledger's deferred-minor lines.
   - Do one fix wave, then send the final report. It must include the per-stage changes, mutation counts before and after, deferrals, the decisions only Hamza can make, and the Linux build and copy commands. Use `make build-all` to build, then copy `.cache/build/archiver-linux-amd64` or `-arm64`, depending on `uname -m` on the server.
6. The "can wait" list comes after that: M6 (`go mod tidy`, since go.sum is untidy after goleak), M7, M9–M13, L1–L5. Also the deferred minors in the ledger.

## Rulings made on Hamza's behalf

Each ruling gives the cost if it's wrong.

1. Work on branch `phase0-fixes` in the main checkout; later, worktrees for parallel work. Cost: none.
2. Red commits that fail only because new exported API does not compile yet are accepted, provided a behavioural red was shown against a stub. Cost: weaker red evidence.
3. "Keep going" means continue through the stages, posting a summary at each boundary. Cost: Hamza wanted per-stage approval. The work is on a branch and revertible.
4. Write retry (B1, H2): 5 tries at 10 ms base, one `WriteFailed`, then retry forever at 160 ms. Constraint errors are permanent: alerted and not retried. Cost: a constraint-failing event is not stored, but it is alerted.
5. Reconnect (B2):
   - temp ban with no expiry pauses 24 h;
   - stream replaced backs off from 5 min to a 1 h cap, resetting after 6 h quiet;
   - connect errors back off from 5 s to a 5 min cap;
   - unknown connect failures retry every 30 min.

   Cost: up to 1 h capture gap after a takeover, and one handshake a day during an indefinite ban.
6. Unpaired `run` exits with code 3 and `RestartPreventExitStatus=3`, and sends one ntfy. Cost: none.
7. Protocol messages that are not edits or revokes are stored as message events of kind `protocol`. Cost: extra rows.
8. History is parsed with whatsmeow `ParseWebMessage` on an unconnected client inside `internal/wa`; edits are detected before parsing. Cost: low.
9. The history ledger ID is a content hash, so an identical redelivered chunk is one ledger row. Task 12 step 9 therefore counts distinct chunks. Cost: redeliveries are not visible in the ledger.
10. The H1 scaling test is redesigned: 100 inserts empty against 100 after a 20k seed, ratio ≤ 4, plus an EXPLAIN assertion. `Aliases` uses the same indexed alias set; a phone number with several LIDs now returns their union. Cost: the union can only over-exclude.
11. Media:
    - a round is 5 attempts at 1 s base;
    - a failed round re-queues after 15 min;
    - after 20 attempts the row is `failed`;
    - the media table gains `from_me` and `next_attempt_at` by editing `0001_raw.sql`, since no DB is deployed and M11 migrations are deferred.

    Cost: a bad file is retried for about 45 min.
12. Health:
    - `alertGate` sends at most once per 6 h per key, with one "Recovered" message;
    - no-events is only checked while connected;
    - a process that never connects counts as disconnected from start;
    - the temp-ban body says "for <d> (until <UTC time>)".

    Cost: minor alert-noise trade-offs.
13. Chat events store every GroupInfo field. IDs use milliseconds. Secret-encrypted MESSAGE_EDIT becomes an edit. Edited media captions become NewText. Cost: none.
14. **Key material:** APP_STATE_SYNC_KEY_SHARE, SHARED_DEVICE_CONTACT_HASH_KEY_SHARE, HISTORY_SYNC_NOTIFICATION and PEER_DATA_OPERATION_REQUEST_RESPONSE_MESSAGE are stored without their raw proto, because `archive.db` is backed up. Hamza delegated this call. Cost: their raw bytes are not archived.
15. One assertion was changed in `TestNonRevokeProtocolMessageIsNotARevoke`, a test written on this branch, to match ruling 14. Cost: none.
16. The exclusion filter must fail closed: alias read errors retry inside `persist` (queued in `ingest-followup`). The other secret-encrypted types become `secret_encrypted` events. Cost: none.
17. Backup: retention touches only folders whose names parse as snapshot names. A failed snapshot removes its own folder, but a verified snapshot is kept even if retention fails. The unit gains `/srv/backups/whatsapp-archiver` in `ReadWritePaths`. Cost: none.
18. H12, the CON-1 guard:
    - the prefix ban is enforced by both archtest and forbidigo;
    - the allow-list is exactly `SetProxy`, `SetProxyAddress`, `SetSOCKSProxy`, `SetMediaHTTPClient`, `SetWebsocketHTTPClient`, `SetPreLoginHTTPClient`, `SetMaxParallelRetryReceiptHandling`;
    - `SendMediaRetryReceipt` is allowed only in `rerequest.go`;
    - the rule covers all of `internal/wa` (interface and constraint methods, embedding, alias, exported `*whatsmeow.Client`);
    - the prefix must end at a word boundary;
    - reflection is a known, unguarded limit.

    Cost: false positives inside `wa`, which fail safe.

## Known open items to carry forward

Details are in the ledger:

- the go.sum is untidy;
- `serve` does not join its goroutines, and `Handle` uses `context.Background()` (both in H13/M5);
- after a reconnect, a stale `LastEvent` can raise one no-events alert;
- `messages_resolved` emits a phone-number chat message once per LID when the number maps to several LIDs;
- the backup `Run` opens `archive.db` read-write;
- the recorder writes key-share raw protos to fixtures (fixed by the H14 scrub ruling).
