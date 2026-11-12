"""Root provisioning tests use only temporary directories and mocked commands."""
import importlib.util
import json
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch

spec = importlib.util.spec_from_file_location('nodehub_init', Path(__file__).with_name('nodehub-init.py'))
init = importlib.util.module_from_spec(spec)
spec.loader.exec_module(init)

class SSHProvisionTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.home = self.root / 'root-home'
        self.legacy = self.root / 'legacy-home'
        self.dropin = self.root / '00-nodehub-root.conf'
        self.state_dir = self.root / 'state'
        self.state_dir.mkdir()
        self.state = {'serial': '0123456789AB'}
        self.key = 'ssh-ed25519 AAAATESTPUBLICKEY'
        self.client = Mock()
        self.client.request.return_value = {'username': 'root', 'authorized_key': self.key}
        self.account = SimpleNamespace(pw_uid=0, pw_gid=0, pw_dir=str(self.home), pw_shell='/bin/bash')
        self.policy = 'permitrootlogin prohibit-password\npubkeyauthentication yes\n'
        def run(argv):
            if argv[:2] == ['/usr/sbin/sshd', '-T']: return self.policy
            return 'ssh-ed25519 AAAAHOSTKEY\n'
        self.run = Mock(side_effect=run)
        self.addCleanup(patch.stopall)
        patch.object(init, 'run', self.run).start()
        patch.object(init, 'Path', lambda p: self.legacy if str(p) == '/var/lib/nodehub-maint' else self.dropin if str(p) == '/etc/ssh/sshd_config.d/00-nodehub-root.conf' else Path(p)).start()
        self.pwd = patch.object(init.pwd, 'getpwnam', return_value=self.account).start()

    def test_install_preserves_existing_authorizations_and_retry(self):
        (self.home / '.ssh').mkdir(parents=True)
        authorized = self.home / '.ssh/authorized_keys'
        existing = 'from="192.0.2.1" ssh-ed25519 AAAAEXISTING admin-key\n'
        authorized.write_text(existing)
        init.provision_ssh(self.client, self.state, self.state_dir)
        first = authorized.read_text()
        self.assertTrue(first.startswith(existing))
        self.assertEqual(authorized.stat().st_mode & 0o777, 0o600)
        self.assertIn(self.key, first)
        self.assertEqual(self.home.stat().st_mode & 0o777, 0o700)
        marker = self.state_dir / 'ssh-provision.json'
        self.assertTrue(json.loads(marker.read_text())['confirmed'])
        init.provision_ssh(self.client, self.state, self.state_dir)
        self.assertEqual(first, authorized.read_text())
        self.assertFalse(any(call.args[0][0] == 'useradd' for call in self.run.call_args_list))
        self.assertEqual(self.client.request.call_args.args[1]['username'], 'root')
        self.assertFalse(self.dropin.exists())

    def test_confirmation_failure_retries_same_identity(self):
        result = self.client.request.return_value
        self.client.request.side_effect = [result, RuntimeError('response lost')]
        with self.assertRaisesRegex(RuntimeError, 'response lost'):
            init.provision_ssh(self.client, self.state, self.state_dir)
        self.client.request.side_effect = None
        self.client.request.return_value = result
        init.provision_ssh(self.client, self.state, self.state_dir)
        self.assertTrue(json.loads((self.state_dir / 'ssh-provision.json').read_text())['confirmed'])
        self.assertEqual((self.home / '.ssh/authorized_keys').read_text().count(self.key), 1)

    def test_changed_login_key_rejected(self):
        (self.state_dir / 'ssh-provision.json').write_text(json.dumps({'serial': self.state['serial'], 'authorized_key': 'other'}))
        with self.assertRaisesRegex(RuntimeError, 'refusing to replace'):
            init.provision_ssh(self.client, self.state, self.state_dir)
        self.assertFalse(self.home.exists())

    def test_symlink_home_rejected(self):
        self.home.symlink_to(self.root, target_is_directory=True)
        with self.assertRaisesRegex(RuntimeError, 'symbolic link'):
            init.provision_ssh(self.client, self.state, self.state_dir)

    def test_migrate_only_owned_legacy_key(self):
        marker = {'serial': self.state['serial'], 'authorized_key': self.key, 'uid': 1001}
        (self.state_dir / 'ssh-provision.json').write_text(json.dumps(marker))
        legacy = SimpleNamespace(pw_uid=1001, pw_dir=str(self.legacy))
        self.pwd.side_effect = lambda name: self.account if name == 'root' else legacy
        (self.legacy / '.ssh').mkdir(parents=True)
        old_key = self.legacy / '.ssh/authorized_keys'
        old_key.write_text('no-agent-forwarding,no-port-forwarding,no-X11-forwarding ' + self.key + '\n')
        init.provision_ssh(self.client, self.state, self.state_dir)
        self.assertFalse(old_key.exists())
        self.assertTrue(self.legacy.exists())
        # Unrelated administrator changes must be retained on later retries.
        old_key.write_text('ssh-ed25519 AAAAOTHER\n')
        init.provision_ssh(self.client, self.state, self.state_dir)
        self.assertTrue(old_key.exists())

    def test_corrupt_managed_block_rejected(self):
        (self.home / '.ssh').mkdir(parents=True)
        authorized = self.home / '.ssh/authorized_keys'
        content = '# BEGIN NODEHUB 0123456789AB\nexisting\n'
        authorized.write_text(content)
        with self.assertRaisesRegex(RuntimeError, 'block is malformed'):
            init.provision_ssh(self.client, self.state, self.state_dir)
        self.assertEqual(content, authorized.read_text())

    def test_root_access_override_and_no_password_expansion(self):
        self.policy = 'permitrootlogin no\npubkeyauthentication yes\n'
        original = self.run.side_effect
        def run(argv):
            if argv[:2] == ['/usr/sbin/sshd', '-T'] and self.dropin.exists():
                return 'permitrootlogin prohibit-password\npubkeyauthentication yes\n'
            return original(argv)
        self.run.side_effect = run
        init.provision_ssh(self.client, self.state, self.state_dir)
        self.assertEqual(self.dropin.read_text(), 'PermitRootLogin prohibit-password\n')

    def test_existing_root_policy_preserved(self):
        self.policy = 'permitrootlogin yes\npubkeyauthentication yes\n'
        init.provision_ssh(self.client, self.state, self.state_dir)
        self.assertFalse(self.dropin.exists())

    def test_server_policy_blocks_confirmation(self):
        self.policy = 'permitrootlogin prohibit-password\npubkeyauthentication no\n'
        with self.assertRaisesRegex(RuntimeError, 'effective sshd configuration'):
            init.provision_ssh(self.client, self.state, self.state_dir)
        self.assertEqual(self.client.request.call_count, 1)

if __name__ == '__main__':
    unittest.main()
