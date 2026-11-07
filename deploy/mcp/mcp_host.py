"""Linux OS administration boundary; never reads archive/session content."""
from contextlib import contextmanager
import fcntl
import grp
import os
from pathlib import Path
import pwd
import stat
import subprocess

READER = 'archiver-mcp'
ADMIN_DIR = '/run/archiver-mcp-admin'


class InstallError(Exception):
    """Safe operator error without raw subprocess or archive diagnostics."""


@contextmanager
def admin_lock(root=Path('/')):
    """Shared root-only lock, writable under the shipped systemd sandbox."""
    directory = Path(root) / ADMIN_DIR.lstrip('/')
    directory.parent.mkdir(mode=0o755, parents=True, exist_ok=True)
    for parent in (directory.parent, directory):
        if parent == directory:
            parent.mkdir(mode=0o700, exist_ok=True)
        st = parent.lstat()
        expected_uid = 0 if Path(root) == Path('/') else os.getuid()
        if not stat.S_ISDIR(st.st_mode) or st.st_uid != expected_uid or st.st_mode & 0o022:
            raise InstallError('unsafe administration runtime directory')
    if directory.stat().st_mode & 0o077:
        raise InstallError('administration runtime must be private to root')
    directory_fd = os.open(directory, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        fd = os.open('lock', os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600, dir_fd=directory_fd)
    finally:
        os.close(directory_fd)
    with os.fdopen(fd, 'r+') as lock:
        st = os.fstat(lock.fileno())
        if not stat.S_ISREG(st.st_mode) or st.st_uid != expected_uid or st.st_nlink != 1 or st.st_mode & 0o077:
            raise InstallError('unsafe administration lock')
        fcntl.flock(lock, fcntl.LOCK_EX)
        yield lock


class HostOps:
    def run(self, args, **kwargs):
        try:
            return subprocess.run(args, check=True, stdout=subprocess.PIPE,
                stderr=subprocess.PIPE, env={'PATH': '/usr/sbin:/usr/bin:/sbin:/bin', 'LC_ALL': 'C'}, **kwargs)
        except (subprocess.CalledProcessError, OSError) as exc:
            raise InstallError('OS administration command failed; check prerequisites and permissions') from exc

    def account(self, name):
        try:
            account = pwd.getpwnam(name)
            return account.pw_uid, account.pw_gid
        except KeyError:
            return None

    def group(self, name):
        try:
            return grp.getgrnam(name).gr_gid
        except KeyError:
            return None

    def reader_marker(self):
        try:
            return pwd.getpwnam(READER).pw_gecos
        except KeyError:
            return None

    def add_reader(self, marker):
        self.run(['/usr/sbin/useradd', '--system', '--user-group', '--no-create-home',
            '--home-dir', '/nonexistent', '--shell', '/usr/sbin/nologin', '--comment', marker, READER])
        return self.account(READER)

    def reader_active(self, uid):
        for directory in Path('/proc').iterdir():
            if not directory.name.isdecimal():
                continue
            try:
                for line in (directory / 'status').read_text().splitlines():
                    if line.startswith('Uid:') and uid in map(int, line.split()[1:]):
                        return True
            except (FileNotFoundError, ProcessLookupError):
                continue
            except OSError as exc:
                raise InstallError('cannot establish whether reader processes are active') from exc
        return False

    def remove_reader(self, expected=None):
        current = self.account(READER)
        expected = tuple(expected) if expected is not None else current
        if current is not None:
            if current != expected:
                raise InstallError('reader account identity changed; refusing removal')
            self.run(['/usr/sbin/userdel', READER])
        remaining_group = self.group(READER)
        if remaining_group is not None:
            if expected is None or remaining_group != expected[1]:
                raise InstallError('reader group identity changed; refusing removal')
            self.run(['/usr/sbin/groupdel', READER])

    def get_acl(self, fd):
        return self.run(['/usr/bin/getfacl', '-cpn', f'/proc/self/fd/{fd}'], pass_fds=(fd,)).stdout.decode()

    def set_acl(self, fd, acl):
        self.run(['/usr/bin/setfacl', '--set-file=-', f'/proc/self/fd/{fd}'],
            input=acl.encode(), pass_fds=(fd,))

    def own(self, path, uid, gid, mode):
        os.chown(path, uid, gid, follow_symlinks=False)
        os.chmod(path, mode, follow_symlinks=False)

    def validate_sudoers(self, path):
        self.run(['/usr/sbin/visudo', '-cf', str(path)])

    def systemctl(self, *args):
        self.run(['/usr/bin/systemctl', *args])

    def tmpfiles(self):
        self.run(['/usr/bin/systemd-tmpfiles', '--create', '/etc/tmpfiles.d/archiver-mcp.conf'])
