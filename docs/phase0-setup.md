# Phase 0 setup

This guide takes you from a blank PC to a running archiver. It assumes you have never used Linux before. Every command can be copied as is. After most commands there is an **Expected** block that shows what a healthy result looks like. Outputs that depend on your machine (numbers, dates, names) are marked with `…`; the lines that matter are the ones that must match.

The archiver is a program that stays logged in to WhatsApp as an extra "linked device" (like WhatsApp Web) and writes every message it receives to a database. It never sends anything.

## 0. How to read this guide

- **On the Mac** means a Terminal window on your Mac, in this repository's folder.
- **On the server** means a Terminal window where you have connected to the server with SSH (section 2).
- `sudo` runs one command as the administrator. It asks for your server password the first time.
- `SERVER` stands for the server's name or IP address. `hamza` stands for your user name on the server. Replace both.
- `whatsapp-archiver` is a separate, powerless user that the archiver runs as. Run the archiver itself only as that user (`sudo -u whatsapp-archiver …`). If you run it as root by mistake, it creates root-owned files that the service cannot read; the fix is `sudo chown -R whatsapp-archiver:whatsapp-archiver /var/lib/whatsapp-archiver`.

## 1. Install the server

1. Download Ubuntu Server 26.04 LTS from ubuntu.com and write it to a USB stick with balenaEtcher.
2. Start the PC from the stick and follow the installer. When it offers **OpenSSH server**, tick it. That is what lets you log in from your Mac.
3. After the reboot, log in on the PC's screen once and run `hostname -I`. The first number it prints (for example `192.168.1.50`) is `SERVER`.

## 2. Connect with SSH

SSH is a way to type commands on another computer over the network. On a Mac open Terminal; on Windows 10 or 11 open PowerShell. Both have `ssh` and `scp` built in.

On the Mac:

```sh
ssh hamza@SERVER
```

The first time, it asks `Are you sure you want to continue connecting (yes/no/[fingerprint])?`. Type `yes`, then your server password.

Expected: a banner that starts with `Welcome to Ubuntu 26.04 …` and a prompt that ends in `$`. From now on, the lines marked "On the server" are typed here. Type `exit` to come back to the Mac.

Find out what kind of processor the server has. You need the answer in section 3:

```sh
uname -m
```

Expected: `x86_64` (most PCs; use the `amd64` build) or `aarch64` (ARM machines such as a Raspberry Pi; use the `arm64` build).

Set the server's time zone (the nightly backup runs at `backup.time` in the **server's** time zone) and install two tools used later:

```sh
sudo timedatectl set-timezone Europe/London
sudo apt update
sudo apt install -y sqlite3 rsync
timedatectl | grep "Time zone"
```

Expected: `Time zone: Europe/London (…)`. `apt` prints many lines and ends without the word `Error`.

## 3. Build on the Mac and copy to the server

On the Mac, in this repository (the first run downloads a pinned Go compiler into `.tools/` and checks its SHA-256; it takes a few minutes):

```sh
make build-all
```

Expected: five lines `build linux/amd64`, `build linux/arm64`, `build darwin/arm64`, `build darwin/amd64`, `build windows/amd64`, and no error. The programs are in `.cache/build/`:

```sh
ls .cache/build
```

Expected: the list includes `archiver-linux-amd64`, `archiver-linux-arm64`, `archiver-darwin-arm64`, `archiver-darwin-amd64` and `archiver-windows-amd64.exe`.

Copy the Linux program that matches `uname -m` on the server and the service file to the server. Run only one of the first two lines: the `amd64` line when `uname -m` printed `x86_64`, the `arm64` line when it printed `aarch64`:

```sh
scp .cache/build/archiver-linux-amd64 hamza@SERVER:/tmp/archiver
scp .cache/build/archiver-linux-arm64 hamza@SERVER:/tmp/archiver
scp deploy/archiver.service hamza@SERVER:/tmp/archiver.service
```

The wrong one fails later with `Exec format error` (section 12).

Expected: one progress line per file ending in `100%`.

## 4. Create the user and folders

On the server:

```sh
sudo useradd --system --home-dir /var/lib/whatsapp-archiver --shell /usr/sbin/nologin whatsapp-archiver
sudo install -d -o whatsapp-archiver -g whatsapp-archiver -m 0700 /var/lib/whatsapp-archiver
sudo install -d -o whatsapp-archiver -g whatsapp-archiver /opt/whatsapp-archiver
sudo install -d -o whatsapp-archiver -g whatsapp-archiver -m 0700 /srv/backups/whatsapp-archiver
sudo install -m 0755 /tmp/archiver /opt/whatsapp-archiver/archiver
id whatsapp-archiver
/opt/whatsapp-archiver/archiver version
```

