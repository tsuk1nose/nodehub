# Debian 13 initialization manual

English | [简体中文](../docs/zh-CN/client/README.md)

For a copyable HTTPS command with local token input, use the [bootstrap installer](../docs/bootstrap.md). For the inspected Debian layout and an example finalization hook, see the [OverlayFS plan](../docs/overlayroot.md).

The initializer supports recoverable registration and Tailscale enrollment, a verified local MeshAgent binary hook, and optional automatic root SSH provisioning. The HTTPS path saves the batch token with `--prepare-image`; clones automatically associate MeshCentral, clean bootstrap files, enable root OverlayFS, and reboot. `/data` must already be Btrfs with CoW; the finalizer adds non-blocking mount options without repartitioning.

## Prepare the template

1. Install Python 3, CA certificates, Tailscale, systemd, networking, and correct time. For native terminals, also install OpenSSH server and the Agent.
2. Seal machine-id/SSH identity according to your Debian image process. Do not retain registered Tailscale/MeshAgent identities. Complete multi-clone acceptance remains pending; do not clone a production node's state.
3. As root, run `client/install.sh`. It enables but does not start the first-boot service.
4. Install root-owned, mode-600 `/etc/nodehub/activation.json` from the example. A center-issued unlimited token may be reused across a batch of images.
5. Provide executable, root-owned `/usr/local/lib/nodehub/mesh-install`, not writable by others. It receives `NODEHUB_SERIAL`, must be retry-safe, and must not expose management credentials in output/arguments.
6. Ensure `/var/lib/nodehub-init` and the Agent state contain no generated device identities or progress. Shut down and clone without starting initialization.

## First boot

The service waits for networking and retries failure every 60 seconds. Before contacting the center, it durably stores an independent request ID/credential. After verifying authenticated recovery, it removes the local activation file; this does not revoke other copies of a shared token.

It sets hostname and `/etc/hosts`, requests a Headscale key, and joins Tailscale using a temporary key file removed afterward. Service identities remain at default root paths. After the MeshAgent hook succeeds, progress prevents repeat installation.

Administrators verify the external MeshCentral Node ID and bind it in node details. Binding checks the group/name/ID and does not select a device solely by name.

## Explicit finalization

Prepare a trusted, root-owned executable OverlayFS hook that:

- Runs idempotently while root is still durably writable.
- Prepares OverlayFS for the **next boot**, without rebooting itself or immediately switching to an ephemeral layer.
- Preserves default service credentials, hostname, machine-id, and SSH identity.
- Uses CoW for `/data` and lets the system boot if that mount fails.
- Defines maintenance and runtime credential-update behavior.

Run from an existing remote/local terminal, not the first-boot unit:

```sh
/usr/local/sbin/nodehub-init --finalize --overlay-hook /usr/local/lib/nodehub/enable-overlay
```

Both upstream bindings are required. The tool records awaiting-reboot state, stops/disables/removes the first-boot unit and optional `agent.conf`/`bootstrap.conf`, removes activation and installed initializer, flushes writes, runs the next-boot hook, then reboots. Cleanup precedes overlay activation. Keep independent recovery state at `/var/lib/nodehub-init/state.json`; do not retain the shared activation secret.

If power fails during cleanup, restore the same tool through MeshCentral/external media and resume with the existing state. Do not erase state and register again. Full unattended power-loss acceptance is still pending.

After reboot verify root mount, hostname, service identity/connectivity, and `/data` degradation. Temporarily supply the same trusted tool and run `--confirm-after-reboot` after operator verification; it checks that root is overlay before recording completion. Remove that recovery copy afterward. Its checks do not replace storage/connectivity acceptance.

## Storage boundary

The initializer does not create or mount `/data`. MeshAgent is installed entirely at `/data/nodehub/meshagent` and requires that writable Btrfs mount; Tailscale and native Agent paths remain unchanged. Establish the root filesystem's persistent identity baseline before OverlayFS.

## Verified MeshAgent binary hook

Install `mesh-install.py` as `/usr/local/lib/nodehub/mesh-install` (root-owned mode 700). Copy `mesh-install.example.json` to `/etc/nodehub/mesh-install.json` (mode 600). Configure absolute paths to the original unregistered Linux binary and group `.msh`, plus its verified SHA256. Root must own the files; other users must not be able to write them.

The tested binary requires `-install --copy-msh=1 --agentName=<serial>`; `-fullinstall` can return success without installing. The hook stages settings with `agentName`/`StartupType=1`, verifies `meshagent.service` is active, and records resumable ownership. It refuses to take over an unrelated existing Agent.

Display names use the serial; MeshCentral internal IDs remain generated. Hostname changes affect newly started Agents without logging into another PTY. Never clone the installed database/certificate.

## Automatic root terminal

Preinstall Agent/OpenSSH without generating an Agent identity. Enable the center terminal, configure `/etc/nodehub-agent/config.json`, then initialize with:

```sh
/usr/local/sbin/nodehub-init \
  --mesh-install-hook /usr/local/lib/nodehub/mesh-install \
  --agent-config /etc/nodehub-agent/config.json
```

The tool enrolls Agent identity, generates missing SSH host keys, reports the host public key, receives a per-node center-generated login public key, and preserves existing root authorization while adding a managed block. Installation is durably confirmed before Agent startup. Do not add per-node center configuration.

The shell runs directly as root, without sudo. Agent identity and SSH keys remain separate. Retries cannot replace established identities; keep original state. Validate the terminal before OverlayFS/finalization. See the [protocol and upgrade manual](../docs/native-terminal.md).

## Enable automatic root terminals in the template

After installing/configuring Agent and enabling terminal support centrally, install this template-wide drop-in once from the repository root:

```sh
install -D -m 644 client/nodehub-init-agent.conf.example \
  /etc/systemd/system/nodehub-init.service.d/agent.conf
systemctl daemon-reload
```

Do not start initialization or enroll the template. Confirm no generated identities remain, then shut down and clone. Every clone receives `--agent-config` through the same service override. Activation and MeshAgent settings are still required.

If not using native terminals, omit the drop-in. For old deployments, upgrade the center and initializer first, then retry with original state. Check the effective ExecStart and verify `id -un` returns root before finalizing. Finalization removes the provided `agent.conf` along with the first-boot entrypoint.
