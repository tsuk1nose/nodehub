# Debian 13 root OverlayFS plan

English | [简体中文](zh-CN/docs/overlayroot.md)

Date: 2026-11-13. This is a device-inspected plan and a reviewed example hook, **not a completed reboot or failure-mode acceptance test**. No remote mount, fstab, boot, or identity settings were changed during inspection.

## 1. Observed template state

The inspected Intel N2840 template has approximately 4 GB RAM, a separate FAT EFI partition, an approximately 15 GB ext4 root, and an approximately 44 GB Btrfs `/data`. The root is writable and `/data` is mounted read-write. No NoCoW flag is set on the `/data` directory; Btrfs CoW is already the default, so do not reformat or run `chattr +C`.

`overlayroot` 0.18.debian14 and `initramfs-tools` are installed. The running kernel's initramfs includes both the OverlayFS module and `scripts/init-bottom/overlayroot`. `/etc/overlayroot.conf` disables overlay by default. The template has no Node Hub enrollment or Agent identity. These are observations, not a requirement to copy its UUIDs or host identity.

The current `/data` fstab entry uses `defaults` without `nofail`; that does **not** satisfy the requirement that its failure must not block boot.

## 2. Recommended layout

- Keep root ext4 as the read-only lower layer after initialization.
- Use an ephemeral RAM upper layer, discarded at reboot.
- Use `recurse=0` so independent filesystems, especially `/data`, are not also made read-only.
- Keep `/data` as Btrfs with CoW and a nonblocking mount policy.
- Keep Tailscale and native Agent identities at existing root paths; install MeshAgent entirely on mounted `/data`.

Use `/etc/overlayroot.local.conf`, rather than editing the package-managed configuration or changing root's fstab type to `overlay`:

```sh
overlayroot="tmpfs:recurse=0,swap=0"
overlayroot_cfgdisk="disabled"
```

The installed init-bottom script reads the real root's configuration at boot. Since this device's initramfs already includes overlayroot, a configuration change on the durable root is the essential enable step. Rebuild initramfs after initially installing/changing relevant packages, and verify the kernel/boot entry that will actually be used. An fstab edit alone is not a complete root overlay setup.

The package's tmpfs defaults to a maximum around half available RAM, not a preallocated reservation. Bound logs/caches and measure business workload memory before production. Consider a template journald policy such as `Storage=volatile` and `RuntimeMaxUse=64M`; ordinary root writes disappear at reboot. Separate EFI and other mounts are not protected by `recurse=0` root overlay.

## 3. Work to do during OS/image preparation

Preinstall the following through your Debian package process:

```sh
apt-get install overlayroot initramfs-tools btrfs-progs python3 curl ca-certificates openssh-server
update-initramfs -u -k all
```

Preinstall Tailscale separately using the deployment's chosen version/source. Keep overlay disabled throughout installation and image sealing. Retain the separate ext4/Btrfs layout already created; no repartition or format is needed.

Change only the `/data` entry in the template's real `/etc/fstab`, retaining its actual UUID:

```fstab
UUID=REPLACE_WITH_DATA_UUID /data btrfs defaults,nofail,x-systemd.device-timeout=5s,x-systemd.mount-timeout=15s 0 0
```

Validate syntax using `findmnt --verify --verbose`, then `systemctl daemon-reload`. Choose timeouts that fit actual hardware; they bound waiting, not repair a damaged filesystem. With `nofail`, business services should explicitly depend on `/data` if required (for example `RequiresMountsFor=/data`), while Tailscale, SSH, and Node Hub Agent remain independent. MeshAgent explicitly depends on `/data` and remains stopped when it is missing. Business tasks must not write into an unmounted fallback directory. Do not add automount casually: it changes when access may block.

Seal machine-id and SSH host identities correctly for cloning. Do not retain registered Tailscale/MeshAgent state, Node Hub state/keys, or an already initialized host. `--prepare-image` installs Node Hub tools without enrollment. The preparation command prompts for and saves the shared batch token for unattended clone first boot.

