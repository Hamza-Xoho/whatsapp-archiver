Single status record for V1 (max 150 lines, update in place; history lives in git and the archived phase log).

## Done

- Phases 0-2 finished and deployed: a87a30f on ryzen since 2026-10-20 (old log: `docs/reviews/2026-10-26-progress-phase0-2-archive.md`).
- 2026-10-26 V1 plan committed: 325a8e6, `docs/plans/2026-10-26-v1-plan.md`.
- Week review report: `docs/reviews/2026-10-26-week-review-issues.md`.
- W0 step 1 done: 7041f7c, pushed.
- W0 step 4 done: check-fast / test-quiet / Stop hook (6278eff); PROGRESS moved + this file (2b88677); skills `ryzen-evidence`, `deploy-ryzen` (f713d23).
- W0 step 2 done: ryzen evidence, Appendix A of the week review (ce81d4d). Findings: ntfy configured, only alert attempt failed on DNS; 403 = 24,536 of 25,358 failed media (no 404/410); practice CPU ~15% not explained by media backlog; `attempts` up to 20 vs text "after 5 attempts" (check in W4).

## In flight

- W0 step 3: Hamza only: stop and unlink the practice instance on the phone; router reservation for MAC 2c:f0:5d:3b:67:65 or a static IP in netplan; delete `stc_wifi_*` profiles and stale DNS 192.168.68.1; reboot test.
- `sudo perf` on the practice PID needs Hamza's approval (perf not in sudoers); skip if the instance is stopped first.

## Rulings

- (a) The `add-message-kind` skill is deferred to W2, after `docs/v1/schema.md` exists.
- (b) W0 is done on main without a worktree.
- (c) No Co-Authored-By trailer on commits.

## Open questions

- ADR-39 (community expand under CON-1) to be decided in W1.
- W3 and W4 run in parallel after W2 merges.
