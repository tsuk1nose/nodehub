# Read-only root and temporary writable directories

English | [简体中文](zh-CN/docs/readonly-runtime.md)

The merged root stays read-only. Only configured directories receive bounded RAM overlays, preserving baseline files while discarding runtime modifications at reboot. `/etc` is read-only unless one of its directories is explicitly configured.

## Configuration

HTTPS bootstrap installs `/usr/local/lib/nodehub/readonly-runtime`. Installation immediately creates `/etc/nodehub/readonly-runtime.toml` on the persistent baseline for editing before imaging; finalization reads it and installs the boot mount service. Defaults include optional overlays for NetworkManager (16 MiB) and `/usr/local/mesh_services/meshagent` (64 MiB, executable); `secret_key` is not required.

```toml
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

# Optional application state (create this directory before imaging).
[[temporary_directories]]
path = "/etc/animactrl"
memory_mb = 4
```

Create directories first with ownership and permissions appropriate for the actual service. The example state directory must match the service's StateDirectory. Paths must be existing absolute directories without symlinks; duplicate or nested entries are rejected. Single files are not supported. `memory_mb` is the per-directory tmpfs capacity limit (1–1024 MiB), not an upfront reservation. `optional: true` skips a missing directory. `/`, `/run`, `/dev`, `/proc`, `/sys`, `/data`, `/media` and their descendants cannot be configured. Avoid covering the entire `/var/lib`, which would also discard credential renewals.

## Installation and updates

Before sealing, or in a persistent `overlayroot=disabled` maintenance boot, edit the configuration and run:

```sh
python3 /usr/local/lib/nodehub/readonly-runtime
systemctl daemon-reload
```

Install the updated helper first on existing nodes. Only TOML is read; legacy JSON is not imported or migrated. Custom lists must explicitly include the installed MeshAgent directory with allow_exec=true. Setup otherwise preserves user configuration and migrates known legacy NodeHub root-writable and NetworkManager mount services. It does not switch live mounts or restart networking. Reboot into OverlayFS to apply changes. Edits made only in RAM do not persist.

The required `nodehub-temporary-writes.service` runs after systemd root remounting and before local filesystem preparation and normal services. It verifies a read-only disk baseline and tmpfs root upper, ensures the merged root is read-only, and mounts each configured directory. Configuration or mount failures block normal local filesystem startup; have a recovery console available. Non-overlay maintenance boots leave mounts unchanged.

Each overlay preserves directory ownership, permissions and baseline files, with nosuid/nodev RAM storage; execution defaults to disabled and `allow_exec: true` enables it on both tmpfs and overlay mounts. Storage is under `/run/nodehub-temporary-writes`. Journald logs are volatile under `/run`, with RuntimeMaxUse=64M and RuntimeKeepFree=32M. MeshAgent identity is saved in each node’s root baseline before enabling OverlayFS. Legacy `/data` installations are not migrated automatically; test on a fresh template. Other service identity paths are unchanged.

Credentials or refreshed tokens that must survive reboot need dedicated persistent storage on `/data`.

## Acceptance

Verify `/` is `ro` and configured directories are `rw`. Use separate test files to verify reboot discards changes and restores baseline files. Check networking, hotspot, business services and management connectivity. Exercise missing directories, mount failure and capacity limits. Unit tests do not replace real-device reboot acceptance.
