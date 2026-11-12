#!/bin/sh
set -eu
[ "$(id -u)" = 0 ] || { echo 'Run as root' >&2; exit 1; }
cd "$(dirname "$0")"
install -d -m 700 /etc/nodehub /var/lib/nodehub-init /usr/local/lib/nodehub
install -m 755 nodehub-init.py /usr/local/sbin/nodehub-init
install -m 644 nodehub-init.service /etc/systemd/system/nodehub-init.service
systemctl daemon-reload
# Enable only: do not register the template before shutdown and cloning.
systemctl enable nodehub-init.service
echo 'Installed without starting. Provide activation.json and the MeshAgent hook, then seal and shut down the template.'
