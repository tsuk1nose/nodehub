#!/usr/bin/env python3
"""Install and mount a bounded NetworkManager state overlay for read-only nodes."""
import argparse
import os
from pathlib import Path
import subprocess

STATE = Path('/var/lib/NetworkManager')
RUNTIME = Path('/run/nodehub-network-state')


def run(*args):
    return subprocess.check_output(args, text=True).strip()


def write_owned(path, content):
    path = Path(path)
    if path.is_symlink():
        raise RuntimeError('Refusing a symbolic-link configuration')
    if path.exists() and path.read_text() != content:
        raise RuntimeError('Existing configuration differs: ' + str(path))
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content)
    path.chmod(0o644)


def mount_state():
    # Maintenance boots use the real, writable state directory.
    if run('findmnt', '-n', '-o', 'FSTYPE', '--target', '/') != 'overlay':
        return
    if STATE.is_symlink() or not STATE.is_dir():
        raise RuntimeError('NetworkManager state must be an existing directory')
    if os.path.ismount(STATE):
        if run('findmnt', '-n', '-o', 'SOURCE', '--target', str(STATE)) != 'nodehub-network-state':
            raise RuntimeError('Unexpected mount on NetworkManager state')
        return
    if RUNTIME.is_symlink():
        raise RuntimeError('Unexpected runtime directory link')
    RUNTIME.mkdir(mode=0o700, exist_ok=True)
    if not os.path.ismount(RUNTIME):
        run('mount', '-t', 'tmpfs', '-o', 'size=16m,mode=0700,nosuid,nodev,noexec', 'nodehub-network-state-ram', str(RUNTIME))
    for name in ('lower', 'upper', 'work'):
        (RUNTIME / name).mkdir(mode=0o700, exist_ok=True)
    lower = RUNTIME / 'lower'
    if not os.path.ismount(lower):
        run('mount', '--bind', str(STATE), str(lower))
        run('mount', '-o', 'remount,bind,ro', str(lower))
    run('mount', '-t', 'overlay', '-o', f'lowerdir={lower},upperdir={RUNTIME}/upper,workdir={RUNTIME}/work,nosuid,nodev,noexec', 'nodehub-network-state', str(STATE))


def install():
    write_owned('/etc/systemd/journald.conf.d/60-nodehub-volatile.conf',
                '[Journal]\nStorage=volatile\nRuntimeMaxUse=64M\nRuntimeKeepFree=32M\n')
    if STATE.is_symlink():
        raise RuntimeError('NetworkManager state must be an existing directory')
    if not STATE.exists():
        return  # Systems without NetworkManager only need the logging policy.
    if not STATE.is_dir():
        raise RuntimeError('NetworkManager state must be an existing directory')
    # A missing secret_key is allowed; NetworkManager manages its own state.
    # Any identity first created in the RAM upper layer may change after reboot.
    write_owned('/etc/systemd/system/nodehub-network-state.service', '''[Unit]
Description=Bounded volatile NetworkManager state
DefaultDependencies=no
After=local-fs.target
Before=NetworkManager.service

[Service]
Type=oneshot
ExecStart=/usr/bin/python3 /usr/local/lib/nodehub/readonly-runtime --mount
RemainAfterExit=yes
''')
    write_owned('/etc/systemd/system/NetworkManager.service.d/nodehub-state.conf', '''[Unit]
Requires=nodehub-network-state.service
After=nodehub-network-state.service
''')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--mount', action='store_true')
    args = parser.parse_args()
    if os.geteuid() != 0:
        raise SystemExit('Run as root')
    if args.mount:
        mount_state()
    else:
        install()
