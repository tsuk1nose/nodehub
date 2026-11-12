# Native Agent and root SSH terminal

English | [简体中文](zh-CN/docs/native-terminal.md)

Version: 0.6 · Date: 2026-11-12 · Status: optional MVP; Debian 13 / N2840 terminal path validated.

## 1. Scope

An optional Go Agent and xterm.js terminal supplement Headscale/MeshCentral without replacing them. Existing configurations without `terminal` remain disabled. Support Ed25519 device authentication, outbound WSS, heartbeats/reconnects, separate terminal channels, PTY resizing, pinned SSH host keys, bounded sessions, and metadata audit.

No arbitrary TCP proxy, Agent-executed native PTY, file transfer, automatic upgrades, full RBAC, recording, historical metrics, or multi-instance scheduling. The shell is root; the Agent relay itself does not require root.

## 2. Data path

```text
Browser xterm.js -- authorized WebSocket -- Node Hub SSH client
                                                 |
                                  per-session WSS data connection
                                                 |
                                         unprivileged Agent
                                                 |
                                           127.0.0.1:22
                                                 |
                                          existing SSHD
                                                 |
                                             root shell
```

The Agent maintains one control connection and opens a separate data connection per terminal. It has no listener, shell execution, or SSH login private key. Its fixed local IPv4 SSH port is configured locally; the center cannot supply arbitrary forwarding destinations.

WSS is outbound and independent of Tailscale connectivity. The center uses `golang.org/x/crypto/ssh`; SSH authentication/encryption covers the relayed byte stream. Browser access still terminates at the center.

## 3. Device identity and protocol

Reuse the immutable 12-character serial. The independent initialization Bearer credential authorizes enrollment, not long-lived Agent connections. The Agent does not retain that recovery credential.

On a cloned device, generate an Ed25519 seed and atomically persist/fsync it before enrollment. Register its public key at `/api/node/{serial}/agent-enroll`. Retries preserve the key; replacing an existing key or reviving a revoked identity with the old recovery credential is forbidden. Identity recreation uses a new registration, not another UUID.

Control authentication:

1. The center issues a random 32-byte hexadecimal challenge, connection-bound and valid for 10 seconds.
2. Sign `nodehub-agent-v1\n<serial>\n<challenge>` and send `auth`, serial, and Base64 signature.
3. The center verifies the registered key/revocation state and returns `authenticated`.
4. `heartbeat`/`heartbeat_ack` maintain the connection, with defaults of 30 seconds and 90 seconds timeout. Configure matching intervals.
5. A new control connection for the same serial replaces the previous generation and closes its terminals.

Challenges are one-use on their connection. Limit unauthenticated handshakes to 64, with message-size and time bounds. Authenticated connections release handshake quota. Control messages are bounded JSON; data frames carry at most 16 KiB binary chunks.

## 4. Terminal authorization and flow control

A browser POST creates a session; its WebSocket must still present the same authenticated management session and an explicitly allowed Origin. A session ID alone grants nothing. Unclaimed sessions expire after 30 seconds.

The center sends a random 32-byte, single-use data ticket over authenticated control, bound to the device/session/control generation. Submit it as the first data message, not a URL parameter. Reject reuse or old-generation takeover.

The center loads the confirmed SQLite SSH target and strictly verifies the host key. Restrict negotiation to that pinned key's algorithms; RSA compatibility uses RSA-SHA2. SSH handshake and PTY/shell initialization are bounded; no insecure host-key bypass.

Defaults: 10 sessions globally, 2 per node, idle timeout 300 seconds, absolute duration 3600 seconds. Browser close, logout, management-session expiry, Agent disconnection/replacement/revocation, and center shutdown close related sessions. Input/output count as activity, but continuous output cannot evade the absolute limit.

Input has at most 8 queued messages of 4096 bytes each. Output chunks are at most 16 KiB and wait for xterm processing acknowledgment; fail after 20 seconds without acknowledgment. stdout/stderr share a bounded output path. Throughput at high RTT still needs measurement.

## 5. Independent keys and trust

| Credential | Location and role |
| --- | --- |
| Agent Ed25519 private identity | Node `/var/lib/nodehub-agent/identity.json`; authenticates its connection |
| Per-node SSH login private key | Generated centrally and retained in SQLite; never sent to the node |
| SSH host public key | Derived locally from the actual host private key, reported over authenticated HTTPS, pinned on first enrollment |

The center administrator has complete node maintenance authority. Do not reuse Agent keys for root login. Establish durable Agent/host keys and root authorization before OverlayFS; none depends on node `/data`.

Root authorization is a serial-marked block within its existing `authorized_keys`; preserve unrelated keys. SSH configuration must allow root public-key login. Existing `PermitRootLogin yes`/`prohibit-password` policy is retained. If necessary, add `/etc/ssh/sshd_config.d/00-nodehub-root.conf` with `PermitRootLogin prohibit-password`, check effective configuration, validate syntax, and reload. Do not enable password authentication to make this work.

## 6. Center configuration

Copy `config/config.terminal.example.json` or enable this section in existing configuration:

```json
"terminal": {
  "enabled": true,
  "allowed_origins": ["https://hub.example.com"],
  "heartbeat_seconds": 30,
  "timeout_seconds": 90,
  "max_sessions": 10,
  "max_sessions_per_node": 2,
  "idle_seconds": 300,
  "max_duration_seconds": 3600
}
```

