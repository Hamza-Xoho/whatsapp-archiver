# Week review and issue report, 20 to 26 Oct 2026

Written for a separate code session. Evidence was gathered read-only over SSH on 26 Oct 2026 at about 01:40 +03. Nothing in the archive, the services or the code was changed, except the network settings in item 1 and the address edits in the docs.

## Setting

- Server: ryzen, Ubuntu, `hamza@192.168.0.105` (was `.104` until 25 Oct; a router reservation is still to be set by Hamza). Wired `enp34s0` and Wi-Fi `wlo1` (`192.168.0.112`) are both up, same router.
- Main instance: `archiver.service`, `/opt/whatsapp-archiver/archiver run --data /var/lib/whatsapp-archiver`, build `a87a30f`, dashboard on `127.0.0.1:8080`, running since 20 Oct 18:07:33 with 0 restarts.
- Practice instance: started by hand in a terminal (`pts/5`, PID 288900) as `sudo -u whatsapp-archiver archiver run --data /var/lib/whatsapp-archiver-practice`, dashboard on `127.0.0.1:8081`, running since 20 Oct, paired as a second linked device. Not under systemd, so its log lines go to that terminal only.
- Journal command used: `journalctl -u archiver --since "2026-10-20 18:07" -o cat` (JSON lines; fields `time`, `level`, `msg`, `err`).
- Read-only database access: `sudo -n -u whatsapp-archiver sqlite3 -readonly ...`. Status: `sudo -n -u whatsapp-archiver /opt/whatsapp-archiver/archiver status --data <dir>`. Never run `archiver run` or `archiver pair` against the live data folder.

## Health summary

| Item | Main | Practice |
|---|---|---|
| State | connected, client `2.3000.1050977907` (latest) | connected |
| Messages | 230,927 (live every day since 17 Oct, 168 to 1,036 per day) | 24,029 (2,560 live) |
| Open decryption gaps | 22 (10 on 20 Oct) | 0 |
| History | in progress 99%, 57 chunks, last chunk 16 Oct 19:12 | complete back to 2020-08-28, 8 chunks |
| Disk | 3.4 GB data; 396 GB free on `/`, 648 GB free on `/mnt/data` | 39 MB |

Journal WARN or ERROR lines since 20 Oct 18:07: 192 WARN and 216 ERROR. Per day: 20 Oct 10, 21 Oct 32, 22 Oct 14, 23 Oct 14, 24 Oct 27, 25 Oct 271, 26 Oct (to 01:38) 40.

## Issues

Severity: High = data loss or silent failure risk; Medium = wrong or wasteful behaviour; Low = tidy-up.

### 1. [High] A 7h20m capture outage raised no alert

Timeline (all +03):

- 25 Oct 16:53 NetworkManager gave the wired port `192.168.0.104` (new lease). 16:15 the Wi-Fi had `.112`.
- 17:09 the Wi-Fi restarted its DHCP transaction and its supplicant went disconnected, scanning, authenticating.
- 17:36 to 18:03 six `connection lost` / `connection restored` pairs (2 to 14 s).
- 17:12:53 first `Error reconnecting after autoreconnect sleep: failed to dial whatsapp web websocket: ... lookup web.whatsapp.com on 127.0.0.53:53: server misbehaving`. 181 such errors until 26 Oct 01:26:19.
- 26 Oct 01:19 to 01:20: 17 `media re-upload request failed ... websocket not connected` WARNs.
- 26 Oct 01:28 systemd-resolved still listed the stale DNS server `192.168.68.1` (a different subnet, probably from one of the `stc_wifi_*` profiles). 01:31:39 `Successfully authenticated`; capture resumed.
- Over that time no `connection lost` / `connection restored` pair describes the long outage, because the existing log lines only mark transitions the client sees; the `autoreconnect` ERROR loop is the only signal. No ntfy alert was seen by Hamza.

Expected behaviour (charter, D3): a `Disconnected for` alert after the configured threshold. It did not reach Hamza. Not yet established: whether ntfy is configured on ryzen, what `alerts.thresholds` holds, and whether the monitor treats the autoreconnect loop as `disconnected`.

To fix or check:
- Read `alerts.thresholds` and `ntfy` config on ryzen (the settings screen shows "configured" only). Find out why no alert fired.
- Decide the state name and alert for "reconnect failing for more than N minutes" (suggest 5 minutes), and make sure it fires even when DNS is the failure.
- Add a regression test around `health.Monitor.SetState` or the autoreconnect error path.
- Consider a log line when the process sees N consecutive `autoreconnect` errors (the logs show 181 identical ERROR lines but no summary).

### 2. [High] 12 new open decryption gaps in six days

