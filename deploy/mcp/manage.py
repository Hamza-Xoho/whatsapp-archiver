#!/usr/bin/python3
"""Additive Linux companion administration; no archive contents are opened.

Archive metadata/ACL operations use pinned no-follow file descriptors. The
injectable OS boundary allows sandbox tests without changing host accounts,
ACLs or services. The command line always uses / and the real OS boundary.
"""
import argparse
from contextlib import contextmanager, ExitStack
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import platform
import re
import secrets
import stat
import sys
import tempfile

READER = 'archiver-mcp'
STATE = '/var/lib/archiver-mcp/installation.json'
CONFIG = '/etc/archiver-mcp/config.yaml'
BIN = '/usr/local/bin/archiver-mcp'
NAME = re.compile(r'[a-z_][a-z0-9_-]{0,30}\Z')
ARCHIVE = re.compile(r'/(?:var/lib|srv)/[A-Za-z0-9_./-]+/archive\.db\Z')


# -I omits the script directory; only this trusted adjacent module is loaded.
sys.path.insert(0, str(Path(__file__).resolve().parent))
from mcp_host import HostOps, InstallError, admin_lock


def identity(fd):
    st = os.fstat(fd)
    return [st.st_dev, st.st_ino]


def permissions(bits):
    return ''.join(char if bits & flag else '-' for char, flag in [('r', 4), ('w', 2), ('x', 1)])


def bitset(text):
    return (4 if 'r' in text else 0) | (2 if 'w' in text else 0) | (1 if 'x' in text else 0)


def acl_with_reader(original, uid, desired):
    """Preserve others' effective permissions when widening the ACL mask."""
    entries = [line.split('#', 1)[0].strip() for line in original.splitlines()]
    entries = [line for line in entries if line]
    old_mask = next((bitset(line.split(':')[2]) for line in entries if line.startswith('mask::')), 7)
    own = f'user:{uid}:'
    current = next((line for line in entries if line.startswith(own)), '')
    if current == own + desired and old_mask & bitset(desired) == bitset(desired):
        return original
    result = []
    for line in entries:
        if line.startswith(own) or line.startswith('mask::'):
            continue
        parts = line.split(':')
        # Do not touch owner, other, or default ACLs. Freeze masked entries at
        # their previous effective permissions before extending the mask.
        if len(parts) == 3 and (parts[0] == 'group' or (parts[0] == 'user' and parts[1])):
            parts[2] = permissions(bitset(parts[2]) & old_mask)
            line = ':'.join(parts)
        result.append(line)
    result.extend([own + desired, 'mask::' + permissions(old_mask | bitset(desired))])
    return '\n'.join(result) + '\n'