Replace the Origin with the actual browser origin, including nondefault port and no path. It is not the Headscale/MeshCentral address. Local development may explicitly allow `http://localhost:5173`; Agent connections still require HTTPS/WSS.

Normal automatic setup has no `nodes` field or SSH-key mount:

```sh
./scripts/start.sh
```

Old `terminal.nodes` file-based targets remain compatible until a confirmed automatic target exists. Private files must be mode 600 or stricter and readable by UID 10001; encrypted keys cannot be interactively unlocked in this MVP. Only legacy file configuration needs `compose.terminal.yaml`. Confirmed database targets take precedence and cannot be replaced by a manual host-key override.

Allow `/agent/control`, `/agent/data`, and browser terminal WebSockets at the existing entry. Machine routes must bypass interactive Authelia login while retaining their own credential checks. Configuration edits require center recreation; active shells do not survive a center restart.

## 7. Install and initialize

Build for the node architecture (Go 1.26+, current Docker toolchain 1.27.3):

```sh
./scripts/build-agent.sh amd64
# Use arm64 where appropriate; N2840 with a 64-bit OS uses amd64.
# Or export the current build platform:
docker build --target agent-binary --output ./bin .
```

Copy the installer, systemd unit, configuration example, and binary to the node, then run as root:

```sh
./agent/install.sh ./bin/nodehub-agent-linux-amd64
```

This does not enroll/start a template. Set `/etc/nodehub-agent/config.json` to the long-term center URL. `serial` can remain empty and be taken from bootstrap state.

On the clone, after activation setup, run:

```sh
/usr/local/sbin/nodehub-init \
  --mesh-install-hook /usr/local/lib/nodehub/mesh-install \
  --agent-config /etc/nodehub-agent/config.json
```

This is the complete initialization path, requiring Tailscale and the MeshAgent hook as well. An already registered, unfinalized node can retry with the same recovery state. To automate across a template batch, install the [service override example](../client/nodehub-init-agent.conf.example) once; commands are in the [client manual](../client/README.md#enable-automatic-root-terminals-in-the-template).

Automatic SSH steps:

1. Enroll Agent identity; reject revoked/replacement identities.
2. Generate missing SSH host keys, enable/start/reload Debian `ssh.service`, and derive the Ed25519 host public key from the real private key.
3. Authenticate `/ssh-provision` with node credentials, `username: "root"`, and `host_key`. Generate a per-node center login key; retries return the same public key and reject changed host keys.
4. Atomically add root authorization with port/Agent/X11 forwarding disabled for that key, leaving PTY/root shell available.
5. Check SSH policy, flush writes, call `/ssh-provision/confirm` with the same fields, then enable/start the Agent. Only confirmation makes a new target available dynamically.

Root home comes from the account database, normally `/root`; home/`.ssh` are root-owned mode 700 and `authorized_keys` mode 600. Custom earlier SSH rules, Match/AllowUsers, or nondefault key paths can still deny login. Verify the actual terminal before finalization.

Local progress is `/var/lib/nodehub-init/ssh-provision.json`. Agent `-enroll` alone only registers communication identity; it does not install root authorization. The full initializer path is required for automatic SSH.

Databases/backups now contain plaintext application-level SSH private-key material, protected by filesystem access rather than separate application encryption. Keep them secret and durable. Reimaging/replacing host identity follows new-node registration, not silent pinned-key replacement.

### Upgrade old maintenance-account deployments

Upgrade center and initializer first, then retry with original recovery state. Keep login and host keys unchanged. Existing maintenance targets remain until root installation is confirmed, at which point the center changes the target user to root. Remove only the exact old authorized-key file installed by this tool; retain the account, directories, and administrator-added authorizations. Do not delete user data.

## 8. Audit and revocation

`terminal_audit` records session/operator/node/source/request/start/end/reason, not terminal contents or private keys. Restart closes unfinished records with `server_restarted`. Security audit records successful sign-in, enrollment, Agent connections/replacement/disconnection, and revocation.

The source address is the center's direct peer, potentially a proxy. Do not blindly trust forwarding headers. With local authentication disabled, the operator is `external-admin`; there is no Authelia identity integration, external-session lifetime check, multi-user attribution, or device-level RBAC.

Revoking an Agent closes connections/sessions and prevents that key from re-enrolling. It does not delete nodes or revoke Headscale/MeshCentral identities. Linked revocation remains unimplemented.

## 9. Validation and remaining work

Full Go race/network tests, static checks, frontend production builds, and nine Python provisioning tests passed. Tests include root migration staging, key isolation, concurrent idempotence, host-key replacement rejection, backup restore, existing-key preservation, malformed blocks, SSH policy, and response-loss recovery.

Real Debian 13 / N2840 tests passed for automatic root migration/provisioning, repeat initialization, preservation of existing root authorizations, and browser terminal input/output, resize, and close. A center restart followed by Agent startup restored database targets. See the [redacted report](live-test-2026-11-12.md).

The earlier dependency scan reported zero reachable vulnerabilities after updating SSH dependencies, with one unused module-only finding; this is not a full security audit. amd64/arm64 Agent binaries have been built.

Production Docker runtime, Dropbear, full clone/OverlayFS reboot behavior, long network disruptions, and sustained resource/throughput/capacity measurements remain pending. One device's roughly 6 MiB service memory is not a capacity guarantee. No shell migration/resumption after connection loss is provided.
