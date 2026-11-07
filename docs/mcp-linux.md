# Install the archive MCP companion on Linux

This is a separate Linux amd64 program for reading an existing legacy or V1
archive. It does not replace or upgrade the capture program. These are operator
instructions for a future installation; no production installation is implied
by the repository's synthetic tests.

The bridge makes no cloud requests. Hermes and an API key are unnecessary for
its local checks. Capture continues while the bridge is absent or stopped.
Use [the Hermes guide](hermes-linux.md) after the local checks pass.

## 1. Confirm the existing service without reading messages

On the archive machine, record the service identity and current process. Replace
`archiver.service` if your existing service has a different name:

```sh
systemctl show archiver.service -p MainPID -p ExecMainStartTimestamp -p User
systemctl show archiver.service -p FragmentPath
stat -c '%U %G %a %n' /var/lib/whatsapp-archiver /var/lib/whatsapp-archiver/archive.db
```

Confirm the archive location from your existing local configuration. Do not
print the login session or copy `session.db`. The installed archiver's revision
can be recorded with its existing `version` command if available; do not replace
that binary to obtain a revision. Metadata-only bridge inspection below reports
the detected schema. An agent helping with installation must not read live
message text; the human operator performs chat selection and content checks.

The default archive account is `whatsapp-archiver`, and the database path is
`/var/lib/whatsapp-archiver/archive.db`. The installer supports a validated custom
`/var/lib/.../archive.db` or `/srv/.../archive.db` only. Names may contain ASCII
letters, digits, `_`, `-`, and directory separators; no whitespace, shell
characters, relative paths, dot components, or symlinks. All existing archive
siblings must be singly linked regular files owned by the specified archive
account. Parent directories must belong to root or that account and have no
group/other write permissions. An unsupported layout needs an operator review,
not a recursive permission change or a copy of the live database.

## 2. Build or unpack the companion

In a source checkout, as your ordinary user:

```sh
make mcp-test
make mcp-build
make mcp-release
.cache/build/archiver-mcp version
```

The binary is `.cache/build/archiver-mcp`. Release archives are under
`.cache/release/`, named `archiver-mcp-linux-amd64-<revision>.tar.gz`. The revision
contains a dirty marker when built from changed source. Keep that exact archive
and its adjacent SHA-256 file for the installation record. To use a release,
verify its outer checksum before extracting, then run `sha256sum -c SHA256SUMS`
inside the extracted `archiver-mcp-linux-amd64` directory. A checksum detects
accidental changes; obtain the release/checksum through a trusted channel.

The release contains `archiver-mcp`, `scripts/`, `deploy/mcp/`, `VERSION`, and
these guides. Keep that directory layout intact. Neither source builds nor this
installer run the archive writer, migrations, checkpoints, pairing, or WhatsApp.

## 3. Install alongside capture

The target must run Linux amd64 with systemd, Python 3, sudo and POSIX ACL tools.
On Debian/Ubuntu, install missing prerequisites deliberately:

```sh
sudo apt install acl sudo python3
```

Choose the existing ordinary Linux account that will run Hermes, for example
`hamza`. Do not use root or the archive service account. From the source checkout:

```sh
sudo ./scripts/install-mcp.sh \
  --binary .cache/build/archiver-mcp \
  --caller hamza \
  --archive /var/lib/whatsapp-archiver/archive.db \
  --archive-user whatsapp-archiver
```

From an extracted release, use `--binary ./archiver-mcp` instead. No command in
this guide reinstalls or restarts the existing archiver. The installer touches
only companion units and named archive ACL entries. It refuses foreign or
modified companion files, an existing unmanaged reader account, unsafe paths,
and conflicting installation settings. Review any refusal rather than deleting
unknown files to force an overwrite.

Installed files and permissions:

| Item | Purpose |
| --- | --- |
| `/usr/local/bin/archiver-mcp` | Root-owned companion binary |
| `/etc/archiver-mcp/config.yaml` | Root-owned policy, readable by the reader group |
| `/usr/local/bin/archiver-mcp-launch` | Hermes entry point, accepts no arguments |
| `/usr/local/libexec/archiver-mcp-serve` | Exact sudo-permitted command; fixed `serve` and config |
| `/etc/sudoers.d/archiver-mcp` | Only the named caller may start that command as `archiver-mcp` |
| `/var/lib/archiver-mcp/installation.json` | Root-only file hashes and prior ACLs for rollback |
| `/run/archiver-mcp` | Reader-owned 0700 bridge lock directory, recreated by tmpfiles at boot |
| `/run/archiver-mcp-admin` | Separate root-only 0700 administration lock; tmpfiles and service runtime directory |
| `archiver-mcp-acl.path` / `.service` | Repair ACLs when exact archive siblings are recreated |

