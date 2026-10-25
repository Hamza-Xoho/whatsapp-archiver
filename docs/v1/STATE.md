Single status record for V1 (max 150 lines, update in place; history lives in git and the archived phase log).

## Done

- Phases 0-2 finished and deployed: a87a30f on ryzen since 2026-10-20 (old log: `docs/reviews/2026-10-26-progress-phase0-2-archive.md`).
- 2026-10-26 V1 plan committed: 325a8e6, `docs/plans/2026-10-26-v1-plan.md`.
- Week review report: `docs/reviews/2026-10-26-week-review-issues.md`.
- W0 step 1 done: 7041f7c, pushed.
- W0 step 4 done: check-fast / test-quiet / Stop hook (6278eff); PROGRESS moved + this file (2b88677); skills `ryzen-evidence`, `deploy-ryzen` (f713d23).
- W0 step 2 done: ryzen evidence, Appendix A of the week review (ce81d4d). Findings: ntfy configured, only alert attempt failed on DNS; 403 = 24,536 of 25,358 failed media (no 404/410); practice CPU ~15% not explained by media backlog; `attempts` up to 20 vs text "after 5 attempts" (check in W4).
- W1 done: charter v1.3 (V1 re-scope, ADR-34 to ADR-39); `docs/phase2-acceptance.md` practice path fixed.

## In flight

- W0 step 3 (Hamza): done 26 Oct: practice instance unlinked on the phone and its process stopped (PID 288900); only the `SaudiSpyWithMyWifi` Wi-Fi profile left; stale DNS 192.168.68.1 gone. Reboot test still open (optional).
- `sudo perf` on the practice PID is moot: the instance is stopped.

## Rulings

- (a) The `add-message-kind` skill is deferred to W2, after `docs/v1/schema.md` exists.
- (b) W0 is done on main without a worktree.
- (c) No Co-Authored-By trailer on commits.
- (d) Router reservation / static IP for ryzen skipped by Hamza (26 Oct): the connection is only needed for coding, so the address may change; `deploy-ryzen` and docs use 192.168.0.105 and must be rechecked if it does.

## Open questions

- ADR-39 (community expand under CON-1): proposed in charter v1.3, Hamza signs.
- W3 and W4 run in parallel after W2 merges.
