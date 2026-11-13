#!/usr/bin/env python3
"""Install the verified, unregistered MeshAgent binary after setting the hostname."""
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import time

def trusted_file(value):
    path = Path(value)
    if not path.is_absolute() or not path.is_file():
        raise RuntimeError('Installation files must exist and use absolute paths')
    stat = path.stat()
    if stat.st_uid != 0 or stat.st_mode & 0o022:
        raise RuntimeError('Installation files must be owned by root and not writable by other users')
    return path

def write_marker(path, value):
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    fd, name = tempfile.mkstemp(dir=path.parent)
    try:
        with os.fdopen(fd, 'w') as f:
            json.dump(value, f)
            f.flush()
            os.fsync(f.fileno())
        os.replace(name, path)
        directory = os.open(path.parent, os.O_DIRECTORY)
        try: os.fsync(directory)
        finally: os.close(directory)
    finally:
        if os.path.exists(name): os.unlink(name)

INSTALL_DIR = Path('/usr/local/mesh_services/meshagent')

def verify_service(directory=INSTALL_DIR):
    binary = directory / 'meshagent'
    if not binary.is_file() or binary.is_symlink():
        raise RuntimeError('The persistent MeshAgent executable is missing')
    command = subprocess.check_output(['systemctl', 'show', 'meshagent.service', '-p', 'ExecStart', '--value'], text=True)
    working = subprocess.check_output(['systemctl', 'show', 'meshagent.service', '-p', 'WorkingDirectory', '--value'], text=True).strip()
    if 'path=' + str(binary) + ' ' not in command or Path(working).resolve() != directory.resolve():
        raise RuntimeError('MeshAgent service does not reference its persistent installation')
    if not (directory / 'meshagent.msh').is_file() or not (directory / 'meshagent.db').is_file():
        raise RuntimeError('Persistent MeshAgent settings or identity database is missing')

def main():
    if os.geteuid() != 0: raise RuntimeError('Run the installer as root')
    serial = os.environ.get('NODEHUB_SERIAL', '')
    if not re.fullmatch('[A-Z0-9]{12}', serial): raise RuntimeError('A valid NODEHUB_SERIAL is required')
    config_path = trusted_file(sys.argv[1] if len(sys.argv) > 1 else '/etc/nodehub/mesh-install.json')
    config = json.loads(config_path.read_text())
    binary, settings = trusted_file(config['binary']), trusted_file(config['settings'])
    expected = config['sha256'].lower()
    if not re.fullmatch('[0-9a-f]{64}', expected) or hashlib.sha256(binary.read_bytes()).hexdigest() != expected:
        raise RuntimeError('MeshAgent binary SHA256 mismatch')
    # Ignore case because systemd may normalize the static hostname.
    hostname = subprocess.check_output(['hostname'], text=True).strip()
    if hostname.upper() != serial: raise RuntimeError('Set the system hostname to the serial before installing MeshAgent')
    marker = Path('/var/lib/nodehub-mesh/install.json')
    owned = json.loads(marker.read_text()) if marker.exists() else None
    unit = 'meshagent.service'
    exists = subprocess.run(['systemctl', 'show', unit, '-p', 'LoadState', '--value'], capture_output=True, text=True).stdout.strip() == 'loaded'
    if owned and owned.get('serial') != serial: raise RuntimeError('MeshAgent belongs to another node; refusing to overwrite it')
    if exists:
        if not owned: raise RuntimeError('An unmanaged MeshAgent installation exists; refusing to take it over')
        if subprocess.run(['systemctl', 'is-active', '--quiet', unit]).returncode == 0:
            verify_service()
            return
    lines = settings.read_text().splitlines()
    values = dict(line.split('=', 1) for line in lines if '=' in line)
    if not values.get('MeshServer', '').startswith('wss://') or not values.get('ServerID') or not values.get('MeshID'):
        raise RuntimeError('Device group settings lack a secure server URL or identity')
    lines = [line for line in lines if not line.startswith(('agentName=', 'StartupType='))]
    lines.extend(['StartupType=1', 'agentName=' + serial])
    # This binary's -install registers its current location as ExecStart.
    # Keep executable, settings, and identity database together permanently.
    staged = INSTALL_DIR
    if staged.is_symlink(): raise RuntimeError('MeshAgent installation must not be a symbolic link')
    if staged.exists() and not owned:
        raise RuntimeError('An unmanaged MeshAgent directory exists; refusing to overwrite it')
    write_marker(marker, {'serial': serial, 'source_sha256': expected, 'phase': 'installing'})
    staged.mkdir(mode=0o700, parents=True, exist_ok=True)
    os.chmod(staged, 0o700)
    for name in ('meshagent', 'meshagent.msh', 'meshagent.db'):
        if (staged / name).is_symlink(): raise RuntimeError('MeshAgent files must not be symbolic links')
    shutil.copyfile(binary, staged / 'meshagent')
    (staged / 'meshagent').chmod(0o700)
    (staged / 'meshagent.msh').write_text('\n'.join(lines) + '\n')
    (staged / 'meshagent.msh').chmod(0o600)
    result = subprocess.run(['./meshagent', '-install', '--copy-msh=1', '--agentName=' + serial], cwd=staged, capture_output=True, text=True, timeout=120)
    if result.returncode:
        raise RuntimeError('MeshAgent installation failed with exit code ' + str(result.returncode))
    subprocess.run(['systemctl', 'daemon-reload'], check=True)
    subprocess.run(['systemctl', 'restart', unit], check=True)
    for _ in range(30):
        if (staged / 'meshagent.db').is_file(): break
        time.sleep(0.5)
    verify_service()
    if subprocess.run(['systemctl', 'is-active', '--quiet', unit]).returncode:
        raise RuntimeError('MeshAgent is not running after installation; check the systemd journal')
    write_marker(marker, {'serial': serial, 'source_sha256': expected, 'phase': 'installed'})

if __name__ == '__main__':
    try:
        main()
    except (RuntimeError, OSError, ValueError, KeyError, subprocess.SubprocessError) as error:
        print('MeshAgent installation incomplete: ' + str(error), file=sys.stderr)
        sys.exit(1)
