---
name: deploy-ryzen
description: Build, checksum, install and verify a new archiver binary on ryzen, keeping archiver.prev for rollback.
---

Source: `README.md` section 10 "Updating" (lines 405-430) and "Let Claude Code do the setup" (from line 550).

Rules, first:
- Ask Hamza for an explicit yes before each action that changes the server: copying the binary, stopping or restarting the service, swapping the binary.
- The agent never runs `pair` or `run` itself and never reads message text.
- Do not deploy a build whose `git describe` ends in `-dirty`.

Paths: binary `/opt/whatsapp-archiver/archiver`, previous `/opt/whatsapp-archiver/archiver.prev`, unit `archiver.service`, data `/var/lib/whatsapp-archiver`, server `hamza@192.168.0.105`. Use `archiver-linux-arm64` if `uname -m` on the server says `aarch64`.

1. On the Mac, newest revision checked out: `make build-all`, then `git describe --always --dirty --tags` and `shasum -a 256 .cache/build/archiver-linux-amd64`. Note both.
2. Ask for yes. `scp .cache/build/archiver-linux-amd64 hamza@192.168.0.105:/tmp/archiver`, then on the server `sha256sum /tmp/archiver` and compare with the Mac value.
3. Ask for yes. Keep the old binary: `sudo cp -p /opt/whatsapp-archiver/archiver /opt/whatsapp-archiver/archiver.prev`.
4. Ask for yes. `sudo systemctl stop archiver`, `sudo install -m 0755 /tmp/archiver /opt/whatsapp-archiver/archiver`, `sudo systemctl start archiver`. Keep the stop short.
5. Post-checks:
   - `sha256sum /opt/whatsapp-archiver/archiver` starts like the Mac's `shasum`.
   - `/opt/whatsapp-archiver/archiver version`: first line `whatsapp-archiver <commit>` equals the Mac's `git describe`.
   - `systemctl is-active archiver` prints `active`.
   - `journalctl -u archiver -n 20 --no-pager` shows no ERROR loop (levels and messages only; log has no message text).
   - `sudo -u whatsapp-archiver /opt/whatsapp-archiver/archiver status --data /var/lib/whatsapp-archiver | head` shows `state: connected` within a minute.
   - `ss -ltn | grep 8080` shows `127.0.0.1:8080` only, never `0.0.0.0` or `*`.
6. Rollback (ask for yes): `sudo systemctl stop archiver`, `sudo cp -p /opt/whatsapp-archiver/archiver.prev /opt/whatsapp-archiver/archiver` (or `mv` to swap), `sudo systemctl start archiver`, then repeat the post-checks.

The limited sudoers file (README lines 665-684) allows only the exact `install -m 0755 /tmp/archiver ...`, `systemctl` and `status` commands; `cp` for `archiver.prev` and `ss` are not in it, so Hamza runs them or widens it.