`open_gaps` went from 10 (20 Oct, step 1 check) to 22. Rows (day of event, chat, count): 16 Oct `68311891067085@lid` 1 and `966548458786-1568644102@g.us` 1; 17 Oct `120363423232501153@g.us` 1; 18 Oct `105424367910940@lid` 2; 19 Oct `120363410844544027@g.us` 1, `120363420515642808@g.us` 2, `121208892870694@lid` 1, `966548458786-1568644102@g.us` 1; 21 Oct `120363420515642808@g.us` 1; 22 Oct `120363423232501153@g.us` 1; 23 Oct `68311891067085@lid` 1; 24 Oct `121208892870694@lid` 1 and `68311891067085@lid` 3; 25 Oct `120363039658967468@g.us` 1, `120363422546317088@g.us` 1, `120363423232501153@g.us` 2, `966583170435-1613673023@g.us` 1.

Journal causes seen: `Error decrypting message ... failed to decrypt group message: no sender key`, `Got untrusted identity error while trying to decrypt prekey message ... clearing stored identity and retrying` (7), and `Unavailable message ... (type: "view_once")`.

To do: check which gaps are real lost visible messages and which are events, using the method in `docs/phase0-acceptance.md` step 12. Check if the gaps from 25 Oct come from the outage backfill (the first messages after reconnecting at 01:31 logged `no sender key` and `untrusted identity` within 11 s). Decision D6 says no gap re-request sweep; this report only asks for the count and cause to be recorded.

### 3. [Medium] 25,268 media rows are `failed`, all of them history

`media` states: done 1,892; failed 25,268; expired 405; skipped 5,346; pending 90; downloading 2; retry_requested 5.

Failed rows by source and type: history image 11,984, history audio 10,256, history video 2,480, history document 566. Live media failures since 20 Oct: 0.

Reasons (truncated in my query to 60 characters): 24,465 `after 5 attempts: download media: download failed with statu...` (the status code is cut off; likely 404 or 410), 803 `... failed to refresh media co...`, 18 `... no url present`. By message month: Mar 2026 2,489, Apr 1,931, May 974, Jun 669, Jul 1,302, Aug 1,904, Sep 717, Oct 6.

To do: print the full `reason` text and the HTTP status distribution; decide whether `failed` history media is expected (CDN blobs gone, D4) and should be moved to `expired`, or whether the retry-request path (`retry_requested`, `next_attempt_at`) should run for them; check readiness check 4 and the Storage page numbers, which will look odd with 25k failed rows. Also 90 `pending` and 2 `downloading` rows are still open; confirm they are being worked.

### 4. [Medium] History sync stuck at 99% on the main instance

`history: in progress (99%), chunks 57, last chunk 2026-10-16 19:12 +03`. The practice instance, paired 20 Oct, reached `complete back to 2020-08-28` with 8 chunks. The main one has not received a new chunk for 10 days, and under ADR-31 it is not complete until the newest FULL sync reaches 100. `PROGRESS.md` already lists the rule limitation: if the lowest-order chunk of the newest sync never reaches the ledger (acked as gone under D4), the status stays on the old sync.

To do: inspect the `history_chunks` ledger for the main archive, and decide whether this is the limitation above, a lost chunk, or normal (the phone may not send more). Also check D4 and the media in item 3, since the same blobs may be gone.

### 5. [Medium] Practice instance: heavy CPU, unmanaged, duplicate of the account

PID 288900: elapsed 5d 7h, CPU time 23h 25min, average 18.3% CPU. The main instance averages 0.3% (29 CPU-minutes in the same period). A mostly idle capture client should not average 18%. Cause unknown; possible candidates: a busy loop after history or pairing completion, the media queue, or the web server's SSE loop (the practice dashboard has no client).

It also runs under `sudo -u` in a terminal, so its logs are not in `journalctl -u archiver` and nobody is alerted if it fails. It duplicates the account (24,029 messages) and is a second linked device, which the phone shows as "Last active Tue at 6:13pm" (most likely).

To do: profile it (Go pprof is not exposed; use `perf top -p 288900` or `/proc/288900/task/*/stat`), and if the cause is a bug, add a test. Decide whether to keep it for SC-4 or stop it and unlink it. Hamza said to keep it running for now.

### 6. [Medium] The practice data folder differs from the acceptance doc

`docs/phase2-acceptance.md` step 3 says `/var/tmp/practice`; the real folder is `/var/lib/whatsapp-archiver-practice` (permission-restricted: `ls` as `hamza` fails). The checklist's cleanup command (`sudo rm -r /var/tmp/practice`) would not remove it.

### 7. [Medium] Network configuration on ryzen was fragile (partly fixed)

