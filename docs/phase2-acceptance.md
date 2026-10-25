# Phase 2 manual acceptance: the control dashboard

Run by Hamza on the server (ryzen, `192.168.0.105`) after the phase 2 build is deployed. It is not run by agent sessions: it uses the live WhatsApp login. Fill every `Result` line. Phase 2 is finished when every step below passes. Deploying needs Hamza's yes; the steps are in `README.md` section 10 (Updating). The dashboard and its screens are described in the README's Dashboard section.

Commands that only read (`status`, `chats`, the SQL queries, `journalctl`) never connect to WhatsApp. Never run `archiver run` or `archiver pair` in a terminal against `/var/lib/whatsapp-archiver` while the service is running.

## Exit criteria covered

| Step | Criterion |
| --- | --- |
| 1 | Capture runs uninterrupted through the switch |
| 2 | SC-5: a new message appears in the activity feed within 2 seconds |
| 3 | SC-4: pairing, and turning logging on for a chat, each under 1 minute with no file editing |
| 4 | SC-2: a chat excluded in the dashboard stores nothing a week later |
| 5 | Extra: the dashboard is reachable only through the tunnel |

## Queries used below

Run them on the server **as the service user, read-only** (sqlite3 as root can leave root-owned files the service cannot open):

```sh
sudo -u whatsapp-archiver sqlite3 -readonly -header -column /var/lib/whatsapp-archiver/archive.db
```

Times are Unix seconds; `datetime(x,'unixepoch')` shows UTC. Leave sqlite3 with `.quit`.

## Steps

### 1. Capture runs through the switch

Before deploying, note the state of the live archive so that the comparison below means something.

- [x] Before the update: `sudo -u whatsapp-archiver /opt/whatsapp-archiver/archiver status --data /var/lib/whatsapp-archiver | head -4` shows `state: connected`. Then, in sqlite3:

  ```sql
  SELECT COUNT(*) AS messages, MAX(received_at) AS newest, datetime(MAX(received_at),'unixepoch') AS newest_utc FROM messages;
  SELECT version FROM schema_version;
  SELECT COUNT(*) AS settings_rows FROM sqlite_master WHERE name IN ('settings','chat_rules','chat_seen');
  ```

  Expected: a message count near 228,000 and a recent `newest`; `schema_version` lists `1` only; `settings_rows` is `0` (the new tables do not exist yet). Write the count and `newest` down. Messages 228364, newest 1792506500 (2026-10-20 14:28:20 UTC). Result: **pass** (20 Oct, state connected, schema_version 1, settings_rows 0).
- [x] Deploy as in README section 10 (build, `scp`, `sudo systemctl stop archiver`, install, `sudo systemctl start archiver`). Write down the start time: 2026-10-20 17:38:40 +03 (unix 1792507120), build `fe1629d`, sha256 `e6dfd02d…a710`; old binary kept as `archiver.prev`.
- [x] Start-up lines. As soon as the service is up:

  ```sh
  journalctl -u archiver --since "5 minutes ago" --no-pager | grep -E 'settings imported|dashboard listening|ignored|ERROR'
  ```

  Expected: one line `"msg":"config.yaml settings imported into the archive","exclusions":N` where `N` is the number of entries of `exclude:` in `config.yaml`, and one `"msg":"dashboard listening","listen":"127.0.0.1:8080"`. No `"level":"ERROR"` line. No `config.yaml exclude is ignored` line on this first start, because every entry was just imported. The migration itself writes no log line; it is seen in the database in the next item. Result: **pass.** `exclusions":0` (config `exclude: []`) at 17:38:40, `dashboard listening` `127.0.0.1:8080` at 17:38:41, authenticated 17:38:42. No ERROR from the new process; the only ERROR lines are the old process's known media-queue shutdown noise at 17:38:40.229 (`context canceled`).
