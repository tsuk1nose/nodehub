"""MeshAgent service must retain executable, settings, and identity after cleanup."""
import importlib.util
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

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

if __name__ == '__main__': unittest.main()
