#!/bin/sh
set -eu
[ "$(id -u)" = 0 ] || { echo 'Install as root' >&2; exit 1; }
[ "$#" = 1 ] || { echo 'Usage: agent/install.sh /path/to/nodehub-agent-linux-amd64' >&2; exit 1; }
agent_binary=$1
[ -f "$agent_binary" ] || { echo 'Agent binary does not exist' >&2; exit 1; }
agent_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
if ! id nodehub-agent >/dev/null 2>&1; then useradd --system --home-dir /var/lib/nodehub-agent --shell /usr/sbin/nologin nodehub-agent; fi
install -d -m 700 -o nodehub-agent -g nodehub-agent /var/lib/nodehub-agent
install -d -m 750 -o root -g nodehub-agent /etc/nodehub-agent
install -m 755 "$agent_binary" /usr/local/sbin/nodehub-agent
if [ ! -f /etc/nodehub-agent/config.json ]; then install -m 640 -o root -g nodehub-agent "$agent_dir/config.example.json" /etc/nodehub-agent/config.json; fi
install -m 644 "$agent_dir/nodehub-agent.service" /etc/systemd/system/nodehub-agent.service
systemctl daemon-reload
# Registration happens only after cloning, never on the template.
echo 'Agent installed, but not enabled or started. Configure the center URL, then enroll on the cloned device after registration.'
