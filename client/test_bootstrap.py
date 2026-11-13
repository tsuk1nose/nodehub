import importlib.util
from pathlib import Path
import unittest
from unittest.mock import patch
import warnings
import io
import hashlib
from unittest.mock import Mock
import getpass

spec = importlib.util.spec_from_file_location('nodehub_bootstrap', Path(__file__).with_name('bootstrap.py'))
bootstrap = importlib.util.module_from_spec(spec)
spec.loader.exec_module(bootstrap)

class BootstrapTests(unittest.TestCase):
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
