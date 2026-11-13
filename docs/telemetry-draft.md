# Long-term telemetry design draft

English | [简体中文](zh-CN/docs/telemetry-draft.md)

Date: 2026-11-09 · Status: future discussion; **not implemented in the current phase**.

The separately authorized [native terminal Agent](native-terminal.md) adds heartbeat/basic system information. It does not implement this draft's historical metrics, offline metric queue, sessions, storage, alerts, or dashboards. Initialization recovery authentication is independent of future telemetry.

Current Tailscale/native Agent identities remain at root paths; MeshAgent is stored entirely at `/data/nodehub/meshagent`. Node `/data` uses CoW and must not block boot if mounting fails. Suggestions below to store a future telemetry key in `/data` are candidates only and do not change today's service identity boundary.

## 1. Goals

Optionally collect system resources, specified service health, and business state, with bounded offline replay. Keep existing remote terminal/file capabilities separate; a telemetry endpoint must not accept arbitrary commands.

Candidate metrics: CPU, memory, disk space, uptime, and selected service status. Frequency, retention, alerts, and exact metrics require agreement. Do not implicitly collect file contents, passwords, or full logs.

## 2. Candidate authentication

Use HTTPS, per-device Ed25519 identity, and short-lived opaque session tokens. This is a proposal, not a confirmed wire protocol.

1. Generate an independent private key on first boot, potentially stored with restricted permissions in `/data`; enroll its public key/key ID through activation authorization, never trust arbitrary request-supplied keys.
2. Verify HTTPS server identity before requesting a challenge.
3. Generate a cryptographically random, short-lived, single-use challenge bound to node, key version, and purpose. Limit issue rate and pending challenges.
4. Sign a versioned, unambiguously encoded message covering challenge ID/value, node identity, key version, and target service identity.
5. Verify the registered key, atomically consume the challenge, check revocation, then issue a short-lived session token. Specify concurrent retry/response-loss recovery before implementation.
6. Submit batches with Authorization headers over HTTPS, never URLs, logs, or metric labels.
7. Reauthenticate after expiry/restart/revocation; do not assume one handshake per day describes actual connection lifetimes.

Use at least 32 random bytes per token. Store only its hash with node, scope, expiry, and credential version. A candidate lifetime is 15–60 minutes, to be chosen based on revocation/scale. Prefer in-memory node sessions; reauthenticate after reboot.

Validate node status, session expiry/scope, and request size on every submission. Revocation must invalidate current sessions, not just future sign-ins. A stolen valid Bearer token still permits impersonation; token use is not per-request proof of private-key possession.

Do not implement custom X25519 exchange or duplicate AES-GCM atop HTTPS. TLS provides transit confidentiality/integrity. Evaluate end-to-end protection through untrusted intermediaries separately if needed. mTLS is an alternative, but certificate issuance/rotation is not introduced now.

## 3. HTTPS server authentication

Use Debian's CA trust store with chain, validity, and configured hostname verification. No extra application public key or leaf-key pinning is proposed by default. This still relies on preconfigured CA trust. Never skip verification or downgrade to HTTP.

The server URL comes from trusted deployment/activation configuration, not unauthenticated network discovery. Credential-bearing requests must not follow cross-host redirects. Fix network/time setup instead of bypassing certificate validation.

DNS tampering/listening alone cannot bypass verification without a trusted matching certificate. CA misissuance, modified trust stores, trusted TLS inspection, and leaked server keys remain relevant trust boundaries.

Provision a private CA securely when used. Pinning would require backup pins and rotation/recovery planning; it is not the current recommendation for remotely deployed devices.

A TLS-terminating reverse proxy is trusted and sees credentials/data. Protect the proxy-to-center hop with a controlled local/isolated network or verified TLS. Machine routes must bypass interactive Authelia login. This proposal does not hide plaintext from a trusted TLS terminator.

## 4. Submission and offline behavior

- Sample at configurable intervals; batch and reuse HTTPS connections. Measure real scale instead of assuming signing each report is inherently infeasible.
- Include protocol version, node ID, batch ID, sample time, and metrics; store center receipt time. Mark unreliable clocks and never authorize based solely on client timestamps.
- Use batch IDs for at-least-once deduplication; reject conflicting content for one ID. Deduplication does not stop a stolen valid token submitting fresh IDs.
- Specify successful/duplicate/rate-limited/unauthenticated/unsupported responses.
- Use exponential backoff with jitter. Bound queues by capacity and age, with explicit drop policy/counting.
- Bound persistent replay in `/data` and disk writes. An extended outage must not fill the partition.
- If `/data` is missing, do not generate a replacement identity or create an unlimited fallback root queue.

## 5. Storage and lifecycle

Separate identity, key versions, sessions, and metrics. Existing SQLite does not imply unbounded history belongs in the same database. Estimate node count, samples, metric cardinality, retention, and write rate before selecting storage/aggregation/cleanup.

Show telemetry failures, Agent disconnection, Headscale state, and MeshCentral state separately. Key rotation requires a valid old credential or administrator reauthorization; support theft/compromise revocation. New identity creation yields a new serial while retaining old records, not node deletion or old-record restoration. `/data` presence alone cannot decide validity of existing service identities.

Agent upgrades, OverlayFS maintenance, configuration persistence, and version compatibility require a separate implementation design.

## 6. Tests before implementation

Reject invalid certificates, wrong names, cross-host credential redirects, replayed challenges, substituted keys, cross-service signatures, expired sessions, revoked nodes, and sessions bound to old keys. Verify durable identity across power loss, deduplication, bounded offline storage, and measured signing/TLS/sampling/disk overhead on target hardware.

## References

- [TLS 1.3, RFC 8446](https://www.rfc-editor.org/rfc/rfc8446.html)
- [TLS service identity, RFC 9525](https://www.rfc-editor.org/rfc/rfc9525.html)
- [Ed25519, RFC 8032](https://www.rfc-editor.org/rfc/rfc8032.html)
- [Bearer tokens, RFC 6750](https://www.rfc-editor.org/rfc/rfc6750)
