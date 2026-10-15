# Letting Claude Code set up and look after the server

This guide shows how to let Claude Code, running on your main computer (the Mac or PC you use every day), log in to your Linux server over SSH and do the setup in [phase0-setup.md](phase0-setup.md) for you: create the folders, copy the program, write the configuration, start the service, check its health and install updates.

It cannot do everything. Four steps need you, and the guide tells Claude Code to stop and ask at each:

1. **Scanning the pairing QR code with your phone** (section 7 of the setup guide).
2. **Subscribing your phone to the ntfy topic.**
3. **Deciding which chats to exclude.** That is a privacy decision, and stored rows can never be deleted.
4. **The acceptance steps on the phone**: sending test messages, unlinking the device.

This guide has not been tried end to end on a real server. The commands are standard, but read each one before you run it.

## What you are agreeing to

Claude Code runs commands on the server as a user you create for it. You decide how much that user may do. This guide uses a deliberately small set of permissions, described in section 5:

- Claude Code **can** see the service status, read the log, start, stop and restart the service, run `status`, `chats` and `backup`, replace the program, and rewrite `config.yaml`.
- Claude Code **cannot** read `archive.db`, `session.db` or the media folder directly: they belong to the `whatsapp-archiver` user and are mode `0700`. It sees message counts and chat names through `status` and `chats`, and whatever the log contains. The log names chat addresses and message IDs but has no message text.
- **Replacing the program is a powerful permission.** Whoever can replace the program that runs as `whatsapp-archiver` can make it read the archive and the WhatsApp login. If you do not want an AI agent to have that power, remove the `install` line in section 5 and do updates yourself.
- Full passwordless `sudo` would be root. Section 4 uses it for a short setup window only; do not leave it in place.

Archive contents are other people's messages, and a message can contain text written to trick an AI ("ignore your instructions and…"). That is one more reason this setup keeps message text away from Claude Code.

## 1. Before you start

You need:

- A Linux server with Ubuntu installed (Server or Desktop) and OpenSSH running, reachable from your main computer. Section 1 and 2 of [phase0-setup.md](phase0-setup.md) cover this; on Ubuntu Desktop run `sudo apt install -y openssh-server` first.
- Your normal login on the server (called `hamza` below) with `sudo` rights, for the one-time steps.
- Claude Code installed on your main computer (the Claude desktop app's Code tab, or the `claude` command). Install it from Anthropic's page and sign in.
- This repository cloned on your main computer. Open Claude Code in the repository folder.

`SERVER` below means the server's name or IP address (`hostname -I` on the server prints it).

## 2. Create a separate login for Claude Code

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

## 3. Make the connection easy and non-interactive

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

## 4. A short setup window

The first-time setup creates users, folders and a service file, which needs root. For that, and only for that, give `claude` full passwordless `sudo` for the length of the setup.

**On the server:**

```sh
echo 'claude ALL=(ALL) NOPASSWD: ALL' | sudo tee /etc/sudoers.d/claude-setup
sudo chmod 0440 /etc/sudoers.d/claude-setup
sudo visudo -cf /etc/sudoers.d/claude-setup
```

Expected: the last line says `parsed OK`.

When the setup is finished (section 7), you will remove it with `sudo rm /etc/sudoers.d/claude-setup`. Claude Code is told to ask you to do that.

## 5. The permanent, limited permissions

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

## 6. Allow the commands in Claude Code (optional)

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

## 7. Start the session

On your main computer, open Claude Code in the repository folder and paste this as your first message. Replace the two values.

```
You are setting up the WhatsApp Archiver on my Linux server by following docs/phase0-setup.md. Read that guide and docs/claude-code-deploy.md first.

How you reach the server: only with `ssh archiver-box '...'` and `scp ... archiver-box:...`. The user is `claude`. Never connect to any other machine. During setup it has passwordless sudo; afterwards only the limited commands in /etc/sudoers.d/claude-archiver.

My values: time zone: <YOUR TIME ZONE, for example Europe/London>. ntfy topic: <YOUR PRIVATE TOPIC>.

Do the setup yourself, in order: build with `make build-all`, copy the program that matches `uname -m` on the server, create the user and folders (section 4), write config.yaml (section 6) with the file owned by whatsapp-archiver and mode 0600, install the service (section 8). After each command, compare its output with the guide's "Expected" block, and stop and tell me if it differs.

Stop and ask me before: choosing what goes in `exclude` (ask me for the list, do not guess), pairing (I will run `archiver pair` myself in my own terminal and scan the QR code), the first start of the service, and removing the setup sudo window.

Rules you must keep:
- Never run `archiver pair` or `archiver run` yourself; the service is started only with systemctl, and only after I say so.
- Never read, print, copy or query archive.db, session.db or anything in the media folder, and never print message text. Use `status`, `chats` and the log.
- Never delete data, backups or the data folder, and never edit sudoers files. Tell me what to type instead.
- Never commit config.yaml, the ntfy topic or any key.
- If something fails, show me the error and propose a fix before trying it.

When the service is running and `status` is healthy, tell me to remove the setup window with `sudo rm /etc/sudoers.d/claude-setup` and to create the limited permissions file from section 5 of docs/claude-code-deploy.md.
```

What happens next:

- Claude Code builds the program, copies it, and works through sections 4 to 6 of the setup guide.
- It stops and asks for your `exclude` list. Use `441234567890@s.whatsapp.net` style addresses for people. Groups cannot be listed before pairing; see the setup guide's note on a practice pairing.
- It tells you when to pair. **You** open a terminal, connect to the server with your own login and run section 7 of the setup guide, scanning the QR code with your phone. Leave it running as the guide says, then press Ctrl-C.
- You say "start the service". Claude Code starts it, checks `status` and the log for the `history chunk imported` lines, and reports.
- Finally you remove the setup window (`sudo rm /etc/sudoers.d/claude-setup`) and create the limited file from section 5. After that, run `ssh archiver-box 'sudo -n -l'` once more and check that the `ALL` line is gone.

## 8. Afterwards: ask it to look after the server

New session, same folder, same `archiver-box` rules. Things you can ask:

- "Check the archiver: status, and any warnings in the last 200 log lines." (`status` and the `journalctl` line from section 5.)
- "I got a *WhatsApp client outdated* alert. Update the program." Claude Code builds with `make build-all`, copies the program to `/tmp/archiver` on the server, then installs it and restarts the service. Read [section 10 of the setup guide](phase0-setup.md#10-updating) to know what it should do. The checks in that section (`sha256sum`, `version`, `systemctl status`) need no `sudo`, so `claude` can run them as itself.
- "Take a backup now and tell me the size." (`backup`)

It cannot fix anything that needs the pairing QR code, a changed ownership or a new folder, because those need root. It will tell you what to type, or you can reopen the setup window for that one job and close it again.

## 9. Taking access away

On the server, any time:

```sh
sudo rm -f /etc/sudoers.d/claude-setup /etc/sudoers.d/claude-archiver
sudo deluser --remove-home claude
```

On your main computer remove the `archiver-box` block from `~/.ssh/config` and delete `~/.ssh/archiver_box` and `~/.ssh/archiver_box.pub`. Your archive and the service are unaffected.

## If something goes wrong

| Symptom | Cause and fix |
| --- | --- |
| `Permission denied (publickey)` | The key line in `/home/claude/.ssh/authorized_keys` is wrong, or the file or folder has the wrong owner or mode. Repeat the three `install`/`chown`/`chmod` commands in section 2. |
| `ssh: connect to host … timed out` | Wrong `SERVER`, the server is off, or it is on another network. On the server run `hostname -I`. |
| `sudo: a password is required` | The command differs from the one in the sudoers file by even one character, or the setup window was already closed. Run `ssh archiver-box 'sudo -n -l'` to see exactly what is allowed. |
| `sudo: no tty present` or `a terminal is required` | The same cause: `sudo` wanted a password. Do not "fix" it by adding `requiretty` or a wider rule; fix the command instead. |
| Claude Code asks permission for every command | Section 6 is optional; approve each one, or add the allow rule. |
| `Host key verification failed` | The server was reinstalled. Remove its old line with `ssh-keygen -R SERVER`, then connect once in your own terminal. |