## 4. Automatic activation after cloning

Use HTTPS bootstrap with `--prepare-image` on the template, enter the batch token, shut down and clone. The enabled first-boot service obtains a serial, configures hostname/Tailscale/MeshAgent/native Agent, and requests automatic MeshCentral association. A separate finalizer prepares root-only RAM overlay, adds `nofail` and bounded waits to the existing Btrfs `/data` mount, removes bootstrap programs/configuration/state/secrets, and reboots. It refuses a different existing overlay configuration or CoW-disabled mount options. No filesystem is reformatted.

After reboot, verify root reports overlay, `/data` remains writable Btrfs, all remote channels reconnect, and identities survive multiple reboots. Test missing `/data` with a recovery console. The center's `awaiting_overlay` stage denotes pending boot acceptance; this implementation does not remotely attest successful overlay activation. The older manual finalization switches remain for custom installations, but the HTTPS path requires no operator confirmation.

## 5. Maintenance and rollback

For a temporary durable maintenance boot, add `overlayroot=disabled` to the chosen kernel's command line at GRUB for that boot. This overrides configuration. Keep a console/recovery path until tested. Upgrade packages, Agents, service configuration, and identity baselines in durable maintenance mode, then return to normal overlay boot.

The installed `overlayroot-chroot` tool can access the lower root for controlled maintenance/recovery. It remounts lower storage writable; it is not an automatic persistence mechanism for live service state. Prefer a deliberate durable boot for package/service changes, and do not copy the entire active upper layer into the baseline.

## 6. Mutable service identity: unresolved long-term policy

A RAM upper layer preserves the initial lower-layer keys but discards **later** writes at reboot. Tailscale state/key renewal and other root-path changes may therefore roll back; MeshAgent writes now persist on `/data`. Keeping Tailscale/native Agent credentials at their root paths does not make later writes durable.

For the initial test plan, establish and validate a durable baseline and perform controlled maintenance boots. Before unattended production, choose and test a durable update policy. A separate persistent system-state partition with bind mounts at default service paths is a possible future layout without using `/data`, but it needs an explicit disk-layout decision and has not been installed. If reinstalling the OS anyway, reserving roughly 2–4 GB for that separate system-state filesystem is a candidate to discuss before allocating all remaining space to Btrfs `/data`. Bind service state at its existing default paths, order only the dependent services after that mount, and test missing-state/rollback behavior. This would require a separate migration plan; it is not a request to shrink or repartition the currently inspected disk. The approved exception is MeshAgent on `/data`; other service paths remain unchanged.

## References

- The inspected package's `/etc/overlayroot.conf`, initramfs hook, init-bottom script, and `/usr/sbin/overlayroot-chroot` are the version-specific primary evidence.
- [Debian overlayroot maintenance command](https://manpages.debian.org/trixie/overlayroot/overlayroot-chroot.8.en.html)
- [systemd mount/fstab semantics, including nofail and timeouts](https://manpages.debian.org/trixie/systemd/systemd.mount.5.en.html)

## Current storage policy

Only MeshAgent's complete installation directory is stored on node `/data`, at `/data/nodehub/meshagent`. `/data` must be a mounted, writable Btrfs filesystem during initialization. The systemd drop-in uses `RequiresMountsFor=/data`, `BindsTo=data.mount`, a mount-point condition, and an explicit pre-start mount check. Missing `/data` prevents MeshAgent startup without preventing OS boot; no fallback identity is created. Keep `/data` mounted with `nofail` and bounded timeouts.

Tailscale and native Agent identities retain their existing root paths. The initializer does not enable a service to make the root OverlayFS writable. The previous writable-overlay workaround is superseded for new installations; existing test nodes are not migrated automatically. Persistent MeshAgent directory mode is 0700, and settings are 0600; `/data/nodehub` must be root-owned and not writable by other users. This new storage arrangement requires fresh-device acceptance, including multiple reboots and missing-/data boot. Tailscale renewal/relogin on a read-only root is not validated by MeshAgent tests.
