#!/usr/bin/env python3
"""HTTPS bootstrap for Debian nodes; tokens are entered locally, never in arguments."""
import argparse
import getpass
import fcntl
import warnings
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import subprocess
import sys
import tempfile
import urllib.parse
import urllib.request

class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise RuntimeError('Bootstrap redirects are not allowed; use the final center URL')

def center_url(value):
    url = urllib.parse.urlsplit(value)
    if url.scheme != 'https' or not url.hostname or url.username or url.password or url.query or url.fragment:
        raise RuntimeError('The center URL must use HTTPS without credentials, query, or fragment')
    return value.rstrip('/')

def checked_file(name):
    return name in {
        'client/bootstrap.py', 'client/nodehub-init.py', 'client/mesh-install.py',
        'client/install.sh', 'client/nodehub-init.service', 'client/enable-overlayroot.example.sh',
        'agent/install.sh', 'agent/config.example.json', 'agent/nodehub-agent.service',
        'bin/nodehub-agent-linux-amd64', 'bin/nodehub-agent-linux-arm64',
    }

def download(opener, url, limit):
    with opener.open(url, timeout=90) as response:
        content = response.read(limit + 1)
    if len(content) > limit: raise RuntimeError('Bootstrap download exceeds the size limit')
    return content

def fetch_asset(opener, server, name, metadata):
    if not checked_file(name): raise RuntimeError('Unexpected bootstrap file')
    size = metadata.get('size')
    digest = metadata.get('sha256', '')
    if not isinstance(size, int) or not 0 < size <= 64 * 1024 * 1024 or not re.fullmatch('[0-9a-f]{64}', digest):
        raise RuntimeError('Invalid bootstrap file metadata')
    content = download(opener, server + '/bootstrap/files/' + name, size)
    if len(content) != size or hashlib.sha256(content).hexdigest() != digest:
        raise RuntimeError('Bootstrap package checksum mismatch')
    return content

def run(argv):
    result = subprocess.run(argv, capture_output=True, text=True)
    if result.returncode: raise RuntimeError(Path(argv[0]).name + ' failed; check the local service configuration')

def install_file(source, target, mode):
    target = Path(target)
    target.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    if target.is_symlink(): raise RuntimeError('Refusing to replace a symbolic link')
    with tempfile.NamedTemporaryFile(dir=target.parent, delete=False) as stream:
        pending = Path(stream.name)
        try:
            stream.write(Path(source).read_bytes()); stream.flush(); os.fchmod(stream.fileno(), mode); os.fsync(stream.fileno())
            os.replace(pending, target)
        finally: pending.unlink(missing_ok=True)

