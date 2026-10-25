Single status record for V1 (max 150 lines, update in place; history lives in git and the archived phase log).

## Done

- Phases 0-2 finished and deployed: a87a30f on ryzen since 2026-10-20 (old log: `docs/reviews/2026-10-26-progress-phase0-2-archive.md`).
- 2026-10-26 V1 plan committed: 325a8e6, `docs/plans/2026-10-26-v1-plan.md`.
- Week review report: `docs/reviews/2026-10-26-week-review-issues.md`.
- W0 step 1 done: 7041f7c, pushed.
- W0 step 4 started: check-fast / test-quiet / Stop hook in 6278eff.

## In flight

- W0 remainder:
  - Step 2: ryzen evidence (not started).
  - Step 3: Hamza-only: stop and unlink the practice instance on the phone; router reservation for MAC 2c:f0:5d:3b:67:65 or a static IP in netplan; delete `stc_wifi_*` profiles and stale DNS 192.168.68.1; reboot test.
  - Step 4: skills `ryzen-evidence` and `deploy-ryzen`.

## Rulings

- (a) The `add-message-kind` skill is deferred to W2, after `docs/v1/schema.md` exists.
- (b) W0 is done on main without a worktree.
- (c) No Co-Authored-By trailer on commits.

## Open questions

- ADR-39 (community expand under CON-1) to be decided in W1.
- W3 and W4 run in parallel after W2 merges.
