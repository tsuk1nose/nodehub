# Read-only node runtime directories

English | [简体中文](zh-CN/docs/readonly-runtime.md)

The HTTPS initializer installs `/usr/local/lib/nodehub/readonly-runtime`. Before enabling root overlay and cleaning up initialization tools, the finalizer runs this helper to configure the next boot. It is a retained boot component, not an enrollment program, and contains no token.

## NetworkManager

If `/var/lib/NetworkManager` exists, finalization requires its existing `secret_key` to be a regular file. Establish NetworkManager identity on each initialized clone before sealing its root baseline; do not cover the directory with an empty tmpfs or generate a fresh identity every boot. Node machine identities must also follow the image-sealing procedure.

On overlay-root boots, `nodehub-network-state.service` bind-mounts the existing directory read-only under `/run/nodehub-network-state/lower`, then mounts an overlay at `/var/lib/NetworkManager`. Upper/work directories use a dedicated 16 MiB tmpfs with restrictive permissions. This preserves baseline files while allowing DHCP leases and other runtime changes. Writes disappear at reboot; changes to NetworkManager state that must persist require maintenance mode. Wi-Fi profiles and credentials must retain their independently configured persistent storage; this helper does not relocate profiles.

The NetworkManager service requires the mount service and starts after it. A missing baseline identity or failed mount prevents NetworkManager startup instead of proceeding with an unusable state directory. NetworkManager restarts retain the same upper layer until OS reboot. On a non-overlay maintenance boot the helper does nothing, so updates reach the persistent directory. Systems without this directory receive only the logging policy; installing NetworkManager later requires running setup in maintenance mode.

## Logs and other storage

`/etc/systemd/journald.conf.d/60-nodehub-volatile.conf` sets `Storage=volatile`, `RuntimeMaxUse=64M`, and `RuntimeKeepFree=32M`. Journald writes to `/run/log/journal`; logs disappear at reboot. These are journald retention controls, not a global limit on `/run` or other log writers. Existing administrator files with different contents are not overwritten. The new policy applies on reboot; setup does not restart active network or logging services.

`/run` and `/tmp` retain the OS mounts and limits. No new writable mount is added to `/var/tmp`, `/var/log`, `/etc`, or the whole `/var/lib`. MeshAgent remains on `/data/nodehub/meshagent`; Tailscale and native Agent paths remain unchanged. The root overlay is not remounted writable. This change does not resolve the separate systemd root-remount, apt, GRUB-recording, or wtmpdb policies.

## Acceptance on a new image

Rebuild the center image so its installer manifest includes `client/readonly-runtime.py`, then prepare a new template normally. Manual initializer installations must also install this file as `/usr/local/lib/nodehub/readonly-runtime` (root-owned, mode 0700) before calling the overlay hook.

After reboot check:

```sh
systemctl status nodehub-network-state NetworkManager --no-pager
findmnt --target /var/lib/NetworkManager
findmnt --target /run/nodehub-network-state
findmnt --target /
journalctl --disk-usage
```

Verify the root remains read-only, the NetworkManager overlay is writable, the tmpfs limit is 16 MiB, and hotspot DHCP clients receive leases. Exercise hotspot stop/start, client disconnect/reconnect, NetworkManager restart, and OS reboot. Verify the baseline secret and machine identity stay stable and saved Wi-Fi profiles survive. Test mount failure with a local recovery console: NetworkManager must not start. A full 16 MiB upper layer causes writes to fail; it does not provide unlimited memory or guarantee hotspot recovery.

Unit tests cover maintenance bypass, missing identity refusal, mount ordering/size policy, service dependencies, and configuration preservation. Real NetworkManager/hotspot boot acceptance remains pending. This repository does not contain the hotspot application shown in the reported screenshot; its false-running status and retry/recovery logic must be addressed in that application separately.