The reader has no login shell or home directory. Hermes receives no direct
archive ACL. The helper grants directory traversal and file reads only on
`archive.db`, `archive.db-wal`, and `archive.db-shm`; it does not grant a directory
default ACL. It never reads database contents. No permission is granted on
`session.db`, media directories, or another database. The directory watcher
reacts to entry changes, not each WAL append, and avoids repeating unchanged ACL
writes. A missing WAL/SHM is allowed; a missing archive or unsafe sibling causes
failure. During recreation the bridge may report temporary unavailability;
there is no writable or immutable SQLite fallback.

The installed account's lack of write permission on the archive directory is
part of this protection: SQLite cannot create siblings during a writer-shutdown
race. Run the bridge as the dedicated reader, as shown below, rather than as
root or the archive owner. The header/sidecar preflight also rejects tested
stopped-WAL states, but that check alone cannot prevent every check/open race
when a manually invoked process has directory-write permission.

## 4. Check readiness locally, before Hermes

Run metadata-only inspection as the dedicated reader:

```sh
sudo -u archiver-mcp /usr/local/bin/archiver-mcp inspect \
  --config /etc/archiver-mcp/config.yaml
sudo systemctl status archiver-mcp-acl.path --no-pager
systemctl show archiver.service -p MainPID -p ExecMainStartTimestamp
```

Compare capture's PID and start time with step 1. They should be unchanged.
Inspection reports schema, capabilities and policy readiness without message
bodies. The default `selected` policy has an empty allowlist: seeing no chats or
content is a healthy installation awaiting the operator's choice.

For a protocol smoke test, log in as the **same ordinary caller** configured
above, and run this with the allowlist still empty. It initializes a local stdio
client, lists tools, and asks for status. It uses no model, API key or network:

```sh
timeout 15s python3 - <<'PY'
import json
import subprocess
p = subprocess.Popen(['/usr/local/bin/archiver-mcp-launch'],
                     stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True)
def send(value):
    p.stdin.write(json.dumps(value) + '\n')
    p.stdin.flush()
def request(i, method, params):
    send({'jsonrpc': '2.0', 'id': i, 'method': method, 'params': params})
    while True:
        line = p.stdout.readline()
        if not line:
            raise RuntimeError('MCP closed before replying')
        answer = json.loads(line)
        if answer.get('id') == i:
            if 'error' in answer:
                raise RuntimeError('MCP request failed')
            return answer['result']
try:
    request(1, 'initialize', {'protocolVersion': '2025-11-25',
        'capabilities': {}, 'clientInfo': {'name': 'local-check', 'version': '1'}})
    send({'jsonrpc': '2.0', 'method': 'notifications/initialized'})
    print([tool['name'] for tool in request(2, 'tools/list', {})['tools']])
    print(json.dumps(request(3, 'tools/call',
        {'name': 'archive_status', 'arguments': {}}), indent=2))
finally:
    p.stdin.close()
    p.wait(timeout=5)
PY
```

Expect the six archive tools plus `list_artifacts` and `get_artifact`.
Artifacts are advertised as unavailable. Status with an empty selection must
not disclose unrestricted chat names or totals. Quit other MCP clients first:
only one bridge process may hold the installation lock. The launcher rejects
`--config`, subcommands, and every other caller argument; it cannot be used for
operator `catalog`, arbitrary executables or a shell.

## 5. Choose chats yourself

The following is an **operator-only** local command. It lists chat identifiers
and names, never message text. The Hermes launcher cannot execute it:

```sh
sudo -u archiver-mcp /usr/local/bin/archiver-mcp catalog \
  --config /etc/archiver-mcp/config.yaml
sudoedit /etc/archiver-mcp/config.yaml
```

Keep the archive/runtime paths unchanged and select the exact IDs shown:

```yaml
archive: /var/lib/whatsapp-archiver/archive.db
runtime_dir: /run/archiver-mcp
policy:
  mode: selected
  allow_chats:
    - 'YOUR-CHOSEN-CHAT-ID'
  deny_chats: []
viewer_base_url: ""
```

