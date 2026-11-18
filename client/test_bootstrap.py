import importlib.util
from pathlib import Path
import unittest
from unittest.mock import patch
import warnings
import io
import hashlib
from unittest.mock import Mock
import getpass
import tempfile
import json
import base64

spec = importlib.util.spec_from_file_location('nodehub_bootstrap', Path(__file__).with_name('bootstrap.py'))
bootstrap = importlib.util.module_from_spec(spec)
spec.loader.exec_module(bootstrap)

class BootstrapTests(unittest.TestCase):
    def test_sealing_removes_only_host_keys_without_powering_off(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            def local(value):
                return root / str(value).lstrip('/')
            for name in ('/etc/ssh/ssh_host_ed25519_key', '/etc/ssh/ssh_host_ed25519_key.pub',
                         '/etc/ssh/sshd_config', '/etc/ssh/ssh_host_custom_config',
                         '/etc/machine-id', '/var/lib/dbus/machine-id'):
                path = local(name)
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text('source identity')
            initializer = Mock()
            commands = []
            with patch.object(bootstrap, 'Path', side_effect=local), patch.object(bootstrap, 'run', side_effect=commands.append):
                bootstrap.seal_template(initializer)
            self.assertFalse(local('/etc/ssh/ssh_host_ed25519_key').exists())
            self.assertFalse(local('/etc/ssh/ssh_host_ed25519_key.pub').exists())
            self.assertEqual(local('/etc/ssh/sshd_config').read_text(), 'source identity')
            self.assertTrue(local('/etc/ssh/ssh_host_custom_config').exists())
            self.assertEqual(local('/etc/machine-id').read_text(), '')
            self.assertEqual(local('/var/lib/dbus/machine-id').readlink(), Path('/etc/machine-id'))
            self.assertEqual(commands[-1], ['sync'])
            self.assertNotIn(['systemctl', 'poweroff'], commands)
            self.assertNotIn(['systemctl', 'reboot'], commands)
            unit = initializer.atomic_write.call_args.args[1]
            self.assertIn('Before=ssh.service nodehub-init.service', unit)
            self.assertIn('ExecStart=/usr/bin/ssh-keygen -A', unit)
            self.assertNotIn(['ssh-keygen', '-A'], commands)

    def test_template_preflight_rejects_persistent_identities_and_unmounted_data(self):
        with patch.object(bootstrap.os.path, 'ismount', return_value=False):
            with self.assertRaisesRegex(RuntimeError, 'Mount'):
                bootstrap.require_clean_template()
        for existing in ('/data/nodehub/meshagent',
                         '/var/lib/nodehub-agent/identity.json', '/var/lib/cloud'):
            with tempfile.TemporaryDirectory() as folder:
                root = Path(folder)
                def local(value): return root / str(value).lstrip('/')
                path = local(existing)
                path.parent.mkdir(parents=True, exist_ok=True)
                path.touch()
                with patch.object(bootstrap.os.path, 'ismount', return_value=True), patch.object(bootstrap, 'Path', side_effect=local):
                    with self.assertRaisesRegex(RuntimeError, 'state'):
                        bootstrap.require_clean_template()

    def test_fresh_tailscale_state_can_be_cleaned_after_stopping(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            def local(value): return root / str(value).lstrip('/')
            state = local('/var/lib/tailscale/tailscaled.state')
            state.parent.mkdir(parents=True)
            state.write_text(json.dumps({'_machinekey': 'private', '_profiles': base64.b64encode(b'[]').decode()}))
            commands = []
            status = Mock(returncode=0, stdout=json.dumps({'BackendState': 'NeedsLogin', 'TailscaleIPs': []}))
            with patch.object(bootstrap, 'Path', side_effect=local), patch.object(bootstrap.subprocess, 'run', return_value=status), patch.object(bootstrap, 'run', side_effect=commands.append):
                bootstrap.require_unregistered_tailscale()
                self.assertTrue(state.exists())
                bootstrap.clean_template_tailscale()
            self.assertEqual(commands, [['systemctl', 'stop', 'tailscaled.service']])
            self.assertFalse(state.parent.exists())

    def test_logged_out_profiles_and_enrolled_tailscale_are_refused(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            def local(value): return root / str(value).lstrip('/')
            state = local('/var/lib/tailscale/tailscaled.state')
            state.parent.mkdir(parents=True)
            state.write_text(json.dumps({'_profiles': base64.b64encode(b'[{"ID":"old"}]').decode()}))
            for backend in ('Running', 'Stopped', 'NeedsMachineAuth', 'NeedsLogin'):
                status = Mock(returncode=0, stdout=json.dumps({'BackendState': backend}))
                with patch.object(bootstrap, 'Path', side_effect=local), patch.object(bootstrap.subprocess, 'run', return_value=status):
                    with self.assertRaises(RuntimeError):
                        bootstrap.require_unregistered_tailscale()
                self.assertTrue(state.exists())
            state.write_text('invalid')
            with patch.object(bootstrap, 'Path', side_effect=local):
                with self.assertRaisesRegex(RuntimeError, 'Cannot verify'):
                    bootstrap.check_tailscale_store()

    def test_runtime_defaults_are_available_without_overwriting_user_policy(self):
        with tempfile.TemporaryDirectory() as folder:
            config = Path(folder) / 'readonly-runtime.json'
            initializer = Mock()
            defaults = {'temporary_directories': [{'path': '/var/lib/NetworkManager'}]}
            with patch.object(bootstrap, 'Path', return_value=config):
                bootstrap.write_runtime_defaults(initializer, defaults)
                initializer.atomic_json.assert_called_once_with(config, defaults)
                initializer.reset_mock()
                config.write_text('{"temporary_directories": []}')
                bootstrap.write_runtime_defaults(initializer, defaults)
                initializer.atomic_json.assert_not_called()
                self.assertEqual(json.loads(config.read_text()), {'temporary_directories': []})

    def test_https_center_and_no_credentials(self):
        self.assertEqual(bootstrap.center_url('https://hub.example.com/'), 'https://hub.example.com')
        for value in ['http://hub.example.com', 'https://user:password@hub.example.com', 'https://hub.example.com/?token=secret', 'https://hub.example.com/#secret']:
            with self.assertRaises(RuntimeError): bootstrap.center_url(value)

    def test_assets_do_not_include_secrets_or_traversal(self):
        self.assertTrue(bootstrap.checked_file('client/nodehub-init.py'))
        for value in ['../config/config.json', 'client/activation.json', 'agent/identity.json', '/etc/shadow']:
            self.assertFalse(bootstrap.checked_file(value))

    def test_package_integrity_and_download_bounds(self):
        opener = Mock()
        payload = b'example executable source'
        metadata = {'size': len(payload), 'sha256': hashlib.sha256(payload).hexdigest()}
        opener.open.return_value = io.BytesIO(payload)
        self.assertEqual(bootstrap.fetch_asset(opener, 'https://hub.example.com', 'client/nodehub-init.py', metadata), payload)
        opener.open.return_value = io.BytesIO(b'changed executable source')
        with self.assertRaisesRegex(RuntimeError, 'size limit|checksum'):
            bootstrap.fetch_asset(opener, 'https://hub.example.com', 'client/nodehub-init.py', metadata)
        for invalid in [{'size': 999999999, 'sha256': metadata['sha256']}, {'size': 1, 'sha256': 'invalid'}]:
            with self.assertRaisesRegex(RuntimeError, 'metadata'):
                bootstrap.fetch_asset(opener, 'https://hub.example.com', 'client/nodehub-init.py', invalid)
        with self.assertRaisesRegex(RuntimeError, 'Unexpected'):
            bootstrap.fetch_asset(opener, 'https://hub.example.com', '../config/config.json', metadata)

    def test_redirects_are_not_followed(self):
        with self.assertRaisesRegex(RuntimeError, 'redirects'):
            bootstrap.NoRedirect().redirect_request(None, None, 302, '', {}, 'https://other.example.com')

    def test_no_echo_fallback(self):
        with patch('builtins.open') as opened:
            def fallback(*args, **kwargs):
                warnings.warn('Cannot disable echo', getpass.GetPassWarning)
                return 'must-not-be-returned'
            with patch.object(bootstrap.getpass, 'getpass', side_effect=fallback):
                with self.assertRaisesRegex(RuntimeError, 'interactive terminal'):
                    bootstrap.prompt_token()

    def test_missing_tty_fails_closed(self):
        with patch('builtins.open', side_effect=OSError('No terminal')):
            with self.assertRaisesRegex(RuntimeError, 'interactive terminal'):
                bootstrap.prompt_token()

if __name__ == '__main__':
    unittest.main()
