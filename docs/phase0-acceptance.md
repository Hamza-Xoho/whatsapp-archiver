# Phase 0 manual acceptance — iPhone

Task 12 of the plan is run by Hamza on the server; it is intentionally not run by Codex or any agent session. Fill every `Result` line before phase 1. Phase 0 is finished when every step is recorded, spikes 1 to 5 have results, any decision a spike forced is written into the charter's decision log, and the 7-day audit passes.

Set-up commands are in the Setup guide in `README.md` (section numbers below refer to it). Everything that talks to WhatsApp (`pair`, the service) is run only in these steps and only by Hamza. `status`, `chats`, `backup`, `replay`, `scrub` and the SQL queries never connect to WhatsApp.

## Spikes

| # | Spike | Steps | What is measured | Result decides |
| --- | --- | --- | --- | --- |
| 1 | History reach | 2, 9 | Pair with full sync (3,650 days, 4,096 MB); keep running 48 hours. Per BMC group and per chat type: oldest message, time until progress reached 100%, number of chunks, chunks that arrived after restarts. | If the course groups reach the start of term, drop backup import from the charter's later list. |
| 2 | iOS vs Android | 9 | Compare spike 1 with the Android figures in the research: 90 days on an ordinary pairing, about 22 months on full sync, 3 years with a raised size limit. | If iOS stops much sooner, raise backup import in the charter's later list. |
| 3 | Version-drift drill | 5 | Force the retired version `2.3000.1040847988` with the version check off, then turn the check back on. | If the alert, the `client_outdated` state or the recovery fails, fix it before real use. |
| 4 | Receipt audit | 4 | Nothing the archiver does is visible to anyone: no "online", no read receipts, no extra delivery ticks, over 24 hours. | Any visible receipt halts phase 1 until explained. |
| 5 | LID dedup | 8 | After the re-pair, count `(canonical chat, message id)` pairs that appear more than once in raw `messages` and in `messages_resolved`. | A duplicate in the view means the dedup key changes before phase 1. |

Decision forced by any spike: ____

## Receipt-audit notes: protocol traffic that is accepted

whatsmeow sends this by itself on the archiver's behalf. None of it is a message, reaction, edit, read receipt, presence or typing, and none should show in any contact's app (CON-1, ADR-15). Spike 4 checks that. Line numbers are in the pinned `go.mau.fi/whatsmeow@v0.0.0-20261013142727-4cf8bc8e0aeb`.

- **`SetPassive(false)`**: an IQ sent to WhatsApp's server **on every connect and reconnect** (`connectionevents.go:200`; defined at `:213`). It tells the server this device is active rather than passive. It is not shown to contacts, but it is the one call that changes how the server treats the device, so if "online" ever appears for Hamza's account while his phone is offline, this is the first thing to consider.
- A delivery `receipt` for every message received, marked `type="inactive"` because the archiver never sends presence (`receipt.go:280-292`, `message.go:495`). If a sender ever sees Hamza's name under "Delivered" while his phone was offline, this is the cause.
- `ack` nodes for every stanza, sent after the archiver's handler has stored the event (`SynchronousAck`).
- Decryption-retry receipts and message re-requests to Hamza's own phone (`AutomaticMessageRerequestFromPhone`), pre-key uploads, app-state fetches and history-sync receipts.
- The media re-upload request (`SendMediaRetryReceipt`, only from `internal/wa/rerequest.go`), sent to Hamza's own phone for expired media at most 10 times a minute.

Watch for, and record: extra ticks, "online", "typing…", "read" (blue ticks), or any message in any chat that Hamza did not send.

## Queries used below

Run them on the server **as the service user, read-only** (running sqlite3 as root can leave root-owned database files that the service cannot open):

```sh
sudo -u whatsapp-archiver sqlite3 -readonly -header -column /var/lib/whatsapp-archiver/archive.db
```

At the `sqlite>` prompt, first name the chat you are looking at. Take the address from `archiver chats` (a group looks like `120363…@g.us`):

```
.parameter set :chat "'120363…@g.us'"
```