Deny entries win across aliases. `mode: all` exposes all chats except explicit
denies and requires an intentional local edit. Missing/invalid policy denies
content. Policy reloads on each request; capture exclusions and agent access
are separate decisions. Removing access does not erase historical capture.
The human operator can now verify one chosen content query. Do not ask an
implementation agent to read live messages as a deployment test.

Use narrow date and chat scopes. Search defaults to the last 30 days; explicit
bounds use RFC3339 offsets and `from <= timestamp < to`. Legacy scans can hit the
2-second budget and return `QUERY_TOO_BROAD`. Narrow the query rather than
adding an index, changing the running archive, or assuming an empty result proves
nothing happened. Text search covers stored body/caption and supported filename
fields; it does not search the contents of attached PDFs/audio/images.

## Updates, removal, and troubleshooting

Close Hermes and other bridge clients before replacing/removing the companion;
the archiver stays running. Build/unpack a new trusted release and repeat the
same install command with the same caller/archive account/path. The installer
preserves the existing policy and original ACL rollback records. Save the prior
release/checksum if you want to reinstall it later. It refuses modifications to
managed executable/unit/sudoers files; resolve those deliberately with your
operator rather than accepting an unreviewed overwrite. A changed archive
account UID is also refused; account replacement requires an explicit reviewed
migration, not an automatic ownership guess.

If installation is interrupted, keep the trusted source/release directory and
root-only manifest. Correct the reported OS prerequisite/permission problem,
then rerun the **same install command** to resume. The manifest records lifecycle
phase and before/after hashes before each managed write. A write completed just
before interruption is recognized; unknown or changed file contents are refused.
The existing selection policy is preserved. To abandon even an early partial
install, run `sudo ./scripts/uninstall-mcp.sh` from that trusted directory; it
uses the source helper even if the installed helper was not yet created.
Do not delete the manifest or reader account by hand to force a retry.

To remove, first delete or disable the Hermes `whatsapp_archive` configuration,
exit its sessions, then run from the source/release directory:

```sh
sudo ./scripts/uninstall-mcp.sh
systemctl show archiver.service -p MainPID -p ExecMainStartTimestamp
```

Removal refuses an active reader before teardown. Close Hermes and retry the
same uninstall command if it reports an active process. It then revokes the
exact sudoers capability, stops only installed companion units,
restores recorded ACLs only on matching file identities, removes companion
executables/units and its reader account, and preserves the archive/session.
Cleanup records each deletion and can be resumed with the same uninstall
command after an OS failure. The installed Python helpers stay present until
the reader account has been removed; Ubuntu's automatic private-group removal
is supported. Installation is refused while removal is unfinished: finish
`sudo ./scripts/uninstall-mcp.sh` first. The operator policy, when created,
remains root-only at `/etc/archiver-mcp/config.yaml` for
reference. Move it aside deliberately before a fresh installation; an orphaned
config is treated as a conflicting installation. No recursive ACL reset or data
delete is needed. Unexpected runtime files are retained for manual review.

| Symptom | Check |
| --- | --- |
| No permitted chats/content | Empty allowlist is the default; run operator catalog and edit policy locally |
| `ARCHIVE_UNAVAILABLE` | Check exact archive/WAL/SHM ownership and ACLs; helper service errors; retry after recreation |
| `UNSUPPORTED_SCHEMA` | Stop and inspect the schema fingerprint; do not migrate a live legacy archive for the bridge |
| `BUSY` | Quit the other local MCP client; the runtime lock allows one bridge |
| Launcher needs a password/permission denied | Caller must match installation; validate `/etc/sudoers.d/archiver-mcp` with `sudo visudo -cf` |
| Reader cannot create a lock after reboot | Check `systemd-tmpfiles --create /etc/tmpfiles.d/archiver-mcp.conf` and runtime mode/owner |
| Helper refuses a symlink or owner change | Investigate locally; do not widen directory defaults or access the session |
| `QUERY_TOO_BROAD` | Restrict chat/date window; broad legacy scans have bounded latency |

Do not paste unredacted catalog output, chats, configuration secrets, or raw
SQLite errors into an issue. The bridge emits redacted diagnostics to stderr;
stdout in `serve` belongs to MCP only. The host ACL/systemd commands still need
an operator smoke test on the intended Linux installation; sandbox tests do not
claim to prove a particular server's configuration or live WAL behavior.