The commands print nothing until the last two. Expected:

```
uid=…(whatsapp-archiver) gid=…(whatsapp-archiver) groups=…(whatsapp-archiver)
whatsapp-archiver 0.1.0-phase0
whatsmeow v0.0.0-…
```

What the folders are for:

| Folder | Holds |
| --- | --- |
| `/opt/whatsapp-archiver` | the program |
| `/var/lib/whatsapp-archiver` | the data folder: `config.yaml`, `archive.db` (the messages), `session.db` (the WhatsApp login), `media/`, `health.json` |
| `/srv/backups/whatsapp-archiver` | nightly copies of `archive.db` |

The service may write only to the data folder and `/srv/backups/whatsapp-archiver` (the `ReadWritePaths` line of `deploy/archiver.service`). The backup folder **must exist before the service starts**, or systemd refuses to start it (`status=226/NAMESPACE`). A backup folder on a second disk is better than one on the same disk; if you use a different path, set it in `backup.destination` and add it to `ReadWritePaths` in the service file before copying that file.

## 5. Set up ntfy alerts

ntfy delivers alerts to your phone. Anyone who knows the topic name can read it and write to it, so make the name long and random. On the Mac:

```sh
echo whatsapp-archiver-$(openssl rand -hex 8)
```

Expected: something like `whatsapp-archiver-3f9c0a1b7d2e4c58`. Write it down. Install the **ntfy** app on your iPhone, tap **+** and subscribe to that topic (default server `ntfy.sh`). Then send a test message from the server:

```sh
curl -d test https://ntfy.sh/whatsapp-archiver-3f9c0a1b7d2e4c58
```

Expected: a line of JSON starting `{"id":"…` in the Terminal, and within a few seconds a notification on the phone with the topic name as heading and the text `test`. If nothing arrives, check the topic spelling in the app before going further.

## 6. Write the configuration

Anything that must never be stored has to be excluded **before pairing**: history arrives immediately after pairing, and stored rows are never deleted. The file contains the ntfy topic (and possibly a token), so only the service user may read it: owned by `whatsapp-archiver`, mode `0600`, inside the data folder. A file owned by root makes the service fail with `permission denied`.

On the server:

```sh
sudo touch /var/lib/whatsapp-archiver/config.yaml
sudo chown whatsapp-archiver:whatsapp-archiver /var/lib/whatsapp-archiver/config.yaml
sudo chmod 0600 /var/lib/whatsapp-archiver/config.yaml
sudo nano /var/lib/whatsapp-archiver/config.yaml
```

In nano, paste the text below (change the lines marked `CHANGE`), press Ctrl-O then Enter to save and Ctrl-X to leave. Every key shown is optional; the values written are the defaults, except `time_zone`, `exclude`, `alerts.ntfy` and `backup.destination`, which you set.

```yaml
# Time zone for the times that `archiver status` prints. Default: the server's own zone.
time_zone: Europe/London

# Chats that must never be stored. Direct chats: the other person's phone number as digits only,
# country code first, no "+" and no leading 0 after the country code, then @s.whatsapp.net.
# CHANGE (default: none)
exclude:
  - "441234567890@s.whatsapp.net"

# What the phone is asked to send when you pair. WhatsApp may send less.
history: {days_limit: 3650, size_mb_limit: 4096}

# Media downloads.
media:
  download: [document, image, audio, video]
  video_max_mb: 100
  skip: [sticker, gif]
  workers: 2
  retry_requests_per_minute: 10

alerts:
  # CHANGE the topic to yours from section 5 (default: no alerts at all)
  ntfy: {url: "https://ntfy.sh/CHANGE-TO-A-PRIVATE-TOPIC", token: ""}
  no_events_after: 6h        # "No WhatsApp events" alert
  disconnected_after: 15m    # "WhatsApp disconnected" alert
  phone_quiet_after: 240h    # "Phone inactive" alert
  disk_free_min_gb: 5        # "Disk space low" alert

# Nightly snapshot of archive.db (default destination: none, so the nightly backup fails and alerts).
# `time` is in the server's time zone; `keep` is how many snapshots to keep.
backup: {destination: "/srv/backups/whatsapp-archiver", time: "03:30", keep: 7}

wa:
  check_latest_version: true   # ask WhatsApp for its newest client version at start and after a rejection
  version_override: ""         # force one version, for example "2.3000.1040847988"; used only for the drill
```