Two interfaces on the same subnet with DHCP, four autoconnect profiles (`netplan-enp34s0`, `SaudiSpyWithMyWifi`, `stc_wifi_2364`, `stc_wifi_2364_5G`; the two `stc_wifi` ones are on another subnet or router), no reservation, and the wired port briefly fell back to a link-local `169.254.237.217` when Hamza looked at it (`systemd-networkd` is not running; NetworkManager is the renderer).

Done on 26 Oct (via `nmcli`, persisted by NetworkManager into `/etc/netplan/90-NM-*.yaml`): wired `ipv4.route-metric 100`, wired DNS `192.168.0.1, 1.1.1.1, 8.8.8.8`, Wi-Fi profiles `ipv4.route-metric 600`. Previous profile dumps are in `~hamza/netbackup/` on ryzen. A reboot has not been tested.

Left to Hamza: a router DHCP reservation for MAC `2c:f0:5d:3b:67:65`. Left open: whether to delete the unused `stc_wifi_*` profiles, and the stale DNS source `192.168.68.1`. `/etc/netplan/00-installer-config.yaml` still has `dhcp4: true` for the wired port; `00-installer-config.yaml.bak-before-dhcp` is the old backup.

### 8. [Medium] Network WARN/ERROR volume breaks the D3 bound on some days

D3 allows 10 or fewer WARN or ERROR lines per 24 hours with recovery within 2 minutes. Over 24 hours the counts were 32 on 21 Oct and 27 on 24 Oct, and 271 on 25 Oct. Breakdown of the total (since 20 Oct 18:07): `Error reconnecting after autoreconnect sleep` 181 ERROR, `Keepalive timed out` 59 WARN, media download network errors 52 + 10 + 3 + 3 + 2 WARN, `Error reading from websocket` 29 ERROR, `media re-upload request failed` 17 WARN, `Got untrusted identity` 7 WARN, `Error sending close to websocket` 7 WARN, `Received stream end frame` 6 and `Got 503 stream error` 6 WARN, and 3 shutdown ERRORs from 20 Oct (`media row not saved`, `stale re-upload requests not failed`, both `context canceled`).

To do: re-read D3 in the charter and the readiness check; decide whether to keep a numeric bound or to measure outage time instead. About 25 short disconnects in 5 days, each 1 to 2 s, is the normal profile; there is nothing to fix there, but a daily count alone does not separate that from the 25 Oct outage. The known shutdown ERRORs trip readiness check 4 at every restart (also listed in `PROGRESS.md` Task 7).

### 9. [Medium] Phase 2 acceptance mostly open

Only step 1 passed. Open: step 2 (SC-5, feed within 2 seconds), step 3 (SC-4, pairing and enabling a chat under 60 s each; the practice instance exists but pairing time was not recorded), step 4 (SC-2, a week with an excluded chat; no exclusion has been set: `exclusions: 0`), step 5 (tunnel-only). The README and checklist tunnel commands now use `192.168.0.105`.

### 10. [Low] Old items still open from the earlier docs

- 5 permanently missing visible messages (Task 6 / D6) and the schema change that closing `…6F66` needs.
- Waived phase 0 acceptance steps: spikes 1, 2, 4, 5, the 7-day SC-1 and SC-10 audit, and history redelivery after a restart (Task 2) are unproven.
- Blank group messages before the 20 Oct 18:07 fix (about 50% of live group messages in the 24 hours before) are not recovered.
- Linked devices on the phone: one entry says "Last active 27 Sep 2026 ... Open WhatsApp on this device to stay logged in". This is probably the main archiver's pairing, but it is unconfirmed. Find out what the warning means for a linked device and whether it can lead to a logout.
- The media copy timer (`archiver-media-backup.timer`) runs nightly 04:30; last run 25 Oct 04:30 exited 0. The snapshot directory `/mnt/data/backups/whatsapp-archiver` is not readable by `hamza` (permission denied), so snapshot age and integrity were not re-checked this time.

## What was not checked

- Whether messages sent during the 25 Oct outage were all backfilled. Message timestamps in the outage hours exist (for example 112 in the 21:00 hour, 4 to 22 per hour either side), which suggests WhatsApp's queue delivered them, but no per-chat comparison was done.
- Snapshot integrity since 20 Oct.
- The CPU cause for the practice instance.
- Anything about the Mac's own Wi-Fi.

## Suggested order for the code session

1. Item 1 (outage alert), with tests.
2. Items 3 and 4 (media failed rows, history 99%), read-only first.
3. Item 2 (new gaps), record the cause.
4. Item 5 (practice CPU), then decide what happens to the practice instance.
5. Items 6 and 9 (docs path fix, acceptance steps), then item 10.
