# Phase 0 setup

Build on the Mac from this repository (the first `make` run downloads the pinned Go and golangci-lint into `.tools/`, verifying their SHA-256):

```sh
make tools
GOOS=linux GOARCH=amd64 make build
scp .cache/build/archiver hamza@SERVER:/tmp/archiver
```

On Ubuntu Server 26.04 LTS (install it from a balenaEtcher USB, select OpenSSH):

```sh
ssh hamza@SERVER
sudo useradd --system --home /var/lib/whatsapp-archiver --shell /usr/sbin/nologin whatsapp-archiver
sudo install -d -o whatsapp-archiver -g whatsapp-archiver /opt/whatsapp-archiver /var/lib/whatsapp-archiver
sudo install -m 0755 /tmp/archiver /opt/whatsapp-archiver/archiver
```

Before pairing, create `/var/lib/whatsapp-archiver/config.yaml`. Anything that must never be stored must already be excluded because history arrives immediately and raw rows are never deleted:

```yaml
time_zone: Europe/London
exclude:
  - "441234567890@s.whatsapp.net"
history: {days_limit: 3650, size_mb_limit: 4096}
alerts:
  ntfy: {url: "https://ntfy.sh/CHANGE-TO-A-PRIVATE-TOPIC", token: ""}
backup: {destination: "/srv/backups/whatsapp-archiver", time: "03:30", keep: 7}
```

The systemd unit allows writes only to the data folder and `/srv/backups/whatsapp-archiver`, so a different `backup.destination` must be added to `ReadWritePaths` in `deploy/archiver.service` too. Create the backup folder owned by the service user:

```sh
sudo install -d -o whatsapp-archiver -g whatsapp-archiver -m 700 /srv/backups/whatsapp-archiver
```

Install the ntfy phone app and subscribe to that private topic. Pair in a terminal, leave capture running for at least ten minutes, then press Ctrl-C:

```sh
sudo -u whatsapp-archiver /opt/whatsapp-archiver/archiver pair --data /var/lib/whatsapp-archiver
```

On the iPhone use WhatsApp → Settings → Linked Devices → Link a Device. Copy `deploy/archiver.service` to `/etc/systemd/system/`, then:

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now archiver
sudo -u whatsapp-archiver /opt/whatsapp-archiver/archiver status --data /var/lib/whatsapp-archiver
sudo -u whatsapp-archiver /opt/whatsapp-archiver/archiver chats --data /var/lib/whatsapp-archiver
journalctl -u archiver -f
```

WhatsApp retires client versions frequently. A `client_outdated` alert means build the current revision, replace the binary, and restart the service. Late history chunks are accepted after restarts.

Nightly snapshots contain only `archive.db`. Never copy or restore `session.db`; recovery is a fresh pairing. Copy media separately with a root-owned systemd timer running:

```sh
rsync -a --delete /var/lib/whatsapp-archiver/media/ /srv/backups/whatsapp-archiver-media/
```