Notes:

- A misspelled key is an error, not ignored: `decode config: yaml: unmarshal errors: line 7: field exclud not found in type config.Config`. Do not add `data_dir`: the `--data` option decides the folder.
- **Direct chats**: write the phone-number form. When WhatsApp later shows the same person under a `…@lid` address, the exclusion covers it as soon as the archiver has seen the link between the two addresses, which arrives with the messages and with history. **Groups**: WhatsApp never shows a group's address, so it cannot be listed before pairing. After the first capture `archiver chats` lists the groups and their addresses (section 8); add the ones to exclude then. Rows stored before that stay in the database. If a group must never be stored, do a practice pairing first: pair as in section 7 with a throw-away data folder (`--data /var/tmp/practice`; it needs no `config.yaml` and stores everything it sees), run `archiver chats --data /var/tmp/practice`, then unlink the device in WhatsApp (Settings → Linked Devices) and delete the folder with `sudo rm -r /var/tmp/practice`. It costs one extra pairing.
- After editing the file later, `ls -l` must still show the owner `whatsapp-archiver` and `-rw-------`; repeat the `chown` and `chmod` above if not.

Check the file and test ntfy through the archiver itself. `run` reads the configuration, finds that the phone is not paired yet, sends one alert and exits without contacting WhatsApp. This test is safe only now, before pairing: **never run `archiver run` in a terminal once the service is installed**, because after pairing it connects with the same login as the service:

```sh
sudo ls -l /var/lib/whatsapp-archiver
sudo -u whatsapp-archiver /opt/whatsapp-archiver/archiver run --data /var/lib/whatsapp-archiver; echo "exit code $?"
```

Expected:

```
-rw------- 1 whatsapp-archiver whatsapp-archiver … config.yaml
not paired: run `archiver pair` in a terminal first
exit code 3
```

and on the phone a notification titled **WhatsApp Archiver is not paired** with the same "not paired: run … first" text. If `ntfy alert failed: …` is printed instead, the URL in `alerts.ntfy` is wrong or the server has no internet. A different message (`decode config: …`) names the line to fix.

## 7. Pair the phone

Run this in a Terminal window that is large (about 100 columns wide and 50 lines tall) so the QR code is not cut:

```sh
sudo -u whatsapp-archiver /opt/whatsapp-archiver/archiver pair --data /var/lib/whatsapp-archiver
```

Expected: `Scan the QR code from WhatsApp → Linked Devices.`, then a square QR code. On the iPhone open WhatsApp → Settings → Linked Devices → Link a Device and scan it. A code expires after a short time; the message "QR code timed out; run archiver pair again." means start again. If it says `already paired`, this data folder already holds a login.

After the scan: `Paired. Capture is running.` Lines of JSON (the log) scroll by; that is normal. Lines such as `{"time":"…","level":"INFO","msg":"history chunk imported","progress":40,"stored":…,"skipped":…}` show WhatsApp sending your old messages in pieces. **Leave it running for at least ten minutes, and press Ctrl-C only after no `history chunk imported` line has appeared for several minutes**; a large history can take an hour or more. The program must be running while history arrives, because nothing is stored otherwise. Ctrl-C stops it; it first finishes the piece being stored, which can take up to 30 seconds.

## 8. Start the service

On the server:

```sh
sudo cp /tmp/archiver.service /etc/systemd/system/archiver.service
sudo systemctl daemon-reload
sudo systemctl enable --now archiver
systemctl status archiver --no-pager
```

Expected: `Created symlink … archiver.service …`, then in the status the line `Active: active (running) since …` and `Main PID: … (archiver)`. To see the program's log:

```sh
journalctl -u archiver -n 20 --no-pager
```

Expected: JSON lines, no `"level":"ERROR"`. Follow it live with `journalctl -u archiver -f` (Ctrl-C leaves).

Check what has been captured:

```sh
sudo -u whatsapp-archiver /opt/whatsapp-archiver/archiver status --data /var/lib/whatsapp-archiver
sudo -u whatsapp-archiver /opt/whatsapp-archiver/archiver chats --data /var/lib/whatsapp-archiver
```

Expected for `status` (an example; the columns are separated by tabs):

```
state: connected
client version: 2.3000.… (latest)
last event: 2026-10-15 12:15 BST
phone last active: 2026-10-15 12:10 BST
history: in progress (60%), chunks 4, last chunk 2026-10-15 12:14 BST
name   canonical JID   messages   oldest   newest   media failed   media expired   media pending   open gaps
Course group   120363…@g.us   412   2024-03-02 00:30 GMT   2026-10-15 12:15 BST   0   0   3   0
total   1 chats   412   -   -   0   0   3   0
recovered panics: 0
data folder: 85.8 MB
```