- [x] The migration and the import are in the archive. In sqlite3:

  ```sql
  SELECT version FROM schema_version;
  SELECT key FROM settings ORDER BY key;
  SELECT COUNT(*) AS excluded FROM chat_rules WHERE logging = 0;
  ```

  Expected: `schema_version` lists `1` and `2`; `settings` lists `alerts.thresholds`, `backup.schedule`, `import.config.v1` and `media.rules`; `excluded` equals the number of `exclude:` entries (more if the same chat was listed under both its phone and LID forms). Result: **pass.** schema_version 1,2; settings `alerts.thresholds, backup.schedule, import.config.v1, media.rules`; excluded 0; chat_seen 399 rows (existing chats marked seen).
- [x] Capture is running again at once: `status` shows `state: connected` within a minute. Result: **pass** (connected, client 2.3000.1050977907, 0 restarts).
- [x] Ten minutes after the start (ask someone to send a message to a logged chat if it is quiet), in sqlite3, with `<start>` the Unix time of the start (`date -d '<start time>' +%s` on the server, or take `newest` of the previous check):

  ```sql
  SELECT COUNT(*) AS since_start, datetime(MAX(received_at),'unixepoch') AS newest_utc
  FROM messages WHERE source = 'live' AND received_at > <start>;
  SELECT COUNT(*) FROM open_gaps;
  ```

  Expected: `since_start` above 0 and `newest_utc` within the last few minutes (live messages are arriving); `open_gaps` is 0 or falls to 0 by itself; the message count is higher than before the update and no lower. Result: **pass** (17:51): since_start 1, newest_utc 2026-10-20 14:48:57; total 228365 (was 228364); open_gaps 10 (unchanged); active, NRestarts 0; no WARN/ERROR and no connection drops since 17:38:41.

### 2. SC-5: a new message appears in the feed within 2 seconds

- [ ] On the Mac, open the tunnel and keep it open: `ssh -L 8080:localhost:8080 hamza@192.168.0.105`. Open <http://localhost:8080>. The Dashboard shows `Connected`, and the Activity box lists recent events.
- [ ] Leave the Dashboard open in the foreground. From a second phone (or have someone) send a message to a chat that is **logging**, and time it with the stopwatch from sending to the line appearing in the Activity box. Expected: a new line for that chat within 2 seconds, without pressing reload. The line names the kind and the chat but never the message text. Seconds: ____. Result: ____
- [ ] Close the laptop lid or drop the tunnel for a minute, then bring it back. Expected: capture is not affected (`journalctl -u archiver -n 5 --no-pager` shows no error and `status` stays `connected`) and the page's feed resumes after it reconnects. Result: ____

### 3. SC-4: pairing and enabling a chat, each under a minute

Pairing the live archiver again would unlink it, so time pairing on a practice data folder with its own login. It is a second linked device on the phone, removed again at the end. This is the one place a second `run` is allowed: it uses a different data folder, a different login and another port.

Note (26 Oct 2026): the practice instance is already stopped and unlinked, so this step is not to be repeated; the folder is `/var/lib/whatsapp-archiver-practice` (permission-restricted).

- [ ] Prepare the practice folder (this is not part of the timing):

  ```sh
  sudo -u whatsapp-archiver sh -c 'mkdir -p -m 700 /var/lib/whatsapp-archiver-practice && printf "web: {listen: \"127.0.0.1:8081\"}\n" > /var/lib/whatsapp-archiver-practice/config.yaml'
  ```

  On the Mac open a second tunnel: `ssh -L 8081:localhost:8081 hamza@192.168.0.105`.
