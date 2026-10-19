# Phase 1 readiness check (run unattended at 03:40, Europe/Dubai time, UTC+3)

You are checking whether the WhatsApp Archiver is ready for phase 1. Nobody will answer questions while you run, so decide nothing that the rules below do not already cover. Produce one report and stop.

## Context

- Local repo: `/Users/hamza/Code/whatsapp-archiver/archiver`. Branch `fix/recovered-after-restart`, which is also `main` and `origin/main` at commit `1d725dd`. Read `AGENTS.md` and the section "Open before phase 1" at the end of `PROGRESS.md` first.
- Server: `hamza@ryzen.local` (x86_64, SSH key login, `sudo -n` works for the commands below). Service `archiver.service`. Data folder `/var/lib/whatsapp-archiver` (mode 0700, owned by `whatsapp-archiver`). Binary `/opt/whatsapp-archiver/archiver`, expected SHA-256 `adc871fec73d12a556c7c6a9947e8c9a9ac394f1e956984cc0400309b5b72831`.
- Restart at 01:29:43 local time. Baseline at 01:39: `messages` count 227884.
- Rollbacks from the 19 Oct maintenance window. Keep them; do not delete them: `/var/lib/whatsapp-archiver/archive.db.pre-scrub` (with its `-wal.pre-scrub` and `-shm.pre-scrub`) and `/opt/whatsapp-archiver/archiver.before-19oct`.

## Hard rules

- Never run `archiver pair` or `archiver run`, and never restart, stop, reconfigure or redeploy the service. If something looks broken, record it as a blocker and stop there.
- Never read message text. Use counts and aggregates only (`COUNT`, `MAX`, `GROUP BY`). Do not print rows from `messages`, `message_events` or `media`.
- Never connect to WhatsApp, and never send anything to anyone.
- Do not delete, move or overwrite any file on the server. Do not change `config.yaml`.
- Do not commit, push, merge or rebase. Do not use `git stash`.
- Server writes are limited to three things, and each is deleted or written as stated: the counter tool in `/tmp` (deleted after use), a temporary copy `/var/lib/whatsapp-archiver/readiness-check.db` (deleted after use), and nothing else.
- If a check cannot be completed, say so in the report. Do not guess a result.

## Checks

Record each check as PASS, FAIL or BLOCKED, with the evidence line.

1. **Local repo.** `git status --short` is empty; `main` and `fix/recovered-after-restart` point at the same commit; `git ls-remote origin main` shows that commit. `wc -l PROGRESS.md` is under 500.
2. **Local gate.** `make check` exits 0, with the last lines from the log. Keep the log in `.cache/readiness-check.log`.
3. **Server service.** `systemctl is-active archiver` prints `active`. `sha256sum /opt/whatsapp-archiver/archiver` matches the expected SHA-256 above. `sudo -n cat /var/lib/whatsapp-archiver/health.json` shows `"state": "connected"` and `"panics": 0`.
4. **Log since restart.** `sudo -n journalctl -u archiver --since "2026-10-19 01:29:40" --no-pager` has no `"level":"ERROR"` or `"level":"WARN"` lines. The lines from before 01:29:40 are from the old process and are expected.
5. **Traffic.** Count messages now with `sudo -n -u whatsapp-archiver sqlite3 -readonly /var/lib/whatsapp-archiver/archive.db "SELECT COUNT(*) FROM messages"`. PASS only if the count is higher than 227884. If it is still 227884 at 03:40, that is a BLOCKER: messages have not been captured since the restart. Report it and do not try to fix it.
6. **Secrets.** Build the counter locally: `GOOS=linux GOARCH=amd64 .tools/go/bin/go build -o .cache/build/secretcount-linux-amd64 ./.cache/secretcount`. Make a consistent copy of the live archive with `sudo -n -u whatsapp-archiver sqlite3 /var/lib/whatsapp-archiver/archive.db ".backup /var/lib/whatsapp-archiver/readiness-check.db"`. Copy the counter to `/tmp` and run it as `whatsapp-archiver` against that copy. PASS only if `with_message_secret=0` in all four tables. Then delete the copy and the tool, and confirm both are gone.
7. **Nightly snapshot.** The backup runs at 03:30 local time, into `/srv/backups/whatsapp-archiver/<UTC timestamp>/`. Find the newest folder created after 00:30 UTC today with `sudo -n ls /srv/backups/whatsapp-archiver/`. Check that it exists and that `sqlite3 -readonly <snapshot>/archive.db "PRAGMA integrity_check"` prints `ok`. Run the secret counter on it as in check 6. PASS only if the snapshot is found, passes integrity, and has zero secrets. If no snapshot exists yet, mark it BLOCKED (backup did not run).
8. **History status.** Run `sudo -n -u whatsapp-archiver /opt/whatsapp-archiver/archiver status --data /var/lib/whatsapp-archiver` and quote the `history:` line. Do not change anything based on it.
9. **Rollbacks present.** Confirm the files listed under Context exist, with `sudo -n ls -la`.
10. **Phase 0 acceptance.** Read `docs/phase0-acceptance.md`. List every step that still needs Hamza on the phone or the server (the steps that are not done, and what each needs). Do not run any of them.

## Report

Write `docs/reviews/2026-10-20-phase1-readiness-report.md`:

- A table with one row per check: PASS, FAIL or BLOCKED, and the evidence line.
- **Verdict.** Use "READY FOR PHASE 1" only if checks 1 to 9 all PASS and the step list in check 10 is empty. Otherwise write "NOT READY" and list each reason. A FAIL or BLOCKED check is never a reason to change the live system.
- **Remaining decisions for Hamza.** Include all of these: the history-import loss on shutdown, which is not fixed; the history "complete" rule, which counts RECENT chunks and should be tightened later; and deleting `archive.db.pre-scrub` and the 18 Oct snapshot once the 03:30 snapshot is confirmed clean.
- **Remaining phone and server steps** from check 10.

Finish by printing the verdict line and the report path. Do not ask questions.
