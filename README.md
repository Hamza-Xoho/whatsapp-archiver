# WhatsApp Archiver

[![CI](https://github.com/Hamza-Xoho/whatsapp-archiver/actions/workflows/ci.yml/badge.svg)](https://github.com/Hamza-Xoho/whatsapp-archiver/actions/workflows/ci.yml)

A self-hosted, receive-only WhatsApp capture logger. It stays logged in as an extra linked device (like WhatsApp Web), writes every message and event it receives into a local SQLite archive, downloads the attached files, and tells your phone when something stops working. It never sends anything a contact can see.

It is written in Go on top of [whatsmeow](https://github.com/tulir/whatsmeow), builds to one static binary for Linux, macOS and Windows, and needs no C compiler.

> **Status: phase 0 (capture logger).** The capture pipeline, health alerts, backups and the Ubuntu setup guide (below) are built and tested. It has not yet been paired with a real account: that acceptance run is the next step (see [What is left](#what-is-left)). The modules, web UI and search described in the [charter](docs/charter/WhatsApp%20Archiver%20%E2%80%94%20Project%20Charter%20%26%20Build%20Plan.md) are later phases.

## Read this first

- **It is an unofficial client.** WhatsApp does not offer a supported way to archive personal chats, and linking an unofficial client carries a small risk that the account is restricted. The archiver stays silent to keep that risk low, but the risk is not zero.
- **It depends on staying up to date.** WhatsApp retires old client versions without warning. The archiver detects a rejected version, raises a "client outdated" alert and can look up the newest version itself, but you still have to update the program when told.
- **Your archive holds other people's messages.** Keep it, its backups and anything made from it for your own use, and check the law where you live before archiving anyone's messages. Chats that must never be stored belong in the `exclude` list, and it has to be set **before** the first pairing because stored rows are never deleted.
- **Your data stays on your machines.** The archiver talks to WhatsApp, and to the ntfy server you choose for alerts. Alert text contains no chat identifier and no message text.

## What it does

- Pairs as a linked device and requests the full available history, with explicit size and age limits. Every history chunk is recorded in a ledger so a missing chunk shows up.
- Stores messages, edits, deletions, reactions, group and chat events exactly as received. Rows are insert-only: nothing is updated or deleted.
- Records messages the phone could not decrypt as open gaps, and fills them in when the phone resends them.
- Keeps phone-number and LID identities apart as received and links them in a mapping table, so one person is one chat in the read views.
- Downloads documents, images, audio and video (sizes and types configurable) with a rate-limited retry queue for files WhatsApp has already expired.
- Skips the chats you exclude, matching both a person's phone-number and LID forms. If the exclusion check itself cannot run, nothing is written for that chat.
- Survives reconnects, temporary bans, replaced sessions and client-version cut-offs with bounded back-off, and shuts down without cutting an import short.
- Takes a nightly snapshot of the archive (never of the login session) and prunes old snapshots.
- Sends [ntfy](https://ntfy.sh) alerts: disconnected, logged out, banned, client outdated, no events, phone inactive, disk space low, write failures, recovered panics, failed backups.
- Includes a record and replay harness with a fixture scrubber, so real traffic can become test fixtures without real numbers.

## How it works

```
 WhatsApp ──► internal/wa ──► recorder ──► ingest.Pipeline ──► store (archive.db)
 (linked      (only package     (optional,    one write path:        insert-only tables,
  device)      that touches      --record)     live, history and      LID/phone alias views
               whatsmeow)                      replay all use it          │
                                                   │                      ▼
                                                   └──► media queue   media/ files
                                                   └──► health ──► ntfy alerts
```

- `internal/wa` is the only package that imports the whatsmeow client. Static guards (depguard, forbidigo and an AST test in `internal/archtest`) fail the build if any other package imports it, or if any code outside one allow-listed file calls a method that sends something visible.
- `archive.db` has exactly one writer. The login session lives in a separate `session.db`, which is never backed up, copied or restored.
- Live events, history sync and fixture replay go through the same `ingest.Pipeline`, so a replayed fixture exercises the real code.

The invariants, package boundaries and workflow rules are in [AGENTS.md](AGENTS.md).

## Contents

- [Read this first](#read-this-first), [What it does](#what-it-does), [How it works](#how-it-works)
- **[Setup guide](#setup-guide)**: install Ubuntu, build, configure, pair, run, update, back up, troubleshoot
- **[Let Claude Code do the setup](#let-claude-code-do-the-setup-optional)**: an optional way to do the setup guide over SSH from your main computer
- [Command reference](#command-reference) and [What is on disk](#what-is-on-disk)
- [Quick start for developers](#quick-start-developers), [Development](#development), [What is left](#what-is-left)

## Setup guide

This guide takes you from a blank PC to a running archiver. It assumes you have never used Linux before. Every command can be copied as is. After most commands there is an **Expected** block that shows what a healthy result looks like. Outputs that depend on your machine (numbers, dates, names) are marked with `…`; the lines that matter are the ones that must match.

The archiver is a program that stays logged in to WhatsApp as an extra "linked device" (like WhatsApp Web) and writes every message it receives to a database. It never sends anything.

Choose how you want to set up the server:

| You | Use |
| --- | --- |
| Have a spare PC and want to use it with a screen and mouse | Install **Ubuntu Desktop** and follow this guide in a Terminal on that PC ([Using Ubuntu Desktop](#using-ubuntu-desktop-instead-of-ubuntu-server)) |
| Want a PC with no screen that you reach from your Mac or Windows PC | Install **Ubuntu Server** and follow the guide over SSH, from section 1 |
| Want Claude Code to type the commands for you | Follow [Let Claude Code do the setup](#let-claude-code-do-the-setup-optional) after installing Ubuntu (sections 1 and 2 below) |

### 0. How to read this guide

- **On the Mac** means a Terminal window on your Mac, in this repository's folder. If you do not have the folder yet, run `git clone https://github.com/Hamza-Xoho/whatsapp-archiver.git` and then `cd whatsapp-archiver` (this needs Git installed and access to the repository).
- **On the server** means a Terminal window where you have connected to the server with SSH (section 2). If the PC has Ubuntu Desktop and you sit at it, it means a Terminal on that PC; read "Using Ubuntu Desktop instead of Ubuntu Server" below first.
- `sudo` runs one command as the administrator. It asks for your server password the first time.
- `SERVER` stands for the server's name or IP address. `hamza` stands for your user name on the server. Replace both.
- `whatsapp-archiver` is a separate, powerless user that the archiver runs as. Run the archiver itself only as that user (`sudo -u whatsapp-archiver …`). If you run it as root by mistake, it creates root-owned files that the service cannot read; the fix is `sudo chown -R whatsapp-archiver:whatsapp-archiver /var/lib/whatsapp-archiver`.

### Using Ubuntu Desktop instead of Ubuntu Server

If you would rather have a screen, a mouse and ordinary windows, install **Ubuntu Desktop 26.04 LTS** on the PC instead of the server edition. It is the same Ubuntu, so every command in this guide works unchanged. The differences:

1. **Section 1:** download Ubuntu Desktop 26.04 LTS from ubuntu.com, write it to a USB stick with balenaEtcher and follow the installer. A "Minimal installation" is enough. There is no OpenSSH question; the user you create in the installer can use `sudo`.
2. **Section 2 (SSH):** skip the `ssh` command. Press Ctrl-Alt-T to open a Terminal on the PC itself. Everywhere the guide says "on the server", type the commands in that Terminal. Run the rest of section 2 as written (`uname -m`, the time zone and the `apt` lines), leaving out `ssh`. `SERVER` is never needed.
3. **Section 3 (copy the program):** the program has to get from your Mac onto this PC. Pick one way and do the rest of section 3 (`make build-all` on the Mac) as written.
   - **USB stick.** On the Mac copy `archiver-linux-amd64` (or `archiver-linux-arm64`, matching `uname -m`) and `deploy/archiver.service` onto a stick. Plug it into the PC, then in the PC's Terminal run `ls /media/$USER/` to see the stick's name and copy the files (replace `STICK` with that name):

     ```sh
     cp /media/$USER/STICK/archiver-linux-amd64 /tmp/archiver
     cp /media/$USER/STICK/archiver.service /tmp/archiver.service
     ```

   - **Over the network.** On the PC run `sudo apt install -y openssh-server` and `hostname -I` (the first number is `SERVER`), then use the `scp` lines in section 3 from the Mac.
4. **Never let the PC sleep.** A desktop suspends itself after a while, and a suspended PC captures nothing. On the PC run:

   ```sh
   sudo systemctl mask sleep.target suspend.target hibernate.target hybrid-sleep.target
   systemctl is-enabled sleep.target
   ```

   Expected: the last command prints `masked`. This also means closing a laptop's lid no longer suspends it. Leave the PC plugged in. In Settings → Power you may also set Screen Blank to any value; the screen turning off is harmless.
5. **Section 7 (pairing):** maximise the Terminal window (double-click its title bar) so the QR code is not cut.
6. **Updates and restarts:** Ubuntu may ask to restart after an update. That is fine once section 8 is done, because the service starts again by itself after a reboot. Do not restart while the first history sync from section 7 is still running.

### 1. Install the server

1. Download Ubuntu Server 26.04 LTS from ubuntu.com and write it to a USB stick with balenaEtcher.
2. Start the PC from the stick and follow the installer. When it offers **OpenSSH server**, tick it. That is what lets you log in from your Mac.
3. After the reboot, log in on the PC's screen once and run `hostname -I`. The first number it prints (for example `192.168.1.50`) is `SERVER`.

### 2. Connect with SSH

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

### 3. Build on the Mac and copy to the server

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

### 4. Create the user and folders

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

### 5. Set up ntfy alerts

ntfy delivers alerts to your phone. Anyone who knows the topic name can read it and write to it, so make the name long and random. On the Mac:

```sh
echo whatsapp-archiver-$(openssl rand -hex 8)
```

Expected: something like `whatsapp-archiver-3f9c0a1b7d2e4c58`. Write it down. Install the **ntfy** app on your iPhone, tap **+** and subscribe to that topic (default server `ntfy.sh`). Then send a test message from the server:

```sh
curl -d test https://ntfy.sh/whatsapp-archiver-3f9c0a1b7d2e4c58
```

Expected: a line of JSON starting `{"id":"…` in the Terminal, and within a few seconds a notification on the phone with the topic name as heading and the text `test`. If nothing arrives, check the topic spelling in the app before going further.

### 6. Write the configuration

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

### 7. Pair the phone

Run this in a Terminal window that is large (about 100 columns wide and 50 lines tall) so the QR code is not cut:

```sh
sudo -u whatsapp-archiver /opt/whatsapp-archiver/archiver pair --data /var/lib/whatsapp-archiver
```

Expected: `Scan the QR code from WhatsApp → Linked Devices.`, then a square QR code. On the iPhone open WhatsApp → Settings → Linked Devices → Link a Device and scan it. A code expires after a short time; the message "QR code timed out; run archiver pair again." means start again. If it says `already paired`, this data folder already holds a login.

After the scan: `Paired. Capture is running.` Lines of JSON (the log) scroll by; that is normal. Lines such as `{"time":"…","level":"INFO","msg":"history chunk imported","progress":40,"stored":…,"skipped":…}` show WhatsApp sending your old messages in pieces. **Leave it running for at least ten minutes, and press Ctrl-C only after no `history chunk imported` line has appeared for several minutes**; a large history can take an hour or more. The program must be running while history arrives, because nothing is stored otherwise. Ctrl-C stops it; it first finishes the piece being stored, which can take up to 30 seconds.

### 8. Start the service

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

### 9. Alerts: what they mean

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

### 10. Updating

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

### 11. Backups

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

### 12. If something goes wrong

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

## Let Claude Code do the setup (optional)

This guide shows how to let Claude Code, running on your main computer (the Mac or PC you use every day), log in to your Linux server over SSH and do the setup in the setup guide above for you: create the folders, copy the program, write the configuration, start the service, check its health and install updates.

It cannot do everything. Four steps need you, and the guide tells Claude Code to stop and ask at each:

1. **Scanning the pairing QR code with your phone** (section 7 of the setup guide above).
2. **Subscribing your phone to the ntfy topic.**
3. **Deciding which chats to exclude.** That is a privacy decision, and stored rows can never be deleted.
4. **The acceptance steps on the phone**: sending test messages, unlinking the device.

This guide has not been tried end to end on a real server. The commands are standard, but read each one before you run it.

### C0. What you are agreeing to

Claude Code runs commands on the server as a user you create for it. You decide how much that user may do. This guide uses a deliberately small set of permissions, described in step C5:

- Claude Code **can** see the service status, read the log, start, stop and restart the service, run `status`, `chats` and `backup`, replace the program, and rewrite `config.yaml`.
- Claude Code **cannot** read `archive.db`, `session.db` or the media folder directly: they belong to the `whatsapp-archiver` user and are mode `0700`. It sees message counts and chat names through `status` and `chats`, and whatever the log contains. The log names chat addresses and message IDs but has no message text.
- **Replacing the program is a powerful permission.** Whoever can replace the program that runs as `whatsapp-archiver` can make it read the archive and the WhatsApp login. If you do not want an AI agent to have that power, remove the `install` line in step C5 and do updates yourself.
- Full passwordless `sudo` would be root. Step C4 uses it for a short setup window only; do not leave it in place.

Archive contents are other people's messages, and a message can contain text written to trick an AI ("ignore your instructions and…"). That is one more reason this setup keeps message text away from Claude Code.

### C1. Before you start

You need:

- A Linux server with Ubuntu installed (Server or Desktop) and OpenSSH running, reachable from your main computer. Sections 1 and 2 of the setup guide above cover this; on Ubuntu Desktop run `sudo apt install -y openssh-server` first.
- Your normal login on the server (called `hamza` below) with `sudo` rights, for the one-time steps.
- Claude Code installed on your main computer (the Claude desktop app's Code tab, or the `claude` command). Install it from Anthropic's page and sign in.
- This repository cloned on your main computer. Open Claude Code in the repository folder.

`SERVER` below means the server's name or IP address (`hostname -I` on the server prints it).

### C2. Create a separate login for Claude Code

Give Claude Code its own account on the server, not your own. You can remove it later without touching anything else.

**On your main computer**, create a key used only for this (press Enter for the file location and choose a passphrase if you want one; with a passphrase you add the key to your agent first, see the note below):

```sh
ssh-keygen -t ed25519 -f ~/.ssh/archiver_box -C "claude-code archiver"
cat ~/.ssh/archiver_box.pub
```

The second command prints one long line starting with `ssh-ed25519`. Copy it.

**On the server** (in a Terminal on it, or over your normal SSH login), create the user without a password, so the only way in is the key:

```sh
sudo adduser --disabled-password --gecos "" claude
sudo install -d -m 0700 -o claude -g claude /home/claude/.ssh
sudo nano /home/claude/.ssh/authorized_keys
```

Paste the copied line, save (Ctrl-O, Enter) and leave (Ctrl-X), then:

```sh
sudo chown claude:claude /home/claude/.ssh/authorized_keys
sudo chmod 0600 /home/claude/.ssh/authorized_keys
```

Expected: no output.

Note on passphrases: a key with a passphrase is safer if your computer is lost, but Claude Code cannot type the passphrase. Load the key into your SSH agent in your own terminal before starting a session: on a Mac `ssh-add --apple-use-keychain ~/.ssh/archiver_box`, on Linux `ssh-add ~/.ssh/archiver_box`. On Windows, start the OpenSSH Authentication Agent service and run `ssh-add` the same way.

### C3. Make the connection easy and non-interactive

**On your main computer**, add these lines to `~/.ssh/config` (create the file if needed; on Windows it is `C:\Users\YOU\.ssh\config`), replacing `SERVER`:

```
Host archiver-box
    HostName SERVER
    User claude
    IdentityFile ~/.ssh/archiver_box
    IdentitiesOnly yes
    BatchMode yes
    ServerAliveInterval 30
```

`BatchMode yes` makes ssh fail instead of waiting for a password that nobody can type, which is what you want from an agent. Test it:

```sh
ssh archiver-box 'whoami; hostname'
```

Expected: `claude`, then the server's name. The first time, ssh asks `Are you sure you want to continue connecting`; type `yes` in your own terminal once (BatchMode refuses to ask, so run it with `ssh -o BatchMode=no archiver-box` the first time).

### C4. A short setup window

The first-time setup creates users, folders and a service file, which needs root. For that, and only for that, give `claude` full passwordless `sudo` for the length of the setup.

**On the server:**

```sh
echo 'claude ALL=(ALL) NOPASSWD: ALL' | sudo tee /etc/sudoers.d/claude-setup
sudo chmod 0440 /etc/sudoers.d/claude-setup
sudo visudo -cf /etc/sudoers.d/claude-setup
```

Expected: the last line says `parsed OK`.

When the setup is finished (step C7), you will remove it with `sudo rm /etc/sudoers.d/claude-setup`. Claude Code is told to ask you to do that.

### C5. The permanent, limited permissions

Claude Code needs a few `sudo` commands afterwards for day-to-day work. List the real paths on your server first, because `sudo` matches the exact path:

```sh
command -v systemctl journalctl install tee
```

Expected: four paths, usually `/usr/bin/systemctl`, `/usr/bin/journalctl`, `/usr/bin/install`, `/usr/bin/tee`. If yours differ, use yours below.

Create the file with `sudo visudo -f /etc/sudoers.d/claude-archiver` and paste this (`visudo` refuses a file with a syntax error, which protects you from locking yourself out of `sudo`):

```
Cmnd_Alias ARCHIVER_SERVICE = \
    /usr/bin/systemctl status archiver --no-pager, \
    /usr/bin/systemctl is-active archiver, \
    /usr/bin/systemctl start archiver, \
    /usr/bin/systemctl stop archiver, \
    /usr/bin/systemctl restart archiver, \
    /usr/bin/journalctl -u archiver --no-pager -n 200

claude ALL=(root) NOPASSWD: ARCHIVER_SERVICE, \
    /usr/bin/install -m 0755 /tmp/archiver /opt/whatsapp-archiver/archiver, \
    /usr/bin/tee /var/lib/whatsapp-archiver/config.yaml

claude ALL=(whatsapp-archiver) NOPASSWD: \
    /opt/whatsapp-archiver/archiver status --data /var/lib/whatsapp-archiver, \
    /opt/whatsapp-archiver/archiver chats --data /var/lib/whatsapp-archiver, \
    /opt/whatsapp-archiver/archiver backup --data /var/lib/whatsapp-archiver, \
    /opt/whatsapp-archiver/archiver version
```

Every command is listed with its full arguments, so `claude` cannot run the same program with other options. Check what it now may do:

```sh
ssh archiver-box 'sudo -n -l'
```

Expected: the list above. (Before the setup window is closed, this also shows the `ALL` line.)

### C6. Allow the commands in Claude Code (optional)

By default Claude Code asks you before every command. For the first run, leave it that way and read each command. To stop the questions for ssh to this one server only, create `.claude/settings.local.json` in the repository folder (it is listed in `.gitignore`; never commit it):

```json
{
  "permissions": {
    "allow": [
      "Bash(ssh archiver-box *)",
      "Bash(scp * archiver-box:*)",
      "Bash(make build-all)"
    ]
  }
}
```

This does not allow anything on other hosts, or any other local command.

### C7. Start the session

On your main computer, open Claude Code in the repository folder and paste this as your first message. Replace the two values.

```
You are setting up the WhatsApp Archiver on my Linux server by following the setup guide in README.md. Read the whole "Setup guide" part and the "Let Claude Code do the setup" part of README.md first.

How you reach the server: only with `ssh archiver-box '...'` and `scp ... archiver-box:...`. The user is `claude`. Never connect to any other machine. During setup it has passwordless sudo; afterwards only the limited commands in /etc/sudoers.d/claude-archiver.

My values: time zone: <YOUR TIME ZONE, for example Europe/London>. ntfy topic: <YOUR PRIVATE TOPIC>.

Do the setup yourself, in order: build with `make build-all`, copy the program that matches `uname -m` on the server, create the user and folders (setup guide section 4), write config.yaml (setup guide section 6) with the file owned by whatsapp-archiver and mode 0600, install the service (setup guide section 8). After each command, compare its output with the guide's "Expected" block, and stop and tell me if it differs.

Stop and ask me before: choosing what goes in `exclude` (ask me for the list, do not guess), pairing (I will run `archiver pair` myself in my own terminal and scan the QR code), the first start of the service, and removing the setup sudo window.

Rules you must keep:
- Never run `archiver pair` or `archiver run` yourself; the service is started only with systemctl, and only after I say so.
- Never read, print, copy or query archive.db, session.db or anything in the media folder, and never print message text. Use `status`, `chats` and the log.
- Never delete data, backups or the data folder, and never edit sudoers files. Tell me what to type instead.
- Never commit config.yaml, the ntfy topic or any key.
- If something fails, show me the error and propose a fix before trying it.

When the service is running and `status` is healthy, tell me to remove the setup window with `sudo rm /etc/sudoers.d/claude-setup` and to create the limited permissions file from step C5 of README.md.
```

What happens next:

- Claude Code builds the program, copies it, and works through sections 4 to 6 of the setup guide above.
- It stops and asks for your `exclude` list. Use `441234567890@s.whatsapp.net` style addresses for people. Groups cannot be listed before pairing; see the setup guide's note on a practice pairing.
- It tells you when to pair. **You** open a terminal, connect to the server with your own login and run section 7 of the setup guide above, scanning the QR code with your phone. Leave it running as the guide says, then press Ctrl-C.
- You say "start the service". Claude Code starts it, checks `status` and the log for the `history chunk imported` lines, and reports.
- Finally you remove the setup window (`sudo rm /etc/sudoers.d/claude-setup`) and create the limited file from step C5. After that, run `ssh archiver-box 'sudo -n -l'` once more and check that the `ALL` line is gone.

### C8. Afterwards: ask it to look after the server

New session, same folder, same `archiver-box` rules. Things you can ask:

- "Check the archiver: status, and any warnings in the last 200 log lines." (`status` and the `journalctl` line from step C5.)
- "I got a *WhatsApp client outdated* alert. Update the program." Claude Code builds with `make build-all`, copies the program to `/tmp/archiver` on the server, then installs it and restarts the service. Read section 10 of the setup guide above ("Updating") to know what it should do. The checks in that section (`sha256sum`, `version`, `systemctl status`) need no `sudo`, so `claude` can run them as itself.
- "Take a backup now and tell me the size." (`backup`)

It cannot fix anything that needs the pairing QR code, a changed ownership or a new folder, because those need root. It will tell you what to type, or you can reopen the setup window for that one job and close it again.

### C9. Taking access away

On the server, any time:

```sh
sudo rm -f /etc/sudoers.d/claude-setup /etc/sudoers.d/claude-archiver
sudo deluser --remove-home claude
```

On your main computer remove the `archiver-box` block from `~/.ssh/config` and delete `~/.ssh/archiver_box` and `~/.ssh/archiver_box.pub`. Your archive and the service are unaffected.

### C10. If the connection or sudo fails

| Symptom | Cause and fix |
| --- | --- |
| `Permission denied (publickey)` | The key line in `/home/claude/.ssh/authorized_keys` is wrong, or the file or folder has the wrong owner or mode. Repeat the three `install`/`chown`/`chmod` commands in step C2. |
| `ssh: connect to host … timed out` | Wrong `SERVER`, the server is off, or it is on another network. On the server run `hostname -I`. |
| `sudo: a password is required` | The command differs from the one in the sudoers file by even one character, or the setup window was already closed. Run `ssh archiver-box 'sudo -n -l'` to see exactly what is allowed. |
| `sudo: no tty present` or `a terminal is required` | The same cause: `sudo` wanted a password. Do not "fix" it by adding `requiretty` or a wider rule; fix the command instead. |
| Claude Code asks permission for every command | Step C6 is optional; approve each one, or add the allow rule. |
| `Host key verification failed` | The server was reinstalled. Remove its old line with `ssh-keygen -R SERVER`, then connect once in your own terminal. |

## Command reference

```
archiver <command> [--data DIR]        DIR defaults to ./data
```

| Command | Purpose |
| --- | --- |
| `pair [--record DIR]` | Show the QR code, link the device, then capture. `--record` also saves supported events for building fixtures. |
| `run` | Capture as a service. Exits with code 3 (no restart) if not paired. |
| `status` | Read-only summary: connection state, client version, last event, history received, per-chat counts, failed and pending media, open gaps, data-folder size. |
| `chats` | Read-only list of chats with their canonical address and whether each is excluded. Use it to find group addresses for `exclude`. |
| `backup` | Take a snapshot of `archive.db` now. |
| `scrub IN OUT` | Replace the phone numbers and WhatsApp addresses in recorded fixtures with stable fakes. Message text and names are kept, so read the output before committing it. |
| `replay DIR [--data D] [--dump]` | Feed recorded fixtures through the real pipeline into a fresh archive. Refuses to write into an existing `archive.db`. |
| `version` | Program and whatsmeow versions. |

## What is on disk

| Path (inside the data folder) | Contents | Backed up |
| --- | --- | --- |
| `archive.db` | Messages, events, chats, id mappings, history ledger, media queue | Yes, nightly |
| `session.db` | The WhatsApp login (device keys) | **Never** |
| `media/` | Downloaded files | No |
| `config.yaml`, `health.json` | Settings; last known health state | No |

## Quick start (developers)

You need `git`, `make` and `curl`. The first `make` downloads a pinned, checksum-verified Go toolchain and golangci-lint into `.tools/`; nothing is installed globally.

```sh
git clone https://github.com/Hamza-Xoho/whatsapp-archiver.git
cd whatsapp-archiver
make check                       # race tests, lint, and builds for 5 platforms
make build
.cache/build/archiver version
```

The first `make check` is slow because it builds the Go cache from scratch.

| Command | What it does |
| --- | --- |
| `make test` | Race-enabled tests |
| `make lint` | Pinned golangci-lint |
| `make build` | Build for this machine into `.cache/build/archiver` |
| `make build-all` | Linux, macOS and Windows binaries into `.cache/build/` |
| `make check` | All of the above; run before every commit |

## Development

The project is built test-first and the rules are enforced by tooling, not by habit:

- **Red then green.** A failing regression test is committed separately (`test:`) before every fix (`fix:`). The pre-commit hook rejects deleted test files and, while `.claude/green.lock` exists, any change to a test file. Enable it with `git config core.hooksPath .githooks`.
- **Mutation testing.** [docs/reviews/2026-10-14-phase0-fixes-mutations/](docs/reviews/2026-10-14-phase0-fixes-mutations/) holds 285 exact-text mutation specs and a runner (`run.py`). On the current head all 285 are killed. Run a few with `python3 run.py specs.json <name>…`.
- **CON-1 guard.** Depguard, forbidigo and an AST test keep the WhatsApp client confined to `internal/wa` and ban every contact-visible send method.
- **Fixtures.** Real traffic is recorded with `pair --record`, scrubbed with `archiver scrub`, and committed only after the scrubbed files have been read. See `.claude/skills/record-fixtures/SKILL.md`.
- **CI.** `make check` runs on every push and pull request ([workflow](.github/workflows/ci.yml)). A weekly workflow opens a pull request that bumps whatsmeow to its latest commit, but only if `make check` still passes ([workflow](.github/workflows/bump-whatsmeow.yml)).

Where things are:

| Path | What |
| --- | --- |
| `cmd/archiver/` | CLI, service lifecycle, `status`, `chats`, `replay` |
| `internal/wa/` | The only whatsmeow client code: pairing, versions, downloads, the one media re-request |
| `internal/ingest/` | Normalization, exclusion filter, the single write path, recorder, scrubber |
| `internal/store/` | Schema, single writer, read views |
| `internal/media/` | Table-backed download workers and the re-request queue |
| `internal/health/`, `internal/backup/` | Alerts and snapshots |
| `internal/config/`, `internal/retry/`, `internal/guard/`, `internal/testkit/`, `internal/archtest/` | Config, shared helpers, static guards |
| `docs/` | [Charter](docs/charter/WhatsApp%20Archiver%20%E2%80%94%20Project%20Charter%20%26%20Build%20Plan.md), plan, research, the manual acceptance checklist, review and fix history |
| `PROGRESS.md` | Per-task log of commits, coverage and deviations |

For the story of how the phase 0 review findings were fixed, start with [docs/reviews/2026-10-14-phase0-fixes-mutations/final-report.md](docs/reviews/2026-10-14-phase0-fixes-mutations/final-report.md).

## What is left

**To finish phase 0 (the next steps, and the only ones that need a real account):**

1. Follow [docs/phase0-acceptance.md](docs/phase0-acceptance.md): pair once, exercise a throw-away test group, drill the version-drift, restart, unlink and backup-restore cases, and check history reach.
2. Let it run for seven days and audit that no message is missing (charter SC-1 and SC-10).
3. Record the corpus: scrub and commit the fixtures the acceptance run produces.
4. Decide the open questions in the [final report](docs/reviews/2026-10-14-phase0-fixes-mutations/final-report.md): whether to buffer decrypted events for redelivery, whether to archive history stub messages, and the recording-versus-privacy trade-off.

**Known small gaps (none can lose data or leak a chat):** `go mod tidy`, goroutine-leak checks in `wa`, `backup` and `store`, a guard for exported functions returning `any`, and a list of minor wording and tooling items in the [ledger](docs/reviews/2026-10-14-phase0-fixes-mutations/ledger.md).

**Before it can be shared as a product (later phases):** a licence, the module contract and first processing modules (transcription, translation), the web UI with browser pairing, Arabic-aware search and a read-only MCP server, then packaging (releases, Docker, service installers, a first-run wizard). The plan for each is in section 13 of the charter.

## Licence and credits

No licence has been chosen yet, so the code is not available for reuse. Parts are adapted from [openclaw/wacli](https://github.com/openclaw/wacli) under the MIT licence; see [NOTICE](NOTICE) and `docs/prior-art.md`. The WhatsApp protocol library is [whatsmeow](https://github.com/tulir/whatsmeow) (MPL-2.0). This project is not affiliated with or endorsed by WhatsApp or Meta.
