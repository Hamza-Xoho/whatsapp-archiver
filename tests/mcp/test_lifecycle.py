"""Failure recovery crosses real file writes; only OS admin is sandboxed."""
import configparser
import json
import os
from pathlib import Path
import unittest
from unittest.mock import patch

import test_install as fixture

m = fixture.m


class LifecycleTests(unittest.TestCase):
    setUp = fixture.InstallTests.setUp
    tearDown = fixture.InstallTests.tearDown
    install = fixture.InstallTests.install

    def fail_write(self, target, after=False):
        original = self.installer.write
        def write(path, *args, **kwargs):
            if path == target:
                if after:
                    original(path, *args, **kwargs)
                raise OSError('synthetic interruption')
            return original(path, *args, **kwargs)
        return patch.object(self.installer, 'write', side_effect=write)

    def assert_clean(self):
        self.assertNotIn('archiver-mcp', self.ops.accounts)
        self.assertFalse((self.root / 'var/lib/archiver-mcp/installation.json').exists())
        self.assertFalse((self.root / 'usr/local/bin/archiver-mcp').exists())
        self.assertTrue(self.archive.exists())
        self.assertTrue(self.session.exists())

    def test_interrupted_config_creation_is_retryable(self):
        with self.fail_write('/etc/archiver-mcp/config.yaml'), self.assertRaises(OSError):
            self.install()
        self.install()
        self.assertEqual(self.ops.created, ['archiver-mcp'])
        self.assertIn('allow_chats: []', (self.root / 'etc/archiver-mcp/config.yaml').read_text())
        self.installer.uninstall()
        self.assert_clean()

    def test_interrupted_config_creation_is_removable_without_installed_helper(self):
        with self.fail_write('/etc/archiver-mcp/config.yaml'), self.assertRaises(OSError):
            self.install()
        self.installer.uninstall()
        self.assert_clean()

    def test_interrupted_reader_creation_resumes_only_owned_identity(self):
        original = self.ops.add_reader
        def add(*args):
            original(*args)
            raise m.InstallError('synthetic interruption after useradd')
        with patch.object(self.ops, 'add_reader', side_effect=add), self.assertRaises(m.InstallError):
            self.install()
        self.install()
        self.assertEqual(self.ops.created, ['archiver-mcp'])
        self.installer.uninstall()
        self.assert_clean()

    def test_install_cannot_resume_an_uninstall_in_progress(self):
        self.install()
        with patch.object(self.ops, 'remove_reader', side_effect=m.InstallError('synthetic busy')), self.assertRaises(m.InstallError):
            self.installer.uninstall()
        with self.assertRaises(m.InstallError):
            self.install()
        self.installer.uninstall()
        self.assert_clean()

    def test_write_finished_before_manifest_commit_resumes(self):
        with self.fail_write('/usr/local/bin/archiver-mcp-launch', after=True), self.assertRaises(OSError):
            self.install()
        self.install()
        self.installer.uninstall()
        self.assert_clean()

    def test_recovery_refuses_foreign_pending_file(self):
        with self.fail_write('/usr/local/bin/archiver-mcp-launch'), self.assertRaises(OSError):
            self.install()
        launcher = self.root / 'usr/local/bin/archiver-mcp-launch'
        launcher.write_text('untracked administrator change')
        with self.assertRaises(m.InstallError):
            self.install()
        self.assertEqual(launcher.read_text(), 'untracked administrator change')

    def test_active_reader_rejects_teardown_before_any_mutation(self):
        self.install()
        manifest = (self.root / 'var/lib/archiver-mcp/installation.json').read_bytes()
        changes = list(self.ops.changes)
        services = list(self.ops.services)
        self.ops.active = True
        with self.assertRaises(m.InstallError):
            self.installer.uninstall()
        self.assertEqual((self.root / 'var/lib/archiver-mcp/installation.json').read_bytes(), manifest)
        self.assertEqual(self.ops.changes, changes)
        self.assertEqual(self.ops.services, services)
        self.assertTrue((self.root / 'etc/sudoers.d/archiver-mcp').exists())
        self.ops.active = False
        self.installer.uninstall()
        self.assert_clean()

    def test_account_removal_failure_retains_helper_and_resumes(self):
        self.install()
        with patch.object(self.ops, 'remove_reader', side_effect=m.InstallError('synthetic busy')), self.assertRaises(m.InstallError):
            self.installer.uninstall()
        self.assertTrue((self.root / 'usr/local/libexec/archiver-mcp/manage.py').is_file())
        self.installer.uninstall()
        self.assert_clean()

    def test_interruption_after_user_removed_resumes_without_recreating_it(self):
        self.install()
        original = self.ops.remove_reader
        def remove(*args):
            original(*args)
            raise m.InstallError('synthetic interruption after userdel')
        with patch.object(self.ops, 'remove_reader', side_effect=remove), self.assertRaises(m.InstallError):
            self.installer.uninstall()
        self.assertTrue((self.root / 'usr/local/libexec/archiver-mcp/manage.py').is_file())
        self.installer.uninstall()
        self.assert_clean()
        self.assertEqual(self.ops.created, ['archiver-mcp'])

    def test_interrupted_unlink_is_resumable(self):
        self.install()
        original = Path.unlink
        target = self.root / 'usr/local/bin/archiver-mcp'
        def unlink(path, *args, **kw):
            if path == target:
                original(path, *args, **kw)
                raise OSError('synthetic interruption after unlink')
            return original(path, *args, **kw)
        with patch.object(Path, 'unlink', unlink), self.assertRaises(OSError):
            self.installer.uninstall()
        self.assertTrue((self.root / 'usr/local/libexec/archiver-mcp/manage.py').is_file())
        self.installer.uninstall()
        self.assert_clean()

    def test_changed_archive_uid_rejected_before_upgrade_writes(self):
        self.install()
        path = self.root / 'var/lib/archiver-mcp/installation.json'
        state = json.loads(path.read_text())
        state['archive_uid'] = 99999  # Previously recorded uid, now stale.
        path.write_text(json.dumps(state))
        before = path.read_bytes()
        services, acls = list(self.ops.services), list(self.ops.changes)
        with self.assertRaises(m.InstallError):
            self.install()
        self.assertEqual(path.read_bytes(), before)
        self.assertEqual(self.ops.services, services)
        self.assertEqual(self.ops.changes, acls)

    def test_helper_lock_can_be_created_with_shipped_unit_write_permissions(self):
        # Interpret relevant systemd filesystem directives and verify the real
        # lock's created path is writable under those declared semantics.
        self.install()
        unit = configparser.ConfigParser(interpolation=None, strict=False)
        unit.read(self.root / 'etc/systemd/system/archiver-mcp-acl.service')
        service = unit['Service']
        allowed = [self.root / path.lstrip('/') for path in service.get('ReadWritePaths', '').split()]
        allowed += [self.root / 'run' / item for item in service.get('RuntimeDirectory', '').split()]
        with m.admin_lock(self.root) as handle:
            path = Path(os.readlink(f'/proc/self/fd/{handle.fileno()}'))
            self.assertTrue(any(path.is_relative_to(parent) for parent in allowed),
                f'helper cannot create its lock under ProtectSystem={service.get("ProtectSystem")}')
            self.assertNotIn('archiver-mcp/', str(path), 'reader-owned runtime cannot hold administration lock')
            self.assertEqual(path.parent.stat().st_mode & 0o777, 0o700)
        rule = (self.root / 'etc/tmpfiles.d/archiver-mcp.conf').read_text().splitlines()
        # tmpfiles root-owned runtime survives boot; validate the interpreted
        # fields of the rule covering the lock's parent, not a source substring.
        matching = [row.split() for row in rule if row.startswith('d ') and row.split()[1] == '/' + str(path.parent.relative_to(self.root))]
        self.assertEqual([row[2:5] for row in matching], [['0700', 'root', 'root']])

    def test_masked_acl_never_widens_another_identity(self):
        before = 'user::rw-\nuser:777:rw-\ngroup::rw-\nmask::---\nother::---\n'
        with self.archive.open('rb') as file:
            self.ops.set_acl(file.fileno(), before)
        self.install()
        with self.archive.open('rb') as file:
            acl = self.ops.get_acl(file.fileno())
        self.assertIn('user:777:---\n', acl)
        self.assertIn('group::---\n', acl)
        self.installer.uninstall()
        with self.archive.open('rb') as file:
            self.assertEqual(self.ops.get_acl(file.fileno()), before)

    def test_path_swapped_after_validation_never_acls_session(self):
        self.install()
        self.ops.changes.clear()
        original = self.ops.get_acl
        swapped = False
        old = self.archive.with_name('pinned.db')
        def get_acl(fd):
            nonlocal swapped
            if os.readlink(f'/proc/self/fd/{fd}') == str(self.archive) and not swapped:
                swapped = True
                self.archive.rename(old)
                self.archive.symlink_to(self.session)
                # Force a missing reader entry on the pinned original inode.
                st = os.fstat(fd)
                self.ops.acls[(st.st_dev, st.st_ino)] = 'user::rw-\ngroup::---\nother::---\n'
            return original(fd)
        with patch.object(self.ops, 'get_acl', side_effect=get_acl):
            self.installer.refresh()
        self.assertTrue(swapped)
        self.assertIn(str(old), [path for path, _ in self.ops.changes])
        self.assertNotIn(str(self.session), [path for path, _ in self.ops.changes])


class UbuntuAccountTests(unittest.TestCase):
    def test_real_proc_reader_detects_current_running_identity(self):
        self.assertTrue(m.HostOps().reader_active(os.getuid()))

    def test_userdel_automatic_private_group_removal_is_success(self):
        class Ubuntu(m.HostOps):
            present = True
            def account(self, name):
                return (2100, 2100) if self.present else None
            def group(self, name):
                return 2100 if self.present else None
            def run(self, args, **kwargs):
                if args[:2] == ['/usr/sbin/userdel', 'archiver-mcp']:
                    self.present = False
                else:
                    raise m.InstallError('unexpected groupdel on absent group')
        ops = Ubuntu()
        ops.remove_reader()
        self.assertIsNone(ops.account('archiver-mcp'))

    def test_recreated_group_is_not_removed(self):
        class Replaced(m.HostOps):
            def account(self, name):
                return None
            def group(self, name):
                return 4444
            def run(self, args, **kwargs):
                raise AssertionError('must not delete unrelated replacement group')
        with self.assertRaises(m.InstallError):
            Replaced().remove_reader((2100, 2100))


if __name__ == '__main__':
    unittest.main()