Paste one query at a time. Leave sqlite3 with `.quit`. Raw tables: `messages` (one row per message), `message_events` (edits, deletions, reactions, pins, poll votes, decryption gaps), `chat_events` (group and app-state changes), `media` (download queue), `history_sync_chunks`. Views: `messages_resolved` (one row per message with LID and phone-number forms merged, column `canonical_jid`) and `open_gaps`. Times are Unix seconds, shown here in UTC unless the query says `localtime` (the server's time zone).

These queries were run against a scratch archive built with `archiver replay internal/ingest/testdata/events/synthetic --data <scratch>` (the synthetic scenario: invented numbers only), extended by hand with a duplicate message and media rows. Where a query was shown there, the expected result below is what it returned.

**Q1 — every message in the chat.** Photos, voice notes, PDFs, stickers and videos show their type in `media`; `fwd` is 1 for a forwarded message, `reply` is 1 for a reply. A poll has no text and no `media`.

```sql
SELECT m.id, datetime(m.ts,'unixepoch') AS utc, m.sender_jid, m.media_type AS media, m.forwarded AS fwd,
       m.quoted_id <> '' AS reply, substr(m.text,1,24) AS text
FROM messages m WHERE m.chat_jid = :chat ORDER BY m.ts;
```

**Q2 — events by kind.** `edit`, `revoke` (delete for everyone), `reaction`, `pin`, `poll_vote`, `undecryptable`; also possible: `protocol`, `secret_encrypted`.

```sql
SELECT kind, COUNT(*) AS n FROM message_events WHERE chat_jid = :chat GROUP BY kind ORDER BY kind;
```

**Q3 — edited and deleted messages keep their original text** (`edited_to` is empty for a deletion, and for an edit WhatsApp sent encrypted).

```sql
SELECT e.kind, m.id, m.text AS original_text, e.new_text AS edited_to
FROM message_events e JOIN messages m ON m.chat_jid = e.chat_jid AND m.id = e.target_id
WHERE e.chat_jid = :chat AND e.kind IN ('edit','revoke') ORDER BY e.ts;
```

**Q4 — group and app-state changes.** `group_info` (name, topic, settings), `participants` (`joined` / `left_group`), `app_state` with `action` archive / mute / pin. The group's first `group_info` row can show 1970-01-01: a group join carries no WhatsApp timestamp.

```sql
SELECT datetime(ts,'unixepoch') AS utc, kind, json_extract(value,'$.action') AS action,
       json_extract(value,'$.name') AS new_name, json_extract(value,'$.join') AS joined,
       json_extract(value,'$.leave') AS left_group, json_extract(value,'$.archived') AS archived,
       json_extract(value,'$.muted') AS muted
FROM chat_events WHERE chat_jid = :chat ORDER BY ts;
```

**Q5 — media states.** `done` = downloaded to the `path` column's file; `skipped` with reason `type skipped` (stickers, GIFs) or `over 100 MB cap` (videos); `pending`, `expired`, `retry_requested` and `failed` are not final.

```sql
SELECT type, mime, state, reason, COUNT(*) AS n FROM media WHERE chat_jid = :chat GROUP BY type, mime, state, reason ORDER BY type;
```

**Q6 — LID duplicates (spike 5).** `raw_duplicates` counts `(canonical chat, message id)` pairs stored more than once in `messages`; `view_duplicates` is the same count in `messages_resolved`. Scratch result with one duplicate planted: raw 1, view 0; on the untouched synthetic scenario both are 0.

```sql
SELECT COUNT(*) AS raw_duplicates FROM (
  SELECT COALESCE(a.canonical_jid, m.chat_jid) AS chat, m.id
  FROM messages m LEFT JOIN chat_aliases a ON a.alias_jid = m.chat_jid
  GROUP BY chat, m.id HAVING COUNT(*) > 1);
SELECT COUNT(*) AS view_duplicates FROM (
  SELECT canonical_jid, id FROM messages_resolved GROUP BY canonical_jid, id HAVING COUNT(*) > 1);
SELECT (SELECT COUNT(*) FROM messages) AS raw_rows, (SELECT COUNT(*) FROM messages_resolved) AS resolved_rows,
       (SELECT COUNT(*) FROM id_mappings) AS mappings;
```

**Q7 — the same message id in two different chats** (a missing LID to phone-number link would show here). Expected: no rows.

```sql
SELECT id, COUNT(DISTINCT canonical_jid) AS chats, GROUP_CONCAT(DISTINCT canonical_jid) AS in_chats
FROM messages_resolved GROUP BY id HAVING COUNT(DISTINCT canonical_jid) > 1;
```

**Q8 — history reach (spikes 1 and 2).** Per chat (oldest message overall and oldest that arrived as history), per chat type, every chunk, and the progress milestones.

```sql
SELECT m.canonical_jid AS chat, COALESCE(MAX(c.name),'') AS name,
       CASE WHEN m.canonical_jid LIKE '%@g.us' THEN 'group' WHEN m.canonical_jid LIKE '%@newsletter' THEN 'channel' ELSE 'direct' END AS chat_type,
       COUNT(*) AS messages, datetime(MIN(m.ts),'unixepoch') AS oldest_utc,
       datetime(MIN(CASE WHEN m.source='history' THEN m.ts END),'unixepoch') AS oldest_history_utc
FROM messages_resolved m LEFT JOIN chats c ON c.jid = m.chat_jid
GROUP BY m.canonical_jid ORDER BY chat_type, oldest_utc;

SELECT CASE WHEN canonical_jid LIKE '%@g.us' THEN 'group' WHEN canonical_jid LIKE '%@newsletter' THEN 'channel' ELSE 'direct' END AS chat_type,
       COUNT(DISTINCT canonical_jid) AS chats, COUNT(*) AS messages, datetime(MIN(ts),'unixepoch') AS oldest_utc
FROM messages_resolved GROUP BY chat_type;

SELECT chunk_order AS "order", sync_type, progress, conversation_count AS chats, message_count AS msgs,
       datetime(oldest_ts,'unixepoch') AS oldest_utc, datetime(received_at,'unixepoch') AS received_utc
FROM history_sync_chunks ORDER BY received_at, chunk_order;

SELECT COUNT(*) AS chunks, datetime(MIN(received_at),'unixepoch') AS first_chunk_utc,
       datetime(MIN(CASE WHEN progress = 100 THEN received_at END),'unixepoch') AS reached_100_utc,
       datetime(MAX(received_at),'unixepoch') AS last_chunk_utc
FROM history_sync_chunks;
```

To find chunks that arrived after a restart, list the restarts with `journalctl -u archiver -o short-iso | grep Started`, then change the time in this query (UTC; the server's `journalctl` times are local):

```sql
SELECT chunk_order, progress, message_count, datetime(received_at,'unixepoch') AS received_utc
FROM history_sync_chunks WHERE received_at > strftime('%s','2026-10-15 12:00:00') ORDER BY received_at;
```

**Q9 — duplicates and gaps after a restart (step 6).** The first query lists the last 30 messages with local times; the second must return no rows (no text stored twice in the last two hours; send numbered messages "1", "2", "3" … to make this meaningful).

```sql
SELECT datetime(ts,'unixepoch','localtime') AS local_time, id, sender_jid, substr(text,1,30) AS text
FROM messages_resolved WHERE canonical_jid = :chat ORDER BY ts DESC LIMIT 30;
SELECT text, COUNT(*) AS n FROM messages WHERE chat_jid = :chat AND text <> ''
AND ts > strftime('%s','now','-2 hours') GROUP BY text HAVING COUNT(*) > 1;
```

**Q10 — seven-day audit (step 12).** Messages per day for one chat, and every decryption gap with its state. The `OPEN` rows must end up recovered or explained; `archiver status` shows the same count in `open gaps`.

```sql
SELECT date(ts,'unixepoch','localtime') AS day, COUNT(*) AS messages
FROM messages_resolved WHERE canonical_jid = :chat AND ts >= strftime('%s','now','-7 days')
GROUP BY day ORDER BY day;
SELECT datetime(e.ts,'unixepoch','localtime') AS when_local, e.chat_jid, e.target_id,
       CASE WHEN g.target_id IS NULL THEN 'recovered' ELSE 'OPEN' END AS state
FROM message_events e
LEFT JOIN open_gaps g ON g.chat_jid = e.chat_jid AND g.target_id = e.target_id AND g.event_id = e.event_id
WHERE e.kind = 'undecryptable' ORDER BY e.ts;
SELECT COUNT(*) AS open_gaps FROM open_gaps;
```

**Q11 — row counts per table (step 11).** Run on the live archive and on the restored copy.

```sql
SELECT 'chats' AS tbl, COUNT(*) AS n FROM chats
UNION ALL SELECT 'participants', COUNT(*) FROM participants
UNION ALL SELECT 'id_mappings', COUNT(*) FROM id_mappings
UNION ALL SELECT 'messages', COUNT(*) FROM messages
UNION ALL SELECT 'message_events', COUNT(*) FROM message_events
UNION ALL SELECT 'chat_events', COUNT(*) FROM chat_events
UNION ALL SELECT 'history_sync_chunks', COUNT(*) FROM history_sync_chunks
UNION ALL SELECT 'media', COUNT(*) FROM media;
```

## Steps

### 1. Before pairing

- [ ] Record the iOS version (Settings → General → About), the WhatsApp version (WhatsApp → Settings → Help → App Info) and the date and time. iOS ____; WhatsApp ____; date/time ____.
- [ ] Configure exclusions, time zone and ntfy (setup guide sections 5 and 6). A test notification reached the phone: `curl -d test https://ntfy.sh/<topic>` showed `test`, and `archiver run` on the unpaired folder showed **WhatsApp Archiver is not paired** and exited with code 3 (safe only before pairing: never run `archiver run` in a terminal once the service is installed). Result: ____

### 2. Pair once with recording (spike 1 starts)

The recorder writes **every** event the archiver receives, excluded chats included, as readable JSON files, and every history chunk whole. Keep the folder on the server (it is created with mode 0700), never copy it elsewhere unscrubbed, and delete it in step 10. The history chunks make it large; check `df -h /var/lib/whatsapp-archiver` first.

- [ ] Pair with the pair command of setup guide section 7, adding the option `--record /var/lib/whatsapp-archiver/rec` (the service may only write inside the data folder). Leave it running 10+ minutes; press Ctrl-C only after no `history chunk imported` line has appeared for several minutes. Pairing time (date/time of the scan): ____
- [ ] Start the service recording for the first day only. Do section 8 of the setup guide up to and including `sudo systemctl daemon-reload`, but do not enable it yet. Then `sudo systemctl edit archiver`, put this in the editor, save and leave:

  ```
  [Service]
  ExecStart=
  ExecStart=/opt/whatsapp-archiver/archiver run --data /var/lib/whatsapp-archiver --record /var/lib/whatsapp-archiver/rec
  ```

  then `sudo systemctl enable --now archiver`. Expected: `ps -o args= -C archiver` prints a command line ending in `--record /var/lib/whatsapp-archiver/rec`, and `sudo ls /var/lib/whatsapp-archiver/rec | tail -3` lists files such as `000123-message.json`. Result: ____

### 3. Test-group day

Use a WhatsApp group with exactly one other person, created for this. Do not @-mention anyone, send contact cards or type phone numbers (the scrubber keeps free text). Exchange, from both phones: text, reply, forward, edit, delete for everyone, reaction, pin, poll and a vote, voice note, PDF, image, sticker, a video over 100 MB, a group name change, adding and removing a participant; then archive and mute the chat on the phone.

- [ ] `archiver chats` and `archiver status` show the group; note its address for the queries. Address ____
- [ ] Q1: every item above is a row (text, reply with `reply` 1, forward with `fwd` 1, image / audio / document, sticker, video; the poll has an empty text and no `media`). Result: ____
- [ ] Q2 and Q3: `edit`, `revoke`, `reaction`, `pin`, `poll_vote` each at least once; the edited and deleted messages still show their original text. Result: ____
- [ ] Q4: the name change, the participant added and removed, `archive`, `mute` rows. Result: ____
- [ ] Q5: image, voice note (`audio`) and PDF `done`; the sticker `skipped` / `type skipped`; the big video `skipped` / `over 100 MB cap`. Check a file: `sudo sha256sum <path>` equals the `sha256` of its `media` row (`SELECT path, sha256 FROM media WHERE state = 'done';`). Result: ____
- [ ] Nothing appeared on anyone's phone from the archiver: no extra message, reaction, receipt or notification in the group or in any other chat. Result: ____

### 4. Receipt audit (spike 4)

- [ ] Prepare: a second phone in the test group; close WhatsApp Desktop / Web and every other linked device on Hamza's account (Settings → Linked Devices shows only the archiver); service running. The "online" status shows only in one-to-one chats, so also open a direct chat with Hamza's number on the second phone and keep it open.
- [ ] Put Hamza's phone in airplane mode for 30 minutes. From the second phone send several messages to the group and to the direct chat. Expected: ticks stay at one grey tick (sent) and the "Message info" of each group message lists Hamza under **Remaining** (or the equivalent list of people not yet delivered to), never under **Delivered** or **Read**; the direct chat never shows "online" or "typing…" for Hamza. Result: ____
- [ ] Phone back on; watch for 24 hours. Expected: "online" or "typing…" is never attributed to the archiver; no blue ticks; no message from Hamza that he did not send. Result: ____

### 5. Version-drift drill (spike 3)

- [ ] `sudo systemctl stop archiver`; edit `/var/lib/whatsapp-archiver/config.yaml` so that the block reads `wa: {check_latest_version: false, version_override: "2.3000.1040847988"}`; `sudo systemctl start archiver`.
- [ ] Expected within 5 minutes: `archiver status` shows `state: client_outdated` and `client version: 2.3000.1040847988 (override)`; an ntfy alert **WhatsApp client outdated** with the text `Version 2.3000.1040847988 was rejected; update the archiver`; the service stays up (`systemctl is-active archiver` prints `active`, and `systemctl show -p NRestarts archiver` prints `NRestarts=0`). Time of the alert after the start: ____. Result: ____
- [ ] Set `check_latest_version: true` and `version_override: ""`; `sudo systemctl restart archiver`. Expected: `state: connected`, `client version: … (latest)` or `(built-in)`, and the alert **WhatsApp client outdated: recovered**. Result: ____ (If the state became `connected` while the old version was forced, WhatsApp still accepts it: record that and the drill is not proven.)

### 6. Kill and restart

- [ ] During a conversation (the second phone sends "1", "2", "3" … every 30 seconds) run `sudo systemctl restart archiver` after about five messages. Expected: Q9 shows every number once, none missing; its second query returns no rows. Result: ____
- [ ] Pull the server's network cable for 20 minutes. Expected: after about 15 to 20 minutes the alert **WhatsApp disconnected** arrives (`Disconnected for 16m0s …`); on reconnect **WhatsApp disconnected: recovered**, and the messages sent meanwhile appear in Q9. Result: ____

### 7. Unlink

- [ ] On the phone: Settings → Linked Devices → the archiver's entry → Log Out. Expected within a minute: ntfy **WhatsApp logged out**; `status` shows `state: logged_out (…)`. Result: ____
- [ ] Re-pair: `sudo systemctl stop archiver`; the pair command of setup guide section 7 (without `--record`); 10+ minutes, and Ctrl-C only after no `history chunk imported` line has appeared for several minutes; `sudo systemctl start archiver`. This triggers a second full history sync. Expected: no `already paired` error and `Paired. Capture is running.`; `status` shows `connected` and more `chunks`. Result: ____

### 8. LID dedup (spike 5)

- [ ] After the re-pair run Q6 and Q7. Raw duplicates may be 0 (the store skips an insert that matches an alias it already knows) or more (a link between the two forms arrived after both were stored): that is expected. The view must show 0, and Q7 must return no rows. `raw_duplicates` ____; `view_duplicates` (must be 0) ____; `mappings` ____; Q7 rows ____

### 9. History reach (spikes 1 and 2)

- [ ] 48 hours after the first pairing run Q8 and `archiver status`. Fill in one row per BMC group (add rows as needed) and one per chat type; for chunks after restarts use the last query of Q8 with the restart times from `journalctl`.

  | Group / chat type | Oldest message | Progress reached 100% at | Chunks | Chunks after restarts |
  | --- | --- | --- | --- | --- |
  | ____ | ____ | ____ | ____ | ____ |
  | ____ | ____ | ____ | ____ | ____ |
  | ____ | ____ | ____ | ____ | ____ |
  | direct chats | ____ | ____ | ____ | ____ |
  | groups (all) | ____ | ____ | ____ | ____ |

- [ ] Do the course groups reach the start of term? ____ (yes: mark backup import as not needed in the charter's later list.)
- [ ] Compared with Android (90 days ordinary; about 22 months full sync; 3 years with a raised size limit), iOS reaches back ____ . (Much sooner: raise backup import in the charter's later list.) Decision: ____

### 10. Commit the corpus

- [ ] Stop recording: `sudo systemctl revert archiver`, `sudo systemctl restart archiver`. Pick the test group's files on the server (the recorder also holds every other chat, which must not leave the server) and carry only those to the Mac:

  ```sh
  sudo -u whatsapp-archiver sh -c 'mkdir -p /var/lib/whatsapp-archiver/rec-group && cd /var/lib/whatsapp-archiver/rec && grep -l "TEST-GROUP-ADDRESS" *.json | xargs -I{} cp {} ../rec-group/'
  (umask 077 && rm -f /tmp/rec-group.tgz && sudo tar -C /var/lib/whatsapp-archiver -czf - rec-group > /tmp/rec-group.tgz)
  ls -l /tmp/rec-group.tgz
  ```

  Replace `TEST-GROUP-ADDRESS` with the address from step 3. Expected from `ls -l`: `-rw-------` and owner `hamza`; `umask 077` makes the archive unreadable to other users from the moment it is created. History chunks are not picked (their content is encoded); the synthetic scenario covers history.
- [ ] **Raw recordings and the scrub key must never be placed inside the repository** (`.gitignore` does not cover them, and a `git add -A` would commit real push names and numbers). On the Mac, work in a folder outside it. Run this from the repository folder; `REPO` remembers where the Linux and Mac programs are (use the `darwin-amd64` build on an Intel Mac):

  ```sh
  REPO=$PWD
  mkdir -p ~/rec-work && cd ~/rec-work
  scp hamza@SERVER:/tmp/rec-group.tgz .
  tar -xzf rec-group.tgz
  "$REPO/.cache/build/archiver-darwin-arm64" scrub ~/rec-work/rec-group ~/rec-work/rec-scrubbed
  "$REPO/.cache/build/archiver-darwin-arm64" replay ~/rec-work/rec-scrubbed --data ~/rec-work/replay-check --dump
  cd "$REPO"
  ```

  Expected: `scrubbed … into … (key …/archiver/scrub.key)` (the key is created outside the repository, in `~/Library/Application Support/archiver/`), then `replayed N events` and the dump. This follows `.claude/skills/record-fixtures/SKILL.md` steps 3 and 4. Read every file of `~/rec-work/rec-scrubbed` and every line of the dump.
- [ ] Only the reviewed, scrubbed folder goes into the repository: `cp -R ~/rec-work/rec-scrubbed internal/ingest/testdata/events/<scenario>`; log the line-by-line review in `PROGRESS.md`; create the golden once as the skill's step 5 says (`testdata/golden/<scenario>.sql`); confirm `make test` passes (`TestReplayMatchesGolden`) and `git status` lists only `internal/ingest/testdata/...` and `PROGRESS.md`. Each kind from step 3 is in the corpus: ____
- [ ] Delete every unscrubbed copy: on the server `sudo rm -r /var/lib/whatsapp-archiver/rec /var/lib/whatsapp-archiver/rec-group /tmp/rec-group.tgz`; on the Mac `rm -r ~/rec-work`. The `--record` option is gone from the service (`systemctl cat archiver` shows the original `ExecStart`). Result: ____

### 11. Backup restore

- [ ] Take a snapshot and open the newest one elsewhere:

  ```sh
  sudo -u whatsapp-archiver /opt/whatsapp-archiver/archiver backup --data /var/lib/whatsapp-archiver
  sudo -u whatsapp-archiver sh -c 'mkdir -p -m 700 /var/tmp/restore-test && cp "$(ls -1d /srv/backups/whatsapp-archiver/*/ | tail -n 1)archive.db" /var/lib/whatsapp-archiver/config.yaml /var/tmp/restore-test/'
  sudo -u whatsapp-archiver /opt/whatsapp-archiver/archiver status --data /var/tmp/restore-test
  sudo ls -A /var/tmp/restore-test
  ```

  Expected: the first line is `snapshot …/archive.db (… bytes, …)`; `status` prints `state: unknown (health.json not found)` (no running service) and the same chats and message counts as the live `status` (messages that arrived since the snapshot may add a few); the folder holds `archive.db` and `config.yaml` and **no `session.db`**.
- [ ] Run Q11 on `/var/tmp/restore-test/archive.db` and on the live archive; the snapshot's counts equal the live counts, or are lower only by what arrived since. (For an exact match, stop the service, take the snapshot, compare, start the service.) Then `sudo rm -r /var/tmp/restore-test`. Result: ____

### 12. Seven-day audit (SC-1, SC-10)

- [ ] After 7 days pick two groups. For each day, compare every message on the phone with Q10 (count per day) and with Q1 / Q9 for the days that differ; compare `archiver status` totals and `open gaps`.

  | Group | Day | On phone | In database | Missing | Explanation / recovered |
  | --- | --- | --- | --- | --- | --- |
  | ____ | ____ | ____ | ____ | ____ | ____ |
  | ____ | ____ | ____ | ____ | ____ | ____ |

- [ ] Expected: zero missing, and every `OPEN` gap in Q10 recovered or explained (`open_gaps` 0 or each one understood). Any miss halts phase 1 until fixed. Result: ____

### 13. Commit

- [ ] Commit this file with the results as `docs: phase 0 acceptance results`, and write the decisions the spikes forced into the charter's decision log.
