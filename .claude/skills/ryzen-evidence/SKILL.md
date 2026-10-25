---
name: ryzen-evidence
description: Gather read-only evidence from the ryzen server (journal, settings, media states, perf) for the week report appendix.
---

Server `hamza@192.168.0.105`. Main data `/var/lib/whatsapp-archiver`, practice `/var/lib/whatsapp-archiver-practice`. Source: "Setting" in `docs/reviews/2026-10-26-week-review-issues.md`.

Rules:
- Never run `archiver run` or `archiver pair` against a live data folder.
- Never SELECT message text, body, caption, push names or people's JIDs. Output counts, timings and states only: aggregate queries, trimmed to counts.
- All sqlite goes through `sudo -n -u whatsapp-archiver sqlite3 -readonly <dir>/archive.db "..."`.
- Change nothing on the server. Paste the output into an appendix of the week report, with the date and command.

Set `D=/var/lib/whatsapp-archiver` (or the practice dir) and prefix each command with `ssh hamza@192.168.0.105`.

1. Status: `sudo -n -u whatsapp-archiver /opt/whatsapp-archiver/archiver status --data $D | head -20`
2. Is ntfy configured (no URL shown)? `sudo -n -u whatsapp-archiver grep -c '^ *ntfy' $D/config.yaml` (verify key name against `internal/config`). The settings screen only shows "configured".
3. Alert failures per day: `journalctl -u archiver --since "2026-10-20 18:07" -o cat | grep -E 'alert not sent|alert dropped' | grep -o '"time":"[0-9-]*' | sort | uniq -c` (verify the `time` field layout; fall back to `grep -c`). Log levels: ERROR per day likewise with `"level":"ERROR"`.
4. Live thresholds: `sqlite3 -readonly $D/archive.db "SELECT key, value, updated_at FROM settings WHERE key='alerts.thresholds'"` (settings: key, value, updated_at; migrations/0002_settings.sql).
5. Media states: `SELECT state, COUNT(*) FROM media GROUP BY state ORDER BY 2 DESC` (media columns: state, reason, attempts, source, type, next_attempt_at, message_ts; migrations/0001_raw.sql).
6. Full failure reasons, counts only: `SELECT reason, COUNT(*) FROM media WHERE state='failed' GROUP BY reason ORDER BY 2 DESC LIMIT 30`. HTTP status distribution: `SELECT substr(reason, instr(reason,'status')) AS s, COUNT(*) FROM media WHERE state='failed' GROUP BY s` (verify the reason text format first with query 6).
7. Practice instance: run 1, 5 and `SELECT COUNT(*) FROM media WHERE state='expired'` with `D=/var/lib/whatsapp-archiver-practice`. It is not under systemd, so it has no journal.
8. CPU profile of a PID (`pgrep -f 'archiver run'`; practice PID was 288900 on 26 Oct): `sudo perf top -p <pid>` for 30 s, or `sudo perf record -p <pid> -g -- sleep 30 && sudo perf report --stdio | head -40`. Go pprof is not exposed. `perf` may need `sudo` that is not in the limited sudoers file: ask Hamza before using it.