- [ ] Start it in the foreground: `sudo -u whatsapp-archiver /opt/whatsapp-archiver/archiver run --data /var/lib/whatsapp-archiver-practice`. Expected: `not paired: open the dashboard through the SSH tunnel and pair …`.
- [ ] Start the stopwatch. In the browser open <http://localhost:8081/pair>, press **Pair WhatsApp**, scan the QR code on the iPhone (WhatsApp, Settings, Linked Devices, Link a Device). Stop the stopwatch when the page says `Paired. Capture is running.` No file was edited and no command was typed after the start. Pairing time: ____ s (limit 60). Result: ____
- [ ] Press Ctrl-C in the terminal. Unlink the practice device on the phone (Settings, Linked Devices, the new device, Log out), then `sudo rm -r /var/lib/whatsapp-archiver-practice`. The live archiver stays linked: `status --data /var/lib/whatsapp-archiver | head -2` still says `connected`. Result: ____
- [ ] Turning logging on for a chat. On the live dashboard, Chats, pick a chat that shows **Not logging** (or press **Stop logging** on a test chat first). Start the stopwatch, press **Start logging**, and stop it when the row shows **Logging**. No file was edited and nothing was restarted. Seconds: ____ (limit 60). Then send a message to that chat from another phone: it appears in the Dashboard feed, and in sqlite3 `SELECT COUNT(*) FROM messages WHERE chat_jid = '<its address>' AND received_at > <the time of the click>;` is at least 1 (use `archiver chats` for the address). Result: ____

### 4. SC-2: an excluded chat stores nothing

Needs a week; start it right after step 3 and write the switch time down.

- [ ] In the dashboard, Chats, press **Stop logging** on a chat that is active (a group or a direct chat with regular messages). The row shows **Not logging**. Chat: ____ Address (from `archiver chats`): ____ Time of the click: ____
- [ ] At once, send a message to that chat from another phone. Expected: nothing for it appears in the Activity feed. Result: ____
- [ ] One week later, in sqlite3. First name the chat (the address as it appears in `archiver chats`, with its quotes inside the double quotes):

  ```
  .parameter set :chat "'120363…@g.us'"
  ```

  Then the switch time (the rule may be stored under another form of the address, so the query looks at every alias) and the count:

  ```sql
  WITH aliases(j) AS (
    SELECT :chat
    UNION SELECT lid FROM id_mappings WHERE pn = :chat
    UNION SELECT pn FROM id_mappings WHERE lid = :chat
    UNION SELECT pn FROM id_mappings WHERE lid IN (SELECT lid FROM id_mappings WHERE pn = :chat)
  )
  SELECT jid, logging, datetime(updated_at,'unixepoch') AS switched_utc FROM chat_rules WHERE jid IN (SELECT j FROM aliases);

  WITH aliases(j) AS (
    SELECT :chat
    UNION SELECT lid FROM id_mappings WHERE pn = :chat
    UNION SELECT pn FROM id_mappings WHERE lid = :chat
    UNION SELECT pn FROM id_mappings WHERE lid IN (SELECT lid FROM id_mappings WHERE pn = :chat)
  )
  SELECT COUNT(*) AS stored_after_switch FROM messages
  WHERE chat_jid IN (SELECT j FROM aliases)
    AND received_at > (SELECT MAX(updated_at) FROM chat_rules WHERE jid IN (SELECT j FROM aliases) AND logging = 0);
  ```

  Expected: the first query shows `logging` 0 and the time of the click; `stored_after_switch` is **0**. Do not turn logging back on during the week, or the count means nothing. Result: ____ (a non-zero count fails SC-2: note the newest `received_at` and `source` of those rows and raise it before phase 3).
- [ ] If the chat was seen under both its phone-number and LID forms, the count above already covers both. Turn logging back on afterwards if the chat should be archived again. Result: ____

### 5. Extra: only the tunnel reaches the dashboard

- [ ] On the server: `ss -ltn | grep 8080`. Expected: the local address is `127.0.0.1:8080` (not `0.0.0.0:8080` or `*:8080`). Result: ____
- [ ] On the server: `curl -s -o /dev/null -w '%{http_code}\n' -H 'Host: example.com' http://127.0.0.1:8080/` prints `403`, and `curl -s -o /dev/null -w '%{http_code}\n' http://localhost:8080/` prints `200`. Result: ____
- [ ] From the Mac, without the tunnel, `curl -m 5 http://192.168.0.105:8080/` fails to connect. Result: ____

## Sign-off

- [ ] Every result above is filled in and passes. Date: ____ Signed: ____
