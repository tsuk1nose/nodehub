# HTTPS node installer

English | [简体中文](zh-CN/docs/bootstrap.md)

The center now distributes its initializer, service files, optional Agent, and a checksum manifest over HTTPS. The enrollment page displays a copyable command based on the browser's current origin. No actual deployment URL or token is embedded in the repository.

## 1. Center setup

New configuration examples include:

```json
"bootstrap": {
  "enabled": true,
  "assets_dir": "/app/bootstrap"
}
```

Add this section explicitly to an existing configuration. Rebuild/recreate the center image: the Dockerfile now packages the client scripts and Linux amd64/arm64 native Agent binaries. Local development can set `assets_dir` to the repository root after building the binaries in `bin/`.

The normal **automatic MeshAgent enrollment installer currently targets Debian 13 amd64**, matching N2840 nodes. Other architectures retain the manual reviewed-hook flow; Agent binary availability alone does not imply automatic MeshAgent support.

Required on the image: Python 3, curl, CA certificates, Tailscale, systemd, and OpenSSH server. Network access and correct time must support HTTPS verification. The bootstrap does not modify partitions, create Btrfs, install Tailscale from an unreviewed external script, or install missing prerequisites. Install `overlayroot` and rebuild/check the current initramfs before sealing. Successful enrollment now enables root OverlayFS and reboots automatically.

Allow `/install.sh`, `/bootstrap/manifest`, and `/bootstrap/files/…` through the existing HTTPS entry without interactive authentication. They serve an allowlisted set of nonsecret code/assets only, never configuration files, node state, or private keys. Existing machine APIs must also remain accessible without interactive login while retaining their own authentication. Do not expose management APIs without their intended protection.

## 2. Run on a deployed node

In **Enrollment tokens**, create an authorization token and save its one-time secret. Copy the installer command and run it as root on the edge node:

```sh
curl --fail --silent --show-error --proto '=https' --tlsv1.2 \
  'https://hub.example.com/install.sh' | bash -s -- --server 'https://hub.example.com'
```

Replace the example origin; the UI uses your actual browser origin automatically. The node prompts for the token on its controlling terminal without echo. It is not included in arguments, environment variables, URLs, or the shell command history. A missing terminal or inability to hide input fails rather than falling back to echoed input.

The installer validates platform and prerequisites, serializes installation, fetches HTTPS assets, verifies center-manifest sizes/SHA256, installs the first-boot tool, and optionally installs/configures the native Agent. It stores activation as a root-only file and runs the durable initializer. Existing same-center state is reused; a different center URL is rejected rather than overwriting identity. If registration already has a serial, retries need no new shared token.

The initializer deletes the local activation file after independent recovery authentication succeeds. Failed registration retains recoverable state. The installer stops an existing first-boot service while running the interactive flow, avoiding simultaneous service/manual initialization.

## 3. MeshAgent enrollment

After registration, authenticated node metadata supplies MeshCentral public HTTPS URLs for the raw Linux amd64 binary and the configured group settings. Management API credentials remain on the center. The node validates HTTPS and refuses redirects/oversize downloads/non-ELF binaries, then installs using the reviewed local MeshAgent hook.

Downloads live under `/opt/nodehub/bootstrap`, root-only; `/etc/nodehub/mesh-install.json` records the observed binary SHA256. This provides local consistency with the authenticated HTTPS download, not an independent vendor signature. Existing ownership checks still refuse an unrelated MeshAgent installation. Complete steps remain resumable.

The MeshCentral **public** download URLs must be reachable from the edge node without an interactive login page. Internal Node Hub-to-MeshCentral `tls_skip_verify` is not inherited by these downloads; install the appropriate private CA on nodes when necessary. A public entry can use HTTPS while the container control listener uses WS.

The authenticated node asks the center to associate the unique serial match in the configured device group automatically. No operator Node ID confirmation is needed. Missing devices retry on subsequent service runs; duplicates or conflicting bindings fail closed. This trusts the configured MeshCentral group and its naming; it is not cryptographic proof from the MeshAgent certificate. After binding, a detached finalizer validates overlay prerequisites, makes `/data` non-blocking, removes initialization files and credentials, enables root OverlayFS, and reboots. Installed service identities remain at their default root paths. See the [OverlayFS plan](overlayroot.md).

## 4. Prepare an unregistered image

The same command can install tools while prompting for and saving a batch token without enrolling:

```sh
curl --fail --silent --show-error --proto '=https' --tlsv1.2 \
  'https://hub.example.com/install.sh' | bash -s -- \
  --server 'https://hub.example.com' --prepare-image
```

This enables the first-boot unit but does not start it. It refuses an existing Node Hub/Agent identity. You must still seal machine-id, SSH, Tailscale, and MeshAgent identities correctly; this option does not erase or sanitize a previously registered production node.

The batch token is saved to `/etc/nodehub/activation.json` (root, mode 600). Choose a reusable token with enough uses and lifetime; unlimited uses/expiry are supported. Shut down and clone without starting enrollment on the template. Each clone creates its own serial and credentials, removes its activation file after independent authentication, and resumes automatically after transient failures. The template token remains in the source image until you replace that image or revoke the token centrally. Successful setup removes the first-boot unit/overrides, initializer, Mesh installation helper/settings/downloads, and initializer state, then reboots. Native Agent, Tailscale, MeshAgent, SSH and their identities are preserved. The center remains `awaiting_overlay` until boot acceptance; cleanup does not claim a verified reboot.

## 5. Testing boundaries

Tests cover asset allowlisting, SHA256 metadata, disabled/incomplete packages, symlink refusal, authenticated group metadata, credential non-disclosure, HTTPS URL validation, unsupported architecture, and refusal of insecure password-input fallback. Installer assets compile and the frontend builds.

The fresh inspected template was not registered or modified during OverlayFS investigation. Real online bootstrap, third-party download availability, complete clone registration, OverlayFS reboot, and `/data` failure scenarios require acceptance on a designated node with a real authorization token. Previous native root SSH tests are not a substitute for those new tests.
