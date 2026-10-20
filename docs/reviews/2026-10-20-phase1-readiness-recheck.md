# Phase 1 readiness re-check, 20 Oct 2026

Run on the build deployed in Task 7 (`8dbdd0f`), against `main` at `8257024`. Server checks ran at 07:40 +03.

## Verdict

**READY FOR PHASE 1, WITH WAIVED ACCEPTANCE STEPS**

Waived (skipped by Hamza, 20 Oct): steps 1, 2, 3 (remaining items), 4, 6, 7, 8, 9, 10, 12 and 13. The charter's phase 0 exit criterion (spikes 1 to 5 recorded, oldest message checked per group, a 7-day audit with zero missing) is not met for these steps.

History of this verdict: the first run (07:40) was NOT READY. Checks 6 and 7 were BLOCKED because the permission system refused the agent's two temporary server writes, and check 10 listed steps 5 and 11 as open because their boxes were unticked although their results were recorded. With Hamza's approval in chat, the controller re-ran the secret counter at about 07:50 (checks 6 and 7 now PASS) and ticked the boxes of steps 5 and 11 in `docs/phase0-acceptance.md` (results unchanged). No change was made to the live system because of any FAIL or BLOCKED result.

## Checks

| # | Result | Evidence |
|---|--------|----------|
| 1 | PASS | `git status --short` empty (before and after `make check`); `main` and `origin/main` both `8257024b236476fed9d34d4f3df858250126c41e`; `PROGRESS.md` is 121 lines. |
| 2 | PASS | `make check` exit 0; log in `.cache/readiness-check.log`; last lines are `build linux/amd64`, `linux/arm64`, `darwin/arm64`, `darwin/amd64`, `windows/amd64`. |
| 3 | PASS | `active`; SHA-256 `071ff74f...09ba578b` matches; `version` prints `whatsapp-archiver 8dbdd0f`; `health.json` has `"state": "connected"`, `"panics": 0`; `NRestarts=0`. |
| 4 | PASS | 2 WARN/ERROR lines since 01:19:48, both class (a): 04:33:20 WARN `Keepalive timed out`; 04:59:54 ERROR websocket read EOF, then `connection lost` and `connection restored` 1.74 s later. 2 in 24 h (limit 10), no "Disconnected for" alert, no other WARN/ERROR. |
| 5 | PASS | `messages` count 228139, above the 228135 baseline (only 4 more; recorded as a thin margin, not a failure). |
| 6 | PASS (re-run) | `.backup` copy of the live archive: messages 228139, message_events 3824, media 32471, chat_events 100 rows, `with_message_secret=0` and `not_parsed_as_message=0` in all four. Copy and tool deleted; `ls` confirms both gone. (First run BLOCKED: server writes refused by the permission system.) |
| 7 | PASS (re-run) | Newest `T0030` snapshot `20261020T003000.004231523Z`: `PRAGMA integrity_check` = `ok`; secret counter: messages 228135, message_events 3823, media 32468, chat_events 99 rows, `with_message_secret=0` in all four. Media copy: `Result=success`, `ExecMainStatus=0`, exit 04:30:53 today; `du -sh` 2.9G backup vs 2.9G live media. |
| 8 | PASS | `history: in progress (99%), chunks 57, last chunk 2026-10-16 19:12 +03`. |
| 9 | PASS | `archiver.prev` exists, SHA-256 `adc871fe...72831` matches. No `archive.db.pre-scrub*` in `/var/lib/whatsapp-archiver/`; `archiver.before-19oct` not in `/opt/whatsapp-archiver/`. `/srv/backups/whatsapp-archiver/` holds only `20261019T003000.001590287Z`. |
| 10 | Only waived steps | See below. Nothing was run. |

Temporary server files: in the first run they were never created (the denied command made no changes). In the re-run both were created, used and deleted; `ls` afterwards reports `No such file or directory` for each.

## Waived or open acceptance steps (check 10)

Task 8 line in `PROGRESS.md` covers: step 1 versions, step 3 gaps, step 4 receipt audit, step 6 kill/restart + outage, steps 7/2/8/10 re-pair with `--record`, LID dedup and corpus, step 9 history reach, step 12 seven-day audit, spike 6, step 13.

- Step 1 (before pairing): skipped by Hamza (20 Oct)
- Step 2 (pair with recording): skipped by Hamza (20 Oct)
- Step 3 (test-group day, including the unchecked "nothing appeared on anyone's phone" item): skipped by Hamza (20 Oct)
- Step 4 (receipt audit, spike 4): skipped by Hamza (20 Oct)
- Step 5 (version-drift drill, spike 3): done (boxes ticked 20 Oct; results were already recorded: `client_outdated` confirmed. The first drill's missing recovered alert was the bug fixed in `1e52a6d`, and the 18 Oct re-run's recovered alert reached the phone at 22:08).
- Step 6 (kill and restart, outage): skipped by Hamza (20 Oct)
- Step 7 (unlink and re-pair): skipped by Hamza (20 Oct)
- Step 8 (LID dedup, spike 5): skipped by Hamza (20 Oct)
- Step 9 (history reach, spikes 1 and 2): skipped by Hamza (20 Oct)
- Step 10 (commit the corpus): skipped by Hamza (20 Oct)
- Step 11 (backup restore): done (boxes ticked 20 Oct; the pass result for snapshot `20261018T202402` was already recorded).
- Step 12 (seven-day audit, SC-1 and SC-10): skipped by Hamza (20 Oct)
- Step 13 (commit results, charter decision log): skipped by Hamza (20 Oct)

The charter's phase 0 exit criterion (spikes 1 to 5 recorded, oldest message checked per group, a 7-day audit with zero missing) is not met for the skipped steps.

## Remaining decisions for Hamza

- The 5 visible messages that were never recovered (Task 6): `...8328`, `...FCCF`, `...0E89`, `...4A9D`, `...B09C`. Step 12 says any miss halts phase 1.
- Whether 403 and bad-SHA history downloads count as "gone" under D4, and what happens to a history chunk that can never be stored (history reads 99%; FULL chunk 47 never arrived).
- Whether to store a source message ID on event rows so recovered event gaps can close (a schema change).
- Recording the phase order change (phase 2 before phase 1, anytopdf as a candidate intelligence layer) in the charter's decision log.
- Deleting the old media copy `/srv/backups/whatsapp-archiver-media` once the `/mnt/data` copy has run for a week.
