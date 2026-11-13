#!/bin/sh
# Reviewed example hook: prepare only the next boot; never reboot here.
# Installed by HTTPS bootstrap and invoked by the automatic finalizer.
set -eu
[ "$(id -u)" = 0 ] || { echo 'Run as root.' >&2; exit 1; }
[ "$(findmnt -n -o FSTYPE --target /)" != overlay ] || { echo 'A persistent writable root is required.' >&2; exit 1; }
[ -f /usr/share/initramfs-tools/scripts/init-bottom/overlayroot ] || { echo 'Install overlayroot first.' >&2; exit 1; }
# Preserve the existing filesystem and options; only make /data non-blocking.
python3 - <<'PYDATA'
import os
from pathlib import Path
import tempfile
path = Path('/etc/fstab')
lines = path.read_text().splitlines(keepends=True)
matches = [(i, line.split()) for i, line in enumerate(lines) if line.strip() and not line.lstrip().startswith('#') and len(line.split()) >= 4 and line.split()[1] == '/data']
if len(matches) != 1 or matches[0][1][2] != 'btrfs':
    raise SystemExit('Exactly one existing Btrfs /data fstab entry is required')
i, fields = matches[0]
options = fields[3].split(',')
if 'nodatacow' in options:
    raise SystemExit('/data must use CoW; remove nodatacow during image preparation')
options = [option for option in options if not option.startswith(('x-systemd.device-timeout=', 'x-systemd.mount-timeout='))]
for option in ('nofail', 'x-systemd.device-timeout=5s', 'x-systemd.mount-timeout=15s'):
    if option not in options: options.append(option)
fields[3] = ','.join(options)
lines[i] = '\t'.join(fields) + '\n'
if ''.join(lines) != path.read_text():
    fd, pending = tempfile.mkstemp(dir='/etc', prefix='.nodehub-fstab-')
    try:
        with os.fdopen(fd, 'w') as stream:
            os.fchmod(stream.fileno(), path.stat().st_mode & 0o777)
            stream.write(''.join(lines)); stream.flush(); os.fsync(stream.fileno())
        os.replace(pending, path)
    finally:
        Path(pending).unlink(missing_ok=True)
PYDATA
initrd="/boot/initrd.img-$(uname -r)"
[ -r "$initrd" ] || { echo 'The running kernel initramfs is missing.' >&2; exit 1; }
lsinitramfs "$initrd" | grep -q '^scripts/init-bottom/overlayroot$' || {
    echo 'Rebuild and verify initramfs with overlayroot before finalizing.' >&2; exit 1;
}
pending=$(mktemp /etc/.nodehub-overlayroot.XXXXXX)
trap 'rm -f -- "$pending"' EXIT
printf '%s\n' 'overlayroot="tmpfs:recurse=0,swap=0"' 'overlayroot_cfgdisk="disabled"' > "$pending"
chmod 600 "$pending"
if [ -e /etc/overlayroot.local.conf ] || [ -L /etc/overlayroot.local.conf ]; then
    [ ! -L /etc/overlayroot.local.conf ] && cmp -s "$pending" /etc/overlayroot.local.conf || {
        echo 'Existing overlayroot.local.conf differs; review it instead of overwriting.' >&2; exit 1;
    }
else
    mv "$pending" /etc/overlayroot.local.conf
fi
sync
echo 'Next boot prepared: RAM overlay for root; independent mounts remain writable. No reboot performed by this hook.'
