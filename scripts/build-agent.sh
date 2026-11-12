#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
agent_arch=${1:-amd64}
case "$agent_arch" in amd64|arm64) ;; *) echo 'Supported architectures: amd64 or arm64' >&2; exit 1 ;; esac
mkdir -p bin
CGO_ENABLED=0 GOOS=linux GOARCH="$agent_arch" go build -buildvcs=false -trimpath -ldflags='-s -w' -o "bin/nodehub-agent-linux-$agent_arch" ./cmd/nodehub-agent