- `state` is `connected` within a few seconds of the start. `unknown (health.json not found)` means the program has not connected yet or is not running.
- `client version` says which WhatsApp client version is in use and why: `latest`, `built-in`, `override`, or `built-in: <error>` when the latest-version check failed.
- `history:` becomes `complete back to <date>` when WhatsApp reports 100%. Late pieces can still arrive after restarts and are stored.
- `open gaps` counts messages the archiver received but could not decrypt and has not yet received again. It should fall to 0 by itself.
- `media pending` / `expired` / `failed` count downloads that have not finished; stickers, GIFs and videos over 100 MB are deliberately skipped and are not counted.

Expected for `chats`: one line per chat, `canonical JID`, name, message count and `excluded=yes` or `excluded=no`, for example `120363…@g.us	Course group	412	excluded=no`. Use it to find the addresses for the `exclude:` list; after changing the file run `sudo systemctl restart archiver`.

## 9. Alerts: what they mean

Each alert is sent at most once every 6 hours while the problem lasts, and a `<title>: recovered` message follows when it clears.

| Title on the phone | Meaning and what to do |
| --- | --- |
| WhatsApp Archiver is not paired | The service started without a login. Run `archiver pair` (section 7), then start the service. |
| WhatsApp disconnected | No connection for more than `disconnected_after` (15 minutes). Check the server's network, then `systemctl status archiver`. |
| WhatsApp logged out | The phone removed this linked device (or WhatsApp did). The service stays up but captures nothing. Re-pair: `sudo systemctl stop archiver`, run `pair`, Ctrl-C, `sudo systemctl start archiver`. Stored messages are kept. |
| WhatsApp temporarily banned | WhatsApp paused the account for the time in the alert. Capture resumes by itself; do not restart repeatedly. |
| WhatsApp session replaced | Another program is using the same login (a second copy of the archiver, or a copied `session.db`). Stop the other one. |
| WhatsApp client outdated | WhatsApp rejected the client version named in the alert. See section 10. |
| No WhatsApp events | Connected, but nothing for `no_events_after` (6 hours). A quiet night is normal; otherwise look at the log and restart. |
| Phone inactive | Your phone has not sent anything for `phone_quiet_after` (10 days). WhatsApp unlinks devices after 14 days of phone inactivity: open WhatsApp on the phone and send a message. |
| Disk space low | Less than `disk_free_min_gb` (5 GB) free in the data folder. |
| Archive write failed | A database write keeps failing and is being retried. The alert names no chat; the error is in the log (`journalctl -u archiver`, line `write failing`). Usually disk full or wrong file owner. |
| Archive read failed | Looking up a chat's other address for the `exclude:` list keeps failing and is being retried; nothing of that chat is stored meanwhile. The error is in the log; the causes are the same as for a failed write. |
| Recovered panic | A bug was caught and capture went on. Keep the log lines for a bug report. |
| Backup failed | The nightly snapshot did not work. Check that the backup folder exists, belongs to `whatsapp-archiver` and has space. |

## 10. Updating

WhatsApp retires old client versions every few weeks. The archiver asks WhatsApp for the newest version at start and again after every rejection (every 30 minutes while rejected), so often it recovers alone when WhatsApp publishes a newer version. If the `WhatsApp client outdated` alert stays for more than an hour, install a newer build.

On the Mac, in the repository, with the newest revision checked out:

```sh
make build-all
shasum -a 256 .cache/build/archiver-linux-amd64
scp .cache/build/archiver-linux-amd64 hamza@SERVER:/tmp/archiver
```

(use `archiver-linux-arm64` if `uname -m` on the server says `aarch64`). The `shasum` line prints a long fingerprint; write down its first characters. On the server:

```sh
sudo systemctl stop archiver
sudo install -m 0755 /tmp/archiver /opt/whatsapp-archiver/archiver
sudo systemctl start archiver
sha256sum /opt/whatsapp-archiver/archiver
/opt/whatsapp-archiver/archiver version
systemctl status archiver --no-pager | grep Active
sudo -u whatsapp-archiver /opt/whatsapp-archiver/archiver status --data /var/lib/whatsapp-archiver | head -2
```

