"""Installer contract tests. Host users, ACLs and services are never modified.

Removing validation, widening ACL scope, accepting caller argv, resetting an
operator policy or blindly overwriting an installation must break these tests.
"""
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

REPO = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location('mcp_manage', REPO / 'deploy/mcp/manage.py')
m = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(m)


class SandboxOps:
    """Only the OS administration boundary is replaced; real files/fds remain."""
    def __init__(self):
        self.accounts = {'operator': (1000, 1000), 'whatsapp-archiver': (2000, 2000)}
        self.acls = {}
        self.changes = []
        self.services = []
        self.created = []
        self.removed = []
        self.active = False
        self.marker = None

    def account(self, name):
        return self.accounts.get(name)

    def add_reader(self, marker=None):
        self.marker = marker
        self.accounts['archiver-mcp'] = (2100, 2100)
        self.created.append('archiver-mcp')
        return (2100, 2100)

    def remove_reader(self, expected=None):
        self.removed.append('archiver-mcp')
        self.accounts.pop('archiver-mcp', None)

    def reader_active(self, uid):
        return self.active

    def reader_marker(self):
        return self.marker

    def get_acl(self, fd):
        st = os.fstat(fd)
        return self.acls.get((st.st_dev, st.st_ino), 'user::rw-\ngroup::---\nother::---\n')

    def set_acl(self, fd, acl):
        st = os.fstat(fd)
        self.acls[(st.st_dev, st.st_ino)] = acl
        self.changes.append((os.readlink(f'/proc/self/fd/{fd}'), acl))

    def own(self, path, uid, gid, mode):
        os.chmod(path, mode)

    def validate_sudoers(self, path):
        pass

    def systemctl(self, *args):
        self.services.append(args)

    def tmpfiles(self):
        pass


class InstallTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(dir=REPO / '.cache/tmp')
        self.root = Path(self.temp.name)
        self.ops = SandboxOps()
        # Archive ownership is the current uid only in this isolated fixture.
        self.ops.accounts['whatsapp-archiver'] = (os.getuid(), os.getgid())
        self.archive = self.root / 'var/lib/whatsapp-archiver/archive.db'
        self.archive.parent.mkdir(parents=True, mode=0o700)
        self.archive.write_bytes(b'synthetic only')
        self.session = self.archive.with_name('session.db')
        self.session.write_bytes(b'forbidden fixture')
        self.binary = self.root / 'source-binary'
        self.binary.write_bytes(b'\x7fELF' + b'\0' * 12 + b'\x02\x00\x3e\x00' + b'fixture')
        self.binary.chmod(0o755)
        self.installer = m.Installation(self.root, self.ops, REPO / 'deploy/mcp')

    def tearDown(self):
        self.temp.cleanup()

    def install(self, **kw):
        return self.installer.install(binary=self.binary, caller='operator',
            archive=kw.pop('archive', '/var/lib/whatsapp-archiver/archive.db'),
            archive_user='whatsapp-archiver', **kw)

    def test_install_only_exact_archive_files_and_own_service(self):
        wal = self.archive.with_name('archive.db-wal')
        wal.write_bytes(b'wal')
        self.install()
        paths = {p for p, _ in self.ops.changes}
        self.assertIn(str(self.archive), paths)
        self.assertIn(str(wal), paths)
        self.assertNotIn(str(self.session), paths)
        self.assertFalse(any('default:' in acl for _, acl in self.ops.changes))
        self.assertEqual(self.session.read_bytes(), b'forbidden fixture')
        self.assertTrue(all('whatsapp-archiver.service' not in a for a in self.ops.services))
        self.assertIn(('enable', '--now', 'archiver-mcp-acl.path'), self.ops.services)
        policy = (self.root / 'etc/archiver-mcp/config.yaml').read_text()
        self.assertIn('allow_chats: []', policy)

    def test_rejects_bad_users_and_paths_before_effects(self):
        for name in ('-root', 'operator ALL', 'operator\nroot', 'root', 'missing'):
            with self.subTest(name=name), self.assertRaises(m.InstallError):
                self.installer.install(self.binary, name, '/var/lib/whatsapp-archiver/archive.db', 'whatsapp-archiver')
        for path in ('relative.db', '/var/lib/a/../archive.db', '/tmp/archive.db', '/var/lib/a;touch/archive.db', '/var/lib/a/session.db'):
            with self.subTest(path=path), self.assertRaises(m.InstallError):
                self.install(archive=path)
        self.assertEqual(self.ops.created, [])
        self.assertEqual(self.ops.changes, [])

    def test_missing_archive_symlink_and_wrong_owner_fail_closed(self):
        self.archive.unlink()
        with self.assertRaises(m.InstallError):
            self.install()
        self.archive.symlink_to(self.session)
        with self.assertRaises(m.InstallError):
            self.install()
        self.archive.unlink()
        self.archive.write_bytes(b'fixture')
        self.ops.accounts['whatsapp-archiver'] = (99999, 99999)
        with self.assertRaises(m.InstallError):
            self.install()
        self.assertEqual(self.ops.created, [])

    def test_unknown_installation_and_managed_file_tamper_refused(self):
        target = self.root / 'usr/local/bin/archiver-mcp'
        target.parent.mkdir(parents=True)
        target.write_text('foreign binary')
        with self.assertRaises(m.InstallError):
            self.install()
        self.assertEqual(target.read_text(), 'foreign binary')
        target.unlink()
        self.install()
        launcher = self.root / 'usr/local/bin/archiver-mcp-launch'
        launcher.write_text('tampered')
        with self.assertRaises(m.InstallError):
            self.install()
        self.assertEqual(launcher.read_text(), 'tampered')

    def test_upgrade_preserves_policy_and_rollback_restores_prior_acl(self):
        with self.archive.open('rb') as file:
            self.ops.set_acl(file.fileno(), 'user::rw-\nuser:777:r--\ngroup::---\nmask::r--\nother::---\n')
            before = self.ops.get_acl(file.fileno())
        self.install()
        config = self.root / 'etc/archiver-mcp/config.yaml'
        policy = config.read_text().replace('allow_chats: []', 'allow_chats: [chosen]')
        config.write_text(policy)
        self.install()
        self.assertEqual(config.read_text(), policy)
        self.installer.uninstall()
        with self.archive.open('rb') as file:
            self.assertEqual(self.ops.get_acl(file.fileno()), before)
        self.assertTrue(config.exists(), 'rollback keeps operator policy')
        self.assertTrue(self.archive.exists())
        self.assertTrue(self.session.exists())
        self.assertEqual(self.ops.removed, ['archiver-mcp'])

    def test_helper_recreation_exact_scope_idempotence_and_symlink_rejection(self):
        self.install()
        count = len(self.ops.changes)
        self.installer.refresh()
        self.assertEqual(len(self.ops.changes), count, 'ACL helper must not trigger itself')
        shm = self.archive.with_name('archive.db-shm')
        shm.write_bytes(b'shm')
        self.installer.refresh()
        self.assertIn(str(shm), [p for p, _ in self.ops.changes])
        shm.unlink()
        shm.symlink_to(self.session)
        with self.assertRaises(m.InstallError):
            self.installer.refresh()
        self.assertNotIn(str(self.session), [p for p, _ in self.ops.changes])

    def test_archive_parent_symlink_and_hardlink_and_writable_parent_rejected(self):
        self.archive.unlink()
        self.archive.hardlink_to(self.session)
        with self.assertRaises(m.InstallError):
            self.install()
        self.archive.unlink()
        self.archive.write_bytes(b'data')
        self.archive.parent.chmod(0o777)
        with self.assertRaises(m.InstallError):
            self.install()
        self.archive.parent.chmod(0o700)
        moved = self.archive.parent.with_name('moved')
        self.archive.parent.rename(moved)
        self.archive.parent.symlink_to(moved, target_is_directory=True)
        with self.assertRaises(m.InstallError):
            self.install()
        self.assertEqual(self.ops.created, [])

    def test_rollback_does_not_reset_replaced_file_permissions(self):
        self.install()
        old = self.archive.with_name('retired.db')
        self.archive.rename(old)
        self.archive.write_bytes(b'new archive')
        with self.archive.open('rb') as file:
            replacement_acl = 'user::rw-\nuser:888:r--\ngroup::---\nmask::r--\nother::---\n'
            self.ops.set_acl(file.fileno(), replacement_acl)
        self.installer.uninstall()
        with self.archive.open('rb') as file:
            self.assertEqual(self.ops.get_acl(file.fileno()), replacement_acl)

    def test_launcher_and_privileged_serve_reject_any_arguments(self):
        self.install()
        for name in ('usr/local/bin/archiver-mcp-launch', 'usr/local/libexec/archiver-mcp-serve'):
            result = subprocess.run(['/bin/sh', str(self.root / name), '--config', '/tmp/evil'],
                capture_output=True, env={'PATH': '/nonexistent', 'ENV': '/tmp/evil'})
            self.assertNotEqual(result.returncode, 0)
            self.assertNotIn(b'not found', result.stderr, 'must reject before sudo or binary exec')

    def test_unknown_account_and_symlinked_install_target_rejected(self):
        self.ops.accounts['archiver-mcp'] = (2100, 2100)
        with self.assertRaises(m.InstallError):
            self.install()
        self.ops.accounts.pop('archiver-mcp')
        path = self.root / 'etc/archiver-mcp'
        path.parent.mkdir(parents=True, exist_ok=True)
        path.symlink_to(self.archive.parent, target_is_directory=True)
        with self.assertRaises(m.InstallError):
            self.install()
        self.assertEqual(self.ops.created, [])


if __name__ == '__main__':
    unittest.main()
