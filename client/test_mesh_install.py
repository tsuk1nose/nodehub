"""MeshAgent service must retain executable, settings, and identity after cleanup."""
import importlib.util
from pathlib import Path
import tempfile
import unittest
from unittest.mock import Mock, patch
from types import SimpleNamespace

spec = importlib.util.spec_from_file_location('mesh_install', Path(__file__).with_name('mesh-install.py'))
mesh = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mesh)

class PersistentMeshServiceTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name) / 'persistent-mesh'
        self.directory.mkdir()
        for name in ('meshagent', 'meshagent.msh', 'meshagent.db'):
            (self.directory / name).write_bytes(b'example')

    def output(self, argv, **kwargs):
        if 'ExecStart' in argv: return 'path=' + str(self.directory / 'meshagent') + ' ; argv[]=meshagent'
        return str(self.directory)

    def test_complete_persistent_service(self):
        with patch.object(mesh.subprocess, 'check_output', side_effect=self.output):
            mesh.verify_service(self.directory)

    def test_transient_executable_is_rejected_even_when_files_exist(self):
        def output(argv, **kwargs):
            if 'ExecStart' in argv: return 'path=/tmp/nodehub-mesh-example/meshagent ; argv[]=meshagent'
            return str(self.directory)
        with patch.object(mesh.subprocess, 'check_output', side_effect=output):
            with self.assertRaisesRegex(RuntimeError, 'persistent installation'):
                mesh.verify_service(self.directory)

    def test_missing_identity_prevents_acceptance(self):
        (self.directory / 'meshagent.db').unlink()
        with patch.object(mesh.subprocess, 'check_output', side_effect=self.output):
            with self.assertRaisesRegex(RuntimeError, 'identity database'):
                mesh.verify_service(self.directory)

class RootInstallationPolicyTest(unittest.TestCase):
    def test_overlay_root_is_rejected(self):
        with patch.object(mesh.subprocess, 'check_output', return_value='overlay'):
            with self.assertRaisesRegex(RuntimeError, 'persistent root'): mesh.require_persistent_root()

    def test_readonly_root_is_rejected(self):
        with patch.object(mesh.subprocess, 'check_output', return_value='ext4'), patch.object(mesh.os, 'statvfs', return_value=SimpleNamespace(f_flag=mesh.os.ST_RDONLY)):
            with self.assertRaisesRegex(RuntimeError, 'writable persistent root'): mesh.require_persistent_root()

    def test_guard_uses_runtime_overlay_without_data_dependency(self):
        with tempfile.TemporaryDirectory() as folder:
            target = Path(folder) / 'runtime.conf'
            with patch.object(mesh, 'Path', return_value=target): mesh.install_mount_guard()
            config = target.read_text()
            self.assertIn('After=nodehub-temporary-writes.service', config)
            self.assertIn('ExecStartPre=/usr/bin/test -w /usr/local/mesh_services/meshagent', config)
            self.assertNotIn('/data', config)

if __name__ == '__main__': unittest.main()
