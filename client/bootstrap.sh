#!/bin/bash
set -euo pipefail
umask 077
if [ "$(id -u)" != 0 ]; then echo 'Run this command as root.' >&2; exit 1; fi
command -v curl >/dev/null || { echo 'Install curl first.' >&2; exit 1; }
command -v python3 >/dev/null || { echo 'Install python3 first.' >&2; exit 1; }
server=''
previous=''
for value in "$@"; do
    if [ "$previous" = '--server' ]; then server="$value"; fi
    previous="$value"
done
case "$server" in https://*) ;; *) echo 'Provide --server https://your-center.example.com' >&2; exit 1 ;; esac
work=$(mktemp -d /tmp/nodehub-bootstrap.XXXXXX)
trap 'rm -rf -- "$work"' EXIT
curl --fail --silent --show-error --proto '=https' --tlsv1.2 \
    "${server%/}/bootstrap/files/client/bootstrap.py" -o "$work/bootstrap.py"
python3 "$work/bootstrap.py" "$@"
