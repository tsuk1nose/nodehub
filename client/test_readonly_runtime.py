"""Runtime policy tests never mount or change host directories."""
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('runtime', Path(__file__).with_name('readonly-runtime.py'))
runtime = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runtime)


class RuntimePolicyTest(unittest.TestCase):
    def test_policy_supports_arbitrary_existing_directories(self):
        with tempfile.TemporaryDirectory() as folder:
            config = Path(folder) / 'policy.json'
            directory = Path(folder) / 'state'; directory.mkdir()
            config.write_text(json.dumps({'temporary_directories': [{'path': str(directory), 'memory_mb': 8}]}))
            with patch.object(runtime, 'CONFIG', config):
                policy = runtime.load_policy()
            self.assertEqual(policy[0]['path'], directory)
            self.assertEqual(policy[0]['memory_mb'], 8)

    def test_bad_policies_are_rejected_before_mounting(self):
        with tempfile.TemporaryDirectory() as folder:
            config = Path(folder) / 'policy.json'
            directory = Path(folder) / 'state'; directory.mkdir()
            link = Path(folder) / 'link'; link.symlink_to(directory)
            file = Path(folder) / 'file'; file.touch()
            entries = [
                [{'path': '/'}], [{'path': '/data/state'}], [{'path': '/run/state'}],
                [{'path': '/etc/../root'}], [{'path': '/etc,bad'}], [{'path': str(link)}],
                [{'path': str(file)}], [{'path': str(directory), 'memory_mb': 0}],
                [{'path': str(directory), 'memory_mb': True}],
                [{'path': str(directory), 'allow_exec': 'yes'}],
                [{'path': str(directory)}, {'path': str(directory / 'nested')}],
                [{'path': str(directory / 'missing')}],
            ]
            with patch.object(runtime, 'CONFIG', config):
                for entries_value in entries:
                    config.write_text(json.dumps({'temporary_directories': entries_value}))
                    with self.subTest(entries=entries_value), self.assertRaises(RuntimeError):
                        runtime.load_policy()

    def test_optional_missing_directory_is_skipped(self):
        with tempfile.TemporaryDirectory() as folder, patch.object(runtime, 'run') as run:
            runtime.mount_directory({'path': Path(folder) / 'missing', 'optional': True, 'memory_mb': 16})
            run.assert_not_called()

    def test_mount_preserves_baseline_and_directory_permissions(self):
        with tempfile.TemporaryDirectory() as folder:
            state = Path(folder) / 'state'; state.mkdir(mode=0o750)
            (state / 'token').write_text('baseline-token')
            ram = Path(folder) / 'ram'
            with patch.object(runtime, 'RUNTIME', ram), patch.object(runtime.os.path, 'ismount', return_value=False), patch.object(runtime, 'run') as run:
                runtime.mount_directory({'path': state, 'memory_mb': 8, 'optional': False})
            calls = [call.args for call in run.call_args_list]
            self.assertIn('size=8m,mode=0700,nosuid,nodev,noexec', calls[0])
            self.assertEqual(calls[-1][-1], str(state))
            self.assertEqual((state / 'token').read_text(), 'baseline-token')
            self.assertEqual(next(ram.glob('*/upper')).stat().st_mode & 0o777, 0o750)
            self.assertTrue(any(call[:3] == ('mount', '-o', 'remount,bind,ro') for call in calls))

    def test_mesh_directory_allows_execution_in_both_mounts(self):
        with tempfile.TemporaryDirectory() as folder:
            state = Path(folder) / 'meshagent'; state.mkdir()
            with patch.object(runtime, 'RUNTIME', Path(folder) / 'ram'), patch.object(runtime.os.path, 'ismount', return_value=False), patch.object(runtime, 'run') as run:
                runtime.mount_directory({'path': state, 'memory_mb': 64, 'optional': False, 'allow_exec': True})
            calls = [call.args for call in run.call_args_list]
            self.assertIn('size=64m,mode=0700,nosuid,nodev,exec', calls[0])
            self.assertIn(',nosuid,nodev,exec', calls[-1][4])
            self.assertNotIn('noexec', calls[-1][4])
            self.assertEqual(runtime.DEFAULT['temporary_directories'][1]['path'], '/usr/local/mesh_services/meshagent')

    def test_existing_unexpected_mount_is_rejected(self):
        with tempfile.TemporaryDirectory() as folder, patch.object(runtime.os.path, 'ismount', return_value=True), patch.object(runtime, 'run', return_value='other'):
            with self.assertRaisesRegex(RuntimeError, 'Unexpected existing mount'):
                runtime.mount_directory({'path': Path(folder), 'memory_mb': 16, 'optional': False})

    def test_maintenance_boot_does_not_change_mounts(self):
        with patch.object(runtime, 'root_mount', return_value={'fstype': 'ext4'}), patch.object(runtime, 'run') as run:
            runtime.mount_state()
            run.assert_not_called()

    def test_root_is_readonly_before_directory_mounts(self):
        root = {'source': 'overlayroot', 'fstype': 'overlay', 'options': 'rw,lowerdir=/media/root-ro,upperdir=/media/root-rw/overlay'}
        readonly = dict(root, options=root['options'].replace('rw,', 'ro,', 1))
        with patch.object(runtime, 'root_mount', return_value=readonly), patch.object(runtime, 'run', side_effect=['tmpfs', 'ro', '']) as run:
            runtime.make_root_readonly(root)
        self.assertEqual(run.call_args.args, ('mount', '-t', 'overlay', '-o', 'remount,ro', 'overlayroot', '/'))

    def test_root_rejects_unsafe_layout(self):
        root = {'source': 'overlayroot', 'fstype': 'overlay', 'options': 'ro,lowerdir=/media/root-ro,upperdir=/media/root-rw/overlay'}
        for changes, responses in (({'source': 'other'}, []), ({}, ['ext4']), ({}, ['tmpfs', 'rw'])):
            with patch.object(runtime, 'run', side_effect=responses), self.assertRaises(RuntimeError):
                runtime.make_root_readonly(dict(root, **changes))

    def test_install_persists_policy_and_early_service(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            config = root / 'policy.json'
            def local(value):
                value = str(value)
                return Path(value) if value.startswith(str(root)) else root / value.lstrip('/')
            with patch.object(runtime, 'CONFIG', config), patch.object(runtime, 'Path', side_effect=local), patch.object(runtime, 'root_mount', return_value={'fstype': 'ext4'}), patch.object(runtime, 'load_policy', return_value=[]):
                runtime.install()
                runtime.install()
            self.assertEqual(json.loads(config.read_text()), runtime.DEFAULT)
            unit = local('/etc/systemd/system/nodehub-temporary-writes.service').read_text()
            self.assertIn('After=systemd-remount-fs.service', unit)
            self.assertIn('Before=local-fs-pre.target', unit)
            self.assertTrue(local('/etc/systemd/system/local-fs.target.requires/nodehub-temporary-writes.service').is_symlink())

    def test_previous_generated_default_is_upgraded_with_meshagent(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            config = root / 'policy.json'
            config.write_text(json.dumps({'temporary_directories': [runtime.DEFAULT['temporary_directories'][0]]}))
            def local(value):
                value = str(value)
                return Path(value) if value.startswith(str(root)) else root / value.lstrip('/')
            with patch.object(runtime, 'CONFIG', config), patch.object(runtime, 'Path', side_effect=local), patch.object(runtime, 'root_mount', return_value={'fstype': 'ext4'}), patch.object(runtime, 'load_policy', return_value=[]):
                runtime.install()
            self.assertEqual(json.loads(config.read_text()), runtime.DEFAULT)

    def test_existing_custom_configuration_is_preserved(self):
        with tempfile.TemporaryDirectory() as folder:
            path = Path(folder) / 'config'; path.write_text('custom')
            with self.assertRaisesRegex(RuntimeError, 'differs'): runtime.write_owned(path, 'replacement')
            self.assertEqual(path.read_text(), 'custom')


if __name__ == '__main__': unittest.main()
