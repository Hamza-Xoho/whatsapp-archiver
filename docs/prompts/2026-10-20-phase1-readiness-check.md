# Phase 1 readiness re-check (Task 9 of `docs/plans/2026-10-19-phase1-gate.md`)

You are checking whether the WhatsApp Archiver is ready for phase 1, on the build deployed in Task 7. Nobody will answer questions while you run, so decide nothing that the rules below do not already cover. Produce one report and stop.

## Context

- Local repo: `/Users/hamza/Code/whatsapp-archiver/archiver`, branch `main`. Read `AGENTS.md` first, then the sections "Open before phase 1" and "Phase 1 gate" in `PROGRESS.md`.
- Server: `hamza@192.168.0.104` (`ryzen.local` may not resolve from the Mac). x86_64, SSH key login, and `sudo -n` works for the commands below. Service `archiver.service`. Data folder `/var/lib/whatsapp-archiver` (mode 0700, owned by `whatsapp-archiver`).
- Deployed binary `/opt/whatsapp-archiver/archiver`: expected SHA-256 `071ff74f09a02cbe6cf8a05192e8ebb1e7e1d8c9c096325e2dc6525c09ba578b`, and `archiver version` prints `whatsapp-archiver 8dbdd0f`. The rollback is `/opt/whatsapp-archiver/archiver.prev` (SHA-256 `adc871fec73d12a556c7c6a9947e8c9a9ac394f1e956984cc0400309b5b72831`).
- Last restart: 2026-10-20 01:19:46 +03. Baseline: the 03:30 snapshot of 20 Oct held 228135 messages.
- Backups go to `/mnt/data/backups/whatsapp-archiver/<UTC timestamp>/` at 03:30 local, and the media copy goes to `/mnt/data/backups/whatsapp-archiver-media/` at 04:30 local.
- Task 8 (Hamza's phone and server acceptance steps) was SKIPPED by Hamza's decision on 20 Oct 2026; see the Task 8 line in `PROGRESS.md`.

## Hard rules

- Never run `archiver pair` or `archiver run`, and never restart, stop, reconfigure or redeploy the service. If something looks broken, record it as a blocker and stop there.
- Never read message text. Use counts and aggregates only (`COUNT`, `MAX`, `GROUP BY`). Do not print rows from `messages`, `message_events` or `media`.
- Never connect to WhatsApp, and never send anything to anyone.
- Do not delete, move or overwrite any file on the server. Do not change `config.yaml`.
- Do not commit, push, merge or rebase. Do not use `git stash`. The only local file you write in the repo is the report.
- Server writes are limited to two things, each deleted after use: the counter tool in `/tmp`, and a temporary copy `/var/lib/whatsapp-archiver/readiness-check.db`.
- If a check cannot be completed, say so in the report. Do not guess a result.

## Checks

Record each check as PASS, FAIL or BLOCKED, with the evidence line.

1. **Local repo.** `git status --short` is empty, apart from the report you are writing. `git rev-parse main` equals `git ls-remote origin main`. `wc -l PROGRESS.md` is under 500.
2. **Local gate.** `make check` exits 0. Give the last lines from the log, and keep the log in `.cache/readiness-check.log`.
3. **Server service.** `systemctl is-active archiver` prints `active`. `sha256sum /opt/whatsapp-archiver/archiver` matches the expected SHA-256 above. `/opt/whatsapp-archiver/archiver version` prints `8dbdd0f`. `sudo -n cat /var/lib/whatsapp-archiver/health.json` shows `"state": "connected"` and `"panics": 0`. `systemctl show archiver -p NRestarts` is 0.
4. **Log since restart.** `sudo -n journalctl -u archiver --since "2026-10-20 01:19:48" --no-pager` has no `"level":"ERROR"` or `"level":"WARN"` lines, except these allowed classes:
   - (a) whatsmeow network WARN/ERROR within D3's bounds: at most 10 per 24 hours, a `connection restored` within 2 minutes of each `connection lost`, and no "Disconnected for" alert.
   - (b) `Node handling took` and `Node handling is taking long` WARNs, only within 15 minutes of a `history chunk imported` line (the history download and import run inside whatsmeow's node queue).
   - (c) The per-host `trying with next host` WARNs of a history download that timed out count as part of the same history window as (b).
   - (d) The media queue's `media row not saved` and `stale re-upload requests not failed` ERRORs with `"err":"context canceled"`, only within 2 seconds of a service stop. These are known shutdown noise, recorded under Task 7 in PROGRESS.md.

   Anything else is FAIL. To print lines, strip URLs first (`sed -E 's/https?:[^" ]*/URL/g'`) and trim chat and message IDs to their last 4 characters.
5. **Traffic.** Count messages now with `sudo -n -u whatsapp-archiver sqlite3 -readonly /var/lib/whatsapp-archiver/archive.db "SELECT COUNT(*) FROM messages"`. PASS only if the count is higher than 228135. If it is not, that is a BLOCKER: report it and do not try to fix it.
6. **Secrets.** Build the counter locally: `GOOS=linux GOARCH=amd64 .tools/go/bin/go build -o .cache/build/secretcount-linux-amd64 ./.cache/secretcount`. Make a consistent copy of the live archive with `sudo -n -u whatsapp-archiver sqlite3 /var/lib/whatsapp-archiver/archive.db ".backup /var/lib/whatsapp-archiver/readiness-check.db"`. Copy the counter to `/tmp` and run it as `whatsapp-archiver` against that copy. PASS only if `with_message_secret=0` in all four tables. Then delete the copy and the tool, and confirm both are gone.
7. **Nightly snapshot and media copy.**
   - Find the newest folder in `/mnt/data/backups/whatsapp-archiver/` whose name is a 00:30 UTC run (`T0030`). Check that `sqlite3 -readonly <snapshot>/archive.db "PRAGMA integrity_check"` prints `ok`, and run the secret counter on it as in check 6.
   - Check the media copy with `systemctl show archiver-media-backup.service -p Result -p ExecMainStatus -p ExecMainExitTimestamp`: the last run is today at about 04:30 with status 0. Also check that `sudo -n du -sh /mnt/data/backups/whatsapp-archiver-media /var/lib/whatsapp-archiver/media` shows sizes within 10% of each other.

   PASS only if all of these hold.
8. **History status.** Run `sudo -n -u whatsapp-archiver /opt/whatsapp-archiver/archiver status --data /var/lib/whatsapp-archiver` and quote the `history:` line. Hamza signed off that it reads `in progress (99%)` (FULL chunk 47 never arrived). PASS if it reads that, or `in progress` at a higher percentage, or `complete`. Otherwise FAIL.
9. **Rollbacks and cleanup.** With `sudo -n ls -la`, confirm three things. `/opt/whatsapp-archiver/archiver.prev` exists with the SHA-256 above. `archive.db.pre-scrub*` and `/opt/whatsapp-archiver/archiver.before-19oct` are gone. `/srv/backups/whatsapp-archiver/` holds only `20261019T003000…`.
10. **Phase 0 acceptance.** Read `docs/phase0-acceptance.md` and the Task 8 line in `PROGRESS.md`. List every acceptance step that is not done, and label each one "skipped by Hamza (20 Oct)" if the Task 8 line covers it, or "open" otherwise. Do not run any of them.

## Report

Write `docs/reviews/2026-10-20-phase1-readiness-recheck.md`:

- A table with one row per check: PASS, FAIL or BLOCKED, and the evidence line.
- **Verdict.** It is one of three lines:
  - "READY FOR PHASE 1" if checks 1 to 9 all PASS and check 10 lists nothing.
  - "READY FOR PHASE 1, WITH WAIVED ACCEPTANCE STEPS" if checks 1 to 9 all PASS and every step check 10 lists is "skipped by Hamza (20 Oct)". Name the waived steps right under the verdict, and say plainly that the charter's phase 0 exit criterion (spikes 1 to 5 recorded, oldest message checked per group, a 7-day audit with zero missing) is not met for those steps.
  - "NOT READY" otherwise, with each reason listed.

  A FAIL or BLOCKED check is never a reason to change the live system.
- **Remaining decisions for Hamza.** Include all of these:
  - the 5 visible messages that were never recovered (Task 6);
  - whether 403 and bad-SHA history downloads count as "gone" under D4, and what happens to a history chunk that can never be stored;
  - whether to store a source message ID on event rows so recovered event gaps can close (a schema change);
  - recording the phase order change (phase 2 before phase 1, anytopdf as a candidate intelligence layer) in the charter's decision log;
  - deleting the old media copy `/srv/backups/whatsapp-archiver-media` once the `/mnt/data` copy has run for a week.
- **Waived or open acceptance steps** from check 10.

Finish by printing the verdict line and the report path. Do not ask questions.