def prompt_token():
    try:
        with open('/dev/tty', 'w') as terminal, warnings.catch_warnings():
            warnings.simplefilter('error', getpass.GetPassWarning)
            return getpass.getpass('Node Hub initialization token: ', stream=terminal).strip()
    except (OSError, getpass.GetPassWarning):
        raise RuntimeError('An interactive terminal with hidden input is required') from None

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--server', required=True)
    parser.add_argument('--prepare-image', action='store_true', help='Save the batch token and install first-boot tools without enrolling the template')
    args = parser.parse_args()
    if os.geteuid() != 0: raise RuntimeError('Run as root')
    os.umask(0o077)
    lock = open('/run/nodehub-bootstrap.lock', 'a')
    fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
    server = center_url(args.server)
    if platform.system() != 'Linux': raise RuntimeError('Linux is required')
    arch = {'x86_64': 'amd64', 'aarch64': 'arm64'}.get(platform.machine())
    if arch != 'amd64': raise RuntimeError('The automatic MeshAgent bootstrap currently supports Debian amd64; use the manual hook for other architectures')
    if not Path('/etc/debian_version').is_file(): raise RuntimeError('This installer targets Debian 13')
    if Path('/etc/debian_version').read_text().strip().split('.')[0] != '13': raise RuntimeError('Debian 13 is required')
    for command in ('tailscale', 'systemctl', 'ssh-keygen'):
        import shutil
        if not shutil.which(command): raise RuntimeError('Preinstall the required command: ' + command)
    # Registration and finalization must happen before root becomes ephemeral.
    if subprocess.check_output(['findmnt', '-n', '-o', 'FSTYPE', '/'], text=True).strip() == 'overlay':
        raise RuntimeError('Enter persistent maintenance mode before installing or enrolling')
    state_file = Path('/var/lib/nodehub-init/state.json')
    state = json.loads(state_file.read_text()) if state_file.exists() else None
    if state and state.get('server') != server: raise RuntimeError('Existing node state belongs to another center; do not overwrite it')
    if args.prepare_image and (state or Path('/var/lib/nodehub-agent/identity.json').exists()):
        raise RuntimeError('The template already has a node identity; do not clone it')
    agent_identity = Path('/var/lib/nodehub-agent/identity.json')
    if agent_identity.exists():
        identity = json.loads(agent_identity.read_text())
        if identity.get('server', '').rstrip('/') != server:
            raise RuntimeError('Existing Agent identity belongs to another center; do not overwrite it')
    if not Path('/usr/sbin/sshd').is_file(): raise RuntimeError('Preinstall OpenSSH server')
    if not Path('/usr/share/initramfs-tools/scripts/init-bottom/overlayroot').is_file():
        raise RuntimeError('Preinstall overlayroot before preparing the image')
    if Path('/etc/systemd/system/nodehub-init.service').exists():
        run(['systemctl', 'stop', 'nodehub-init.service'])
    token = None
    if not state or not state.get('serial'):
        token = prompt_token()
        if not token: raise RuntimeError('The initialization token cannot be empty')
    opener = urllib.request.build_opener(NoRedirect())
    manifest = json.loads(download(opener, server + '/bootstrap/manifest?arch=' + arch, 128 * 1024))
    files = manifest['files']
    required = {'client/nodehub-init.py','client/mesh-install.py','client/install.sh','client/nodehub-init.service','client/enable-overlayroot.example.sh'}
    if manifest['terminal_enabled']:
        required.update({'agent/install.sh','agent/config.example.json','agent/nodehub-agent.service','bin/nodehub-agent-linux-' + arch})
    if not required.issubset(files): raise RuntimeError('The center bootstrap package is incomplete')
    with tempfile.TemporaryDirectory(prefix='nodehub-package-') as temp:
        folder = Path(temp)
        for name in required:
            content = fetch_asset(opener, server, name, files[name])
            path = folder / name; path.parent.mkdir(parents=True, exist_ok=True); path.write_bytes(content)
        run(['bash', str(folder / 'client/install.sh')])
        install_file(folder / 'client/mesh-install.py', '/usr/local/lib/nodehub/mesh-install', 0o700)
        install_file(folder / 'client/enable-overlayroot.example.sh', '/usr/local/lib/nodehub/enable-overlayroot', 0o700)
        import importlib.util
        spec = importlib.util.spec_from_file_location('nodehub_init', folder / 'client/nodehub-init.py')
        initializer = importlib.util.module_from_spec(spec); spec.loader.exec_module(initializer)
        if manifest['terminal_enabled']:
            run(['bash', str(folder / 'agent/install.sh'), str(folder / ('bin/nodehub-agent-linux-' + arch))])
            config_file = Path('/etc/nodehub-agent/config.json')
            config = json.loads(config_file.read_text())
            if Path(config['identity_file']).exists() and config['server'].rstrip('/') != server:
                raise RuntimeError('Existing Agent identity belongs to another center')
            config.update(server=server, heartbeat_seconds=manifest['heartbeat_seconds'])
            initializer.atomic_json(config_file, config)
            import pwd
            group = pwd.getpwnam('nodehub-agent').pw_gid
            os.chown(config_file, 0, group); os.chmod(config_file, 0o640)
        override = Path('/etc/systemd/system/nodehub-init.service.d/bootstrap.conf')
        command = '/usr/local/sbin/nodehub-init --mesh-install-hook /usr/local/lib/nodehub/mesh-install --auto-mesh --auto-finalize --overlay-hook /usr/local/lib/nodehub/enable-overlayroot'
        if manifest['terminal_enabled']: command += ' --agent-config /etc/nodehub-agent/config.json'
        initializer.atomic_write(override, '[Service]\nExecStart=\nExecStart=' + command + '\n', 0o644)
        # The older optional override sorts before this one; the new command resets ExecStart.
        run(['systemctl', 'daemon-reload'])
        if token is not None:
            initializer.atomic_json('/etc/nodehub/activation.json', {'server': server, 'token': token})
            token = None
        if args.prepare_image:
            print('Batch token saved with root-only permissions. Shut down and clone; first boot will enroll, clean up, enable root overlay, and reboot.')
            return
        # Keep terminal output visible, but never print token values or command credentials.
        result = subprocess.run(command.split())
        if result.returncode: raise RuntimeError('Initialization is incomplete; its durable state has been retained for retry')

if __name__ == '__main__':
    try: main()
    except (RuntimeError, OSError, ValueError, KeyError, subprocess.SubprocessError) as error:
        print('Bootstrap incomplete: ' + str(error), file=sys.stderr)
        sys.exit(1)
