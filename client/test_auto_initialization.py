"""Automatic cleanup tests never touch the host's services or filesystem."""
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import Mock, patch

spec = importlib.util.spec_from_file_location('initializer_auto', Path(__file__).with_name('nodehub-init.py'))
init = importlib.util.module_from_spec(spec)
spec.loader.exec_module(init)

class AutomaticInitializationTest(unittest.TestCase):
    def test_detached_finalizer_cleans_only_bootstrap_files(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            def local(value):
                value = str(value)
                if value.startswith(str(root)): return Path(value)
                return root / value.lstrip('/') if value.startswith('/') else Path(value)
            state = local('/var/lib/nodehub-init')
            state.mkdir(parents=True)
            (state / 'state.json').write_text(json.dumps({'server': 'https://hub.example.com', 'serial': 'ABCDEFGHIJKL', 'credential': 'private'}))
            owned = ['/etc/nodehub/activation.json', '/etc/nodehub/mesh-install.json',
                     '/usr/local/sbin/nodehub-init', '/usr/local/lib/nodehub/mesh-install',
                     '/usr/local/lib/nodehub/enable-overlayroot', '/opt/nodehub/bootstrap/meshagent',
                     '/etc/systemd/system/nodehub-init.service',
                     '/etc/systemd/system/nodehub-init.service.d/bootstrap.conf']
            retained = ['/var/lib/nodehub-agent/identity.json', '/var/lib/tailscale/tailscaled.state',
                        '/usr/local/sbin/nodehub-agent', '/root/.ssh/authorized_keys']
            for name in owned + retained:
                path = local(name); path.parent.mkdir(parents=True, exist_ok=True); path.write_text('preserve' if name in retained else 'bootstrap')
            client = Mock()
            client.request.return_value = {'node': {'headscale_id': '1', 'mesh_id': 'node//test'}}
            commands = []
            def run(argv, **kwargs):
                commands.append(argv)
                return 'ext4\n' if argv[0] == 'findmnt' else ''
            with patch.object(init, 'DEFAULT_STATE', state), patch.object(init, 'Path', local), patch.object(init, '__file__', '/usr/local/sbin/nodehub-init'), patch.object(init, 'Client', return_value=client), patch.object(init, 'run', side_effect=run), patch.object(init, 'trusted_hook', return_value='/hook'), patch.object(init.os, 'geteuid', return_value=0), patch('sys.argv', ['nodehub-init', '--finalize', '--overlay-hook', '/hook']):
                init.main()
            for name in owned: self.assertFalse(local(name).exists(), name)
            self.assertFalse(state.exists())
            for name in retained: self.assertEqual(local(name).read_text(), 'preserve')
            self.assertEqual(commands[-1], ['systemctl', 'reboot'])
            self.assertLess(commands.index(['/hook']), commands.index(['systemctl', 'disable', '--now', 'nodehub-init.service']))

    def test_overlay_failure_preserves_recovery_tools_and_state(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            def local(value):
                if str(value).startswith(str(root)): return Path(value)
                return root / str(value).lstrip('/')
            state = local('/var/lib/nodehub-init'); state.mkdir(parents=True)
            (state / 'state.json').write_text(json.dumps({'server': 'https://hub.example.com', 'serial': 'ABCDEFGHIJKL', 'credential': 'private'}))
            client = Mock(); client.request.return_value = {'node': {'headscale_id': '1', 'mesh_id': 'node//test'}}
            commands = []
            def run(argv, **kwargs):
                commands.append(argv)
                if argv == ['/hook']: raise RuntimeError('overlay prerequisite missing')
                return 'ext4\n'
            with patch.object(init, 'DEFAULT_STATE', state), patch.object(init, 'Path', local), patch.object(init, 'Client', return_value=client), patch.object(init, 'run', side_effect=run), patch.object(init, 'trusted_hook', return_value='/hook'), patch.object(init.os, 'geteuid', return_value=0), patch('sys.argv', ['nodehub-init', '--finalize', '--overlay-hook', '/hook']):
                with self.assertRaisesRegex(RuntimeError, 'prerequisite'): init.main()
            self.assertTrue((state / 'state.json').exists())
            self.assertNotIn(['systemctl', 'reboot'], commands)
            self.assertNotIn(['systemctl', 'disable', '--now', 'nodehub-init.service'], commands)

if __name__ == '__main__': unittest.main()
