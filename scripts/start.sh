#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
terminal_mode=false
case "${1:-}" in
  '') ;;
  --terminal) terminal_mode=true; shift ;;
  *) echo 'Usage: scripts/start.sh [--terminal]' >&2; exit 2 ;;
esac
[ "$#" -eq 0 ] || { echo 'Usage: scripts/start.sh [--terminal]' >&2; exit 2; }
if [ ! -f config/config.json ]; then
  echo 'Create config/config.json from an example and set the password hash and upstream addresses.' >&2
  exit 1
fi
# The read-only configuration must be readable by the container's UID 10001.
if [ "$(id -u)" = 0 ]; then
  chown 10001:10001 config/config.json
  chmod 600 config/config.json
fi
if [ "$terminal_mode" = true ]; then
  if [ ! -d secrets/ssh ]; then
    mkdir -p secrets/ssh
    if [ "$(id -u)" = 0 ]; then
      chown 10001:10001 secrets/ssh
      chmod 700 secrets/ssh
    fi
  fi
  docker compose -f compose.yaml -f compose.terminal.yaml up -d --build
else
  docker compose -f compose.yaml up -d --build
fi