Expected: `sha256sum` starts with the same characters as the Mac's `shasum`; `version` prints two lines (the `whatsmeow` line changes when the WhatsApp library was updated; the first line is `whatsapp-archiver 0.1.0-phase0` for every phase 0 build, so it cannot tell two builds apart); `Active: active (running) since …` with a time of a minute ago; `state: connected` within a minute. WhatsApp queues messages for a linked device that is offline and delivers them when it connects again; keep the stop short anyway.

## 11. Backups

- **Nightly snapshot**: at `backup.time` (server time) the archiver writes a verified copy of `archive.db` into a new folder `/srv/backups/whatsapp-archiver/<UTC timestamp>/` and keeps the newest `backup.keep` folders. Try it now:

  ```sh
  sudo -u whatsapp-archiver /opt/whatsapp-archiver/archiver backup --data /var/lib/whatsapp-archiver
  sudo ls /srv/backups/whatsapp-archiver
  ```

  Expected: `snapshot /srv/backups/whatsapp-archiver/20261015T033000.123456789Z/archive.db (… bytes, …ms)` and one folder per snapshot. A failure prints the reason and sends a `Backup failed` alert at night.
- **`session.db` is never backed up, copied or restored.** It is the WhatsApp login; a copy used on two machines gets the login replaced. Moving to a new server or recovering from a disaster means pairing again, with a snapshot's `archive.db` placed in the new data folder first.
- **Media** (`media/`) is not in the snapshots; copy it every night with a systemd timer, as below. The copy never deletes files (no `--delete`), so an accidental wipe of the data folder is not mirrored into the backup.

Create the media timer on the server. The `ConditionPathExists` line skips a run while no media has been downloaded yet (the `media` folder appears with the first download):

```sh
command -v rsync
sudo install -d -m 0700 /srv/backups/whatsapp-archiver-media
sudo tee /etc/systemd/system/archiver-media-backup.service > /dev/null <<'EOF'
[Unit]
Description=Copy WhatsApp Archiver media to the backup folder
ConditionPathExists=/var/lib/whatsapp-archiver/media

[Service]
Type=oneshot
ExecStart=/usr/bin/rsync -a /var/lib/whatsapp-archiver/media/ /srv/backups/whatsapp-archiver-media/
EOF
sudo tee /etc/systemd/system/archiver-media-backup.timer > /dev/null <<'EOF'
[Unit]
Description=Nightly copy of WhatsApp Archiver media

[Timer]
OnCalendar=*-*-* 04:30:00
Persistent=true

[Install]
WantedBy=timers.target
EOF
sudo systemctl daemon-reload
sudo systemctl enable --now archiver-media-backup.timer
sudo systemctl start archiver-media-backup.service
systemctl list-timers archiver-media-backup.timer --no-pager
```

Expected: `command -v rsync` prints `/usr/bin/rsync` (if it prints another path, use that path in `ExecStart`); the timer list shows a `NEXT` time around 04:30 tomorrow. After media has arrived, `sudo ls /srv/backups/whatsapp-archiver-media` lists the same chat folders as `sudo ls /var/lib/whatsapp-archiver/media`.

## 12. If something goes wrong

| Symptom | Cause and fix |
| --- | --- |
| `systemctl status` shows `status=226/NAMESPACE` | The backup folder (or the data folder) does not exist: section 4. |
| `permission denied` for `config.yaml`, `archive.db` or `session.db` | A file is owned by root. `sudo chown -R whatsapp-archiver:whatsapp-archiver /var/lib/whatsapp-archiver`, then `sudo chmod 0600 /var/lib/whatsapp-archiver/config.yaml`. |
| `systemctl status` shows `inactive` / `failed` with `status=3` and the not-paired message | The service was started before pairing. Pair (section 7), then `sudo systemctl start archiver`. systemd does not retry exit code 3 on purpose. |
| `no archive at …/archive.db: run archiver first` from `status` | The `--data` path is wrong, or nothing has been captured there yet. |
| `already paired` from `pair` | The data folder already has a login. To pair again after an unlink, stop the service and run `pair`; if it still says this, ask for help before deleting `session.db`. |
| `exclude entry "…" is not a chat address` | A line of `exclude:` is not written as `<digits>@s.whatsapp.net`, `<digits>@lid` or `<id>@g.us` (no `+`, no spaces). Correct it; the archiver does not start until then. |
| `Exec format error` when the program or the service starts | The wrong Linux build was copied. Copy `archiver-linux-arm64` when `uname -m` prints `aarch64`, `archiver-linux-amd64` when it prints `x86_64` (section 3). |
| `Text file busy` while installing a new program | The service is still running: `sudo systemctl stop archiver` first. |