class Installation:
    def __init__(self, root, ops, templates):
        self.root = Path(root)
        self.ops = ops
        self.templates = Path(templates)
        self.state = None

    def path(self, path):
        return self.root / path.lstrip('/')

    def secure_tree(self, path, leaf=False):
        """Refuse links/non-root writable privileged installation paths."""
        target = self.path(path)
        current = self.root
        for part in PurePosixPath(path).parts[1:]:
            current = current / part
            try:
                st = current.lstat()
            except FileNotFoundError:
                continue
            if stat.S_ISLNK(st.st_mode):
                raise InstallError('symlink in installation path')
            if st.st_uid != (0 if self.root == Path('/') else os.getuid()) or st.st_mode & 0o022:
                raise InstallError('installation path must be root owned and not writable by group/other')
            if current != target and not stat.S_ISDIR(st.st_mode):
                raise InstallError('non-directory installation parent')
            if current == target and leaf and not stat.S_ISREG(st.st_mode):
                raise InstallError('installation target must be a regular file')

    def read_state(self):
        self.secure_tree(STATE, leaf=True)
        try:
            value = json.loads(self.path(STATE).read_text())
        except FileNotFoundError:
            return None
        except (ValueError, OSError) as exc:
            raise InstallError('invalid installation state; manual recovery required') from exc
        if value.get('format') != 1:
            raise InstallError('unknown installation state version')
        value.setdefault('phase', 'ready')
        value.setdefault('config', True)
        return value

    def write(self, path, data, mode=0o644, gid=0):
        self.secure_tree(path, leaf=True)
        dest = self.path(path)
        dest.parent.mkdir(parents=True, exist_ok=True, mode=0o755)
        self.secure_tree(str(PurePosixPath(path).parent))
        fd, temp = tempfile.mkstemp(prefix='.mcp-', dir=dest.parent)
        try:
            with os.fdopen(fd, 'wb') as out:
                out.write(data)
                out.flush()
                os.fsync(out.fileno())
            self.ops.own(temp, 0, gid, mode)
            os.replace(temp, dest)
        finally:
            if os.path.exists(temp):
                os.unlink(temp)

    def save(self):
        self.write(STATE, (json.dumps(self.state, sort_keys=True, indent=2) + '\n').encode(), 0o600)

    @contextmanager
    def archive_handles(self, archive, archive_uid, require_database=True):
        """Hold every directory/file descriptor through ACL operations."""
        with ExitStack() as stack:
            fd = os.open(self.root, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
            stack.callback(os.close, fd)
            handles = []
            parts = PurePosixPath(archive).parts[1:]
            for index, part in enumerate(parts[:-1]):
                try:
                    fd = os.open(part, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=fd)
                except OSError as exc:
                    raise InstallError('archive parent unavailable or unsafe') from exc
                stack.callback(os.close, fd)
                st = os.fstat(fd)
                if st.st_uid not in (0, archive_uid) or st.st_mode & 0o022:
                    raise InstallError('archive parents require trusted ownership and no group/other writes')
                handles.append(('/' + '/'.join(parts[:index + 1]), fd, '--x'))
            for suffix in ('', '-wal', '-shm'):
                try:
                    filefd = os.open(parts[-1] + suffix, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=fd)
                except FileNotFoundError as exc:
                    if not suffix and require_database:
                        raise InstallError('archive file is missing; no file will be created') from exc
                    continue
                except OSError as exc:
                    raise InstallError('unsafe archive sibling') from exc
                stack.callback(os.close, filefd)
                st = os.fstat(filefd)
                if not stat.S_ISREG(st.st_mode) or st.st_nlink != 1 or st.st_uid != archive_uid:
                    raise InstallError('archive siblings must be singly linked regular files owned by archive user')
                handles.append((archive + suffix, filefd, 'r--'))
            yield handles

    def check_managed(self):
        for path, digest in self.state['files'].items():
            self.secure_tree(path, leaf=True)
            try:
                actual = hashlib.sha256(self.path(path).read_bytes()).hexdigest()
            except OSError as exc:
                raise InstallError('managed file missing; manual recovery required') from exc
            if actual != digest:
                raise InstallError('managed file changed; refusing overwrite or removal')
        self.secure_tree(CONFIG, leaf=True)
        if self.state['config'] and not self.path(CONFIG).is_file():
            raise InstallError('configuration missing; restore it before upgrading')

    def resolve_pending(self):
        pending = self.state.get('pending')
        if not pending:
            return
        path = pending['path']
        self.secure_tree(path, leaf=True)
        actual = hashlib.sha256(self.path(path).read_bytes()).hexdigest() if self.path(path).exists() else None
        if pending['action'] == 'write':
            if actual == pending['new']:
                if path == CONFIG:
                    self.state['config'] = True
                else:
                    self.state['files'][path] = actual
            elif actual != pending['old']:
                raise InstallError('pending installation file has foreign contents; refusing recovery')
        elif actual is None:
            self.state['files'].pop(path, None)
        elif actual != self.state['files'][path]:
            raise InstallError('pending removal file changed; refusing recovery')
        self.state.pop('pending')
        self.save()

    def managed_write(self, path, data, mode, gid=0):
        old = self.state['files'].get(path)
        if old is None and self.path(path).exists():
            raise InstallError('unknown file appeared during installation; refusing overwrite')
        self.state['pending'] = {'action': 'write', 'path': path, 'old': old,
            'new': hashlib.sha256(data).hexdigest()}
        self.save()
        self.write(path, data, mode, gid)
        self.resolve_pending()

    def managed_remove(self, path):
        if path not in self.state['files']:
            return
        self.state['pending'] = {'action': 'delete', 'path': path}
        self.save()
        self.path(path).unlink()
        self.resolve_pending()

    def grant(self, handles):
        uid = self.state['reader'][0]
        for path, fd, wanted in handles:
            original = self.ops.get_acl(fd)
            # No need to add directory ACLs where "other" already traverses
            # and no named reader ACL overrides it.
            if wanted == '--x' and not any(line.startswith(f'user:{uid}:') for line in original.splitlines()):
                other = next((line for line in original.splitlines() if line.startswith('other::')), '')
                if 'x' in other:
                    continue
            changed = acl_with_reader(original, uid, wanted)
            if changed == original:
                continue
            previous = self.state['acls'].get(path)
            if not previous or previous['identity'] != identity(fd):
                self.state['acls'][path] = {'identity': identity(fd), 'before': original}
                self.save()  # Persist rollback before granting access.
            self.ops.set_acl(fd, changed)

    def install(self, binary, caller, archive='/var/lib/whatsapp-archiver/archive.db', archive_user='whatsapp-archiver'):
        if not NAME.fullmatch(caller) or caller in ('root', READER) or not self.ops.account(caller):
            raise InstallError('caller must be an existing ordinary Linux user')
        if self.ops.account(caller)[0] == 0:
            raise InstallError('caller must not have root uid')
        if not NAME.fullmatch(archive_user) or not self.ops.account(archive_user) or archive_user in ('root', READER):
            raise InstallError('archive user must be an existing dedicated account')
        if not ARCHIVE.fullmatch(archive) or any(part in ('.', '..', '') for part in archive.split('/')[1:]) or str(PurePosixPath(archive)) != archive:
            raise InstallError('archive must be a plain absolute /var/lib/.../archive.db or /srv/.../archive.db path')
        archive_uid = self.ops.account(archive_user)[0]
        binary = Path(binary)
        if binary.is_symlink() or not binary.is_file():
            raise InstallError('binary must be a regular file')
        binary_data = binary.read_bytes()
        if binary_data[:4] != b'\x7fELF' or binary_data[18:20] != b'\x3e\x00':
            raise InstallError('binary must be Linux amd64 ELF')
        archive_dir = str(PurePosixPath(archive).parent)
        substitutions = {'@ARCHIVE@': archive, '@ARCHIVE_DIR@': archive_dir}
        files = {BIN: (binary_data, 0o755)}
        sources = {
            '/usr/local/bin/archiver-mcp-launch': 'archiver-mcp-launch',
            '/usr/local/libexec/archiver-mcp-serve': 'archiver-mcp-serve',
            '/usr/local/libexec/archiver-mcp/manage.py': 'manage.py',
            '/usr/local/libexec/archiver-mcp/mcp_host.py': 'mcp_host.py',
            '/etc/systemd/system/archiver-mcp-acl.service': 'archiver-mcp-acl.service',
            '/etc/systemd/system/archiver-mcp-acl.path': 'archiver-mcp-acl.path',
            '/etc/tmpfiles.d/archiver-mcp.conf': 'archiver-mcp.tmpfiles',
        }
        for dest, source in sources.items():
            text = (self.templates / source).read_text()
            for old, new in substitutions.items():
                text = text.replace(old, new)
            files[dest] = (text.encode(), 0o755 if source in ('archiver-mcp-launch', 'archiver-mcp-serve') else 0o644)
        sudoers = f'{caller} ALL=({READER}) NOPASSWD: /usr/local/libexec/archiver-mcp-serve ""\n'
        files['/etc/sudoers.d/archiver-mcp'] = (sudoers.encode(), 0o440)
        for dest in (*files, CONFIG, STATE):
            self.secure_tree(dest, leaf=True)
        self.state = self.read_state()
        if self.state:
            if self.state['phase'] == 'removing':
                raise InstallError('removal is in progress; resume uninstall before installing')
            if any(self.state[key] != value for key, value in [('archive', archive), ('caller', caller), ('archive_user', archive_user)]):
                raise InstallError('installation settings conflict; remove existing installation first')
            if self.state['archive_uid'] != archive_uid:
                raise InstallError('archive account uid changed; refusing upgrade')
            reader = self.ops.account(READER)
            if self.state['reader'] is not None and list(reader or ()) != self.state['reader']:
                raise InstallError('managed reader identity changed')
            self.resolve_pending()
            self.check_managed()
        elif self.ops.account(READER) or any(self.path(path).exists() for path in (*files, CONFIG)):
            raise InstallError('unknown/conflicting installation; nothing was overwritten')
        with self.archive_handles(archive, archive_uid) as handles:
            if not self.state:
                self.state = {'format': 1, 'phase': 'installing', 'archive': archive,
                    'archive_user': archive_user, 'archive_uid': archive_uid, 'caller': caller,
                    'marker': 'archiver-mcp-' + secrets.token_hex(16), 'reader': None,
                    'files': {}, 'acls': {}, 'config': False}
                self.path('/var/lib/archiver-mcp').mkdir(mode=0o700, parents=True, exist_ok=True)
                self.ops.own(self.path('/var/lib/archiver-mcp'), 0, 0, 0o700)
                self.save()
            if self.state['reader'] is None:
                reader = self.ops.account(READER)
                if reader and self.ops.reader_marker() != self.state['marker']:
                    raise InstallError('reader account is not owned by this interrupted installation')
                self.state['reader'] = list(reader or self.ops.add_reader(self.state['marker']))
                self.save()
            self.state['phase'] = 'installing'
            self.save()
            if not self.state['config']:
                if self.path(CONFIG).exists():
                    raise InstallError('unknown configuration appeared during installation')
                text = (self.templates / 'config.yaml').read_text().replace('@ARCHIVE@', archive)
                self.managed_write(CONFIG, text.encode(), 0o640, self.state['reader'][1])
            for dest, (data, mode) in files.items():
                if dest == '/etc/sudoers.d/archiver-mcp':
                    candidate = self.path('/var/lib/archiver-mcp/sudoers-candidate')
                    self.write('/var/lib/archiver-mcp/sudoers-candidate', data, 0o600)
                    self.ops.validate_sudoers(candidate)
                    candidate.unlink()
                self.managed_write(dest, data, mode)
            self.grant(handles)
        self.ops.tmpfiles()
        self.ops.systemctl('daemon-reload')
        self.ops.systemctl('enable', '--now', 'archiver-mcp-acl.path')
        self.state['phase'] = 'ready'
        self.save()

    def refresh(self):
        self.state = self.read_state()
        if not self.state or self.state['phase'] != 'ready':
            raise InstallError('no ready managed installation')
        with self.archive_handles(self.state['archive'], self.state['archive_uid']) as handles:
            self.grant(handles)

    def uninstall(self):
        self.state = self.read_state()
        if not self.state:
            raise InstallError('no managed installation')
        reader = self.ops.account(READER)
        recorded = self.state['reader']
        if recorded is None and reader:
            if self.ops.reader_marker() != self.state['marker']:
                raise InstallError('reader identity is not owned by this installation')
            recorded = list(reader)
        if reader and list(reader) != recorded:
            raise InstallError('reader identity changed; refusing removal')
        if recorded and self.ops.reader_active(recorded[0]):
            raise InstallError('reader is active; close Hermes and retry uninstall')
        self.resolve_pending()
        self.check_managed()
        self.state['reader'] = recorded
        self.state['phase'] = 'removing'
        self.save()
        self.managed_remove('/etc/sudoers.d/archiver-mcp')
        # Recheck after revoking launches to close the preflight/start race.
        if recorded and self.ops.reader_active(recorded[0]):
            raise InstallError('reader became active; close Hermes and resume uninstall')
        if '/etc/systemd/system/archiver-mcp-acl.path' in self.state['files']:
            self.ops.systemctl('disable', '--now', 'archiver-mcp-acl.path')
        if '/etc/systemd/system/archiver-mcp-acl.service' in self.state['files']:
            self.ops.systemctl('stop', 'archiver-mcp-acl.service')
        with self.archive_handles(self.state['archive'], self.state['archive_uid'], False) as handles:
            for path, fd, _ in handles:
                prior = self.state['acls'].get(path)
                if prior and not prior.get('restored') and prior['identity'] == identity(fd):
                    self.ops.set_acl(fd, prior['before'])
                    prior['restored'] = True
                    self.save()
        # Keep both installed Python files until account removal succeeds.
        helpers = [path for path in self.state['files'] if path.endswith('.py')]
        for path in list(self.state['files']):
            if path not in helpers:
                self.managed_remove(path)
        self.ops.systemctl('daemon-reload')
        if self.state['config']:
            self.ops.own(self.path(CONFIG), 0, 0, 0o600)
        if recorded:
            self.ops.remove_reader(recorded)
        for path in helpers:
            self.managed_remove(path)
        self.path(STATE).unlink()
        runtime = self.path('/run/archiver-mcp')
        if runtime.exists() and not runtime.is_symlink():
            try:
                runtime.rmdir()
            except OSError:
                pass  # Unexpected files remain for deliberate operator review.


def main():
    parser = argparse.ArgumentParser(description='Install the separate read-only Linux archive MCP companion')
    sub = parser.add_subparsers(dest='command', required=True)
    install = sub.add_parser('install')
    install.add_argument('--binary', required=True)
    install.add_argument('--caller', required=True, help='ordinary Hermes Linux username; never root')
    install.add_argument('--archive', default='/var/lib/whatsapp-archiver/archive.db')
    install.add_argument('--archive-user', default='whatsapp-archiver')
    sub.add_parser('refresh')
    sub.add_parser('uninstall')
    args = parser.parse_args()
    try:
        if sys.platform != 'linux' or platform.machine() != 'x86_64' or os.geteuid() != 0:
            raise InstallError('requires root on Linux amd64')
        for program in ('/usr/bin/getfacl', '/usr/bin/setfacl', '/usr/sbin/useradd',
                '/usr/sbin/visudo', '/usr/bin/sudo', '/usr/bin/systemctl', '/usr/bin/systemd-tmpfiles'):
            if not os.access(program, os.X_OK):
                raise InstallError('missing system prerequisite; install acl, sudo, passwd and systemd')
        with admin_lock():
            instance = Installation(Path('/'), HostOps(), Path(__file__).resolve().parent)
            if args.command == 'install':
                instance.install(args.binary, args.caller, args.archive, args.archive_user)
            else:
                getattr(instance, args.command)()
        print('archiver-mcp: ' + args.command + ' complete', file=sys.stderr)
        return 0
    except (InstallError, OSError) as exc:
        # Raw OS errors may carry paths; intentionally use stable diagnostics.
        message = str(exc) if isinstance(exc, InstallError) else 'filesystem operation failed; inspect installation permissions locally'
        print('archiver-mcp: ' + message, file=sys.stderr)
        return 1


if __name__ == '__main__':
    sys.exit(main())
