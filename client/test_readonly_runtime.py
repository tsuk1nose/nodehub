"""Read-only runtime policy checks without mounting host filesystems."""
import importlib.util
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('runtime', Path(__file__).with_name('readonly-runtime.py'))
runtime = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runtime)

class RuntimePolicyTest(unittest.TestCase):
    def test_maintenance_boot_does_not_mount(self):
        with patch.object(runtime, 'run', return_value='ext4') as run:
            runtime.mount_state()
        self.assertEqual(run.call_count, 1)

    def test_missing_identity_blocks_mount(self):
        with tempfile.TemporaryDirectory() as folder, patch.object(runtime, 'STATE', Path(folder)), patch.object(runtime, 'run', return_value='overlay') as run:
            with self.assertRaisesRegex(RuntimeError, 'identity'): runtime.mount_state()
            self.assertEqual(run.call_count, 1)

    def test_mount_policy_preserves_lower_and_bounds_upper(self):
        with tempfile.TemporaryDirectory() as folder:
            state = Path(folder) / 'state'; state.mkdir()
            (state / 'secret_key').write_text('existing-identity')
            ram = Path(folder) / 'ram'
            with patch.object(runtime, 'STATE', state), patch.object(runtime, 'RUNTIME', ram), patch.object(runtime.os.path, 'ismount', return_value=False), patch.object(runtime, 'run', return_value='overlay') as run:
                runtime.mount_state()
            calls = [call.args for call in run.call_args_list]
            self.assertTrue(any('size=16m,mode=0700,nosuid,nodev,noexec' in call for call in calls))
            self.assertIn(('mount', '-o', 'remount,bind,ro', str(ram / 'lower')), calls)
            self.assertEqual(calls[-1][-2:], ('nodehub-network-state', str(state)))
            self.assertEqual((state / 'secret_key').read_text(), 'existing-identity')

    def test_install_requires_mount_before_networkmanager(self):
        with tempfile.TemporaryDirectory() as folder:
            state=Path(folder); (state/'secret_key').write_text('identity')
            with patch.object(runtime, 'STATE', state), patch.object(runtime, 'write_owned') as write:
                runtime.install()
            configs = dict(call.args for call in write.call_args_list)
            self.assertIn('Requires=nodehub-network-state.service', configs['/etc/systemd/system/NetworkManager.service.d/nodehub-state.conf'])
            self.assertIn('Storage=volatile', configs['/etc/systemd/journald.conf.d/60-nodehub-volatile.conf'])

    def test_existing_custom_configuration_is_preserved(self):
        with tempfile.TemporaryDirectory() as folder:
            path=Path(folder)/'config';path.write_text('custom')
            with self.assertRaisesRegex(RuntimeError, 'differs'): runtime.write_owned(path, 'replacement')
            self.assertEqual(path.read_text(),'custom')

if __name__ == '__main__': unittest.main()
