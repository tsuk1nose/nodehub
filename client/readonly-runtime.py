#!/usr/bin/env python3
"""Keep the root read-only and configure bounded temporary writable directories."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import stat
import subprocess
import tomllib

CONFIG = Path('/etc/nodehub/readonly-runtime.toml')
RUNTIME = Path('/run/nodehub-temporary-writes')
DEFAULT_TOML = """# Temporary writable directories on a read-only root.
# Changes are stored in RAM and discarded on reboot.
# Create directories before imaging. Entries must not overlap.
# memory_mb limits capacity; optional skips missing directories.
# allow_exec defaults to false. Enable it only for executable directories.

# NetworkManager runtime state.
[[temporary_directories]]
path = "/var/lib/NetworkManager"
memory_mb = 16
optional = true

# MeshAgent executable, settings, and identity database.
[[temporary_directories]]
path = "/usr/local/mesh_services/meshagent"
memory_mb = 64
optional = true
allow_exec = true
"""
DEFAULT = tomllib.loads(DEFAULT_TOML)


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


def load_policy():
    if CONFIG.is_symlink():
        raise RuntimeError('Runtime configuration must not be a symbolic link')
    policy = tomllib.loads(CONFIG.read_text()) if CONFIG.exists() else DEFAULT
    if not isinstance(policy, dict) or set(policy) != {'temporary_directories'} or not isinstance(policy['temporary_directories'], list):
        raise RuntimeError('Expected temporary_directories configuration list')
    result = []
    for item in policy['temporary_directories']:
        if not isinstance(item, dict) or set(item) - {'path', 'memory_mb', 'optional', 'allow_exec'}:
            raise RuntimeError('Invalid temporary directory entry')
        value = item.get('path')
        size = item.get('memory_mb', 16)
        optional = item.get('optional', False)
        allow_exec = item.get('allow_exec', False)
        if not isinstance(value, str) or not value.startswith('/') or value == '/' or str(Path(value)) != value or any(c in value for c in ',:\\ \t\n') or '..' in value.split('/'):
            raise RuntimeError('Temporary directory must be a normalized absolute directory path')
        if any(value == prefix or value.startswith(prefix + '/') for prefix in ('/run', '/dev', '/proc', '/sys', '/data', '/media')):
            raise RuntimeError('Temporary directory must not cover runtime, system, or persistent mounts')
        if type(size) is not int or not 1 <= size <= 1024 or type(optional) is not bool or type(allow_exec) is not bool:
            raise RuntimeError('memory_mb must be 1..1024 and optional/allow_exec must be boolean')
        path = Path(value)
        # Reject links in any ancestor, not only at the mountpoint itself.
        if path.resolve() != path:
            raise RuntimeError('Temporary directory must not contain symbolic links')
        if any(path == prior['path'] or path in prior['path'].parents or prior['path'] in path.parents for prior in result):
            raise RuntimeError('Temporary directories must not overlap')
        if path.exists() and not path.is_dir():
            raise RuntimeError('Temporary writable paths must be directories')
        if not path.exists() and not optional:
            raise RuntimeError('Create the temporary directory in maintenance mode first: ' + value)
        result.append({'path': path, 'memory_mb': size, 'optional': optional, 'allow_exec': allow_exec})
    return result


def root_mount():
    info = json.loads(run('findmnt', '--json', '-o', 'SOURCE,FSTYPE,OPTIONS', '--target', '/'))
    return info['filesystems'][0]


def make_root_readonly(root):
    options = dict(item.partition('=')[::2] for item in root['options'].split(','))
    if root['source'] != 'overlayroot' or options.get('lowerdir') != '/media/root-ro':
        raise RuntimeError('Unexpected root overlay')
    upper = options.get('upperdir', '')
    if not upper.startswith('/media/root-rw/') or '..' in upper.split('/'):
        raise RuntimeError('Unexpected root overlay upper directory')
    if run('findmnt', '-n', '-o', 'FSTYPE', '--target', upper) != 'tmpfs':
        raise RuntimeError('Root upper directory must be on tmpfs')
    if 'ro' not in run('findmnt', '-n', '-o', 'OPTIONS', '--target', '/media/root-ro').split(','):
        raise RuntimeError('Disk baseline must remain read-only')
    if 'ro' not in options:
        run('mount', '-t', 'overlay', '-o', 'remount,ro', 'overlayroot', '/')
    if 'ro' not in root_mount()['options'].split(','):
        raise RuntimeError('Root overlay is still writable')


def mount_directory(entry):
    path = entry['path']
    if not path.exists() and entry['optional']:
        return
    tag = hashlib.sha256(str(path).encode()).hexdigest()[:16]
    source = 'nodehub-temporary-' + tag
    if os.path.ismount(path):
        if run('findmnt', '-n', '-o', 'SOURCE', '--target', str(path)) != source:
            raise RuntimeError('Unexpected existing mount: ' + str(path))
        return
    execution = 'exec' if entry.get('allow_exec', False) else 'noexec'
    base = RUNTIME / tag
    if base.resolve() != base:
        raise RuntimeError('Unexpected runtime directory link')
    base.mkdir(mode=0o700, parents=True, exist_ok=True)
    if not os.path.ismount(base):
        run('mount', '-t', 'tmpfs', '-o', f"size={entry['memory_mb']}m,mode=0700,nosuid,nodev,{execution}", source + '-ram', str(base))
    for name in ('lower', 'upper', 'work'):
        (base / name).mkdir(mode=0o700, exist_ok=True)
    lower = base / 'lower'
    if not os.path.ismount(lower):
        run('mount', '--bind', str(path), str(lower))
        run('mount', '-o', 'remount,bind,ro', str(lower))
    original = path.stat()
    os.chown(base / 'upper', original.st_uid, original.st_gid)
    os.chmod(base / 'upper', stat.S_IMODE(original.st_mode))
    run('mount', '-t', 'overlay', '-o', f'lowerdir={lower},upperdir={base}/upper,workdir={base}/work,nosuid,nodev,{execution}', source, str(path))


def mount_state():
    root = root_mount()
    if root['fstype'] != 'overlay':
        return  # Persistent maintenance boots use the original directories.
    policy = load_policy()  # Validate every entry before changing mounts.
    make_root_readonly(root)
    for entry in policy:
        mount_directory(entry)


def remove_legacy_service():
    # Only migrate the known NodeHub units; preserve unrelated/custom files.
    files = {
        '/etc/systemd/system/nodehub-root-writable.service': '--root-writable',
        '/etc/systemd/system/nodehub-network-state.service': 'readonly-runtime --mount',
        '/etc/systemd/system/NetworkManager.service.d/nodehub-state.conf': 'Requires=nodehub-network-state.service',
    }
    for name, marker in files.items():
        path = Path(name)
        if path.is_symlink() or (path.exists() and marker not in path.read_text()):
            raise RuntimeError('Review customized legacy runtime unit: ' + name)
    link = Path('/etc/systemd/system/local-fs.target.requires/nodehub-root-writable.service')
    if link.is_symlink() and str(link.readlink()) == '/etc/systemd/system/nodehub-root-writable.service':
        link.unlink()
    elif link.exists() or link.is_symlink():
        raise RuntimeError('Review customized legacy runtime service link')
    for name in files:
        Path(name).unlink(missing_ok=True)


def install():
    if root_mount()['fstype'] == 'overlay':
        raise RuntimeError('Install runtime policy in persistent maintenance mode')
    policy = load_policy()
    mesh = Path('/usr/local/mesh_services/meshagent')
    if mesh.exists() and not any(item['path'] == mesh and item['allow_exec'] for item in policy):
        raise RuntimeError('Configure the MeshAgent directory with allow_exec=true before finalizing')
    if not CONFIG.exists():
        write_owned(CONFIG, DEFAULT_TOML)
    remove_legacy_service()
    write_owned('/etc/systemd/journald.conf.d/60-nodehub-volatile.conf',
                '[Journal]\nStorage=volatile\nRuntimeMaxUse=64M\nRuntimeKeepFree=32M\n')
    write_owned('/etc/systemd/system/nodehub-temporary-writes.service', '''[Unit]
Description=Configured temporary writable directories on read-only root
DefaultDependencies=no
After=systemd-remount-fs.service
Before=local-fs-pre.target local-fs.target systemd-tmpfiles-setup.service

[Service]
Type=oneshot
ExecStart=/usr/bin/python3 /usr/local/lib/nodehub/readonly-runtime --mount
RemainAfterExit=yes

[Install]
RequiredBy=local-fs.target
''')
    link = Path('/etc/systemd/system/local-fs.target.requires/nodehub-temporary-writes.service')
    target = Path('/etc/systemd/system/nodehub-temporary-writes.service')
    if link.is_symlink():
        if link.readlink() != target:
            raise RuntimeError('Existing runtime service link differs')
    elif link.exists():
        raise RuntimeError('Refusing to replace existing runtime service link')
    else:
        link.parent.mkdir(parents=True, exist_ok=True)
        link.symlink_to(target)


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
