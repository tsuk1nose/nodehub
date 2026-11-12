# Historical requirements and design discussion

English | [简体中文](zh-CN/docs/design-discussion.md)

Historical version: v0.10 · Date: 2026-11-09 · Status at that time: discussion before implementation.

This archive records the original decisions and alternatives. The current [specification](specification.md), [README](../README.md), and [native terminal manual](native-terminal.md) supersede proposals below. In particular, a native root terminal was subsequently authorized and implemented even though the initial scope deferred it.

## 1. Original objective and division of responsibility

A central program would coordinate Debian 13 initialization, identities, and access status, correlate Headscale and MeshCentral records, and provide a simple Web UI.

| Component | Responsibility |
| --- | --- |
| Center | Identity, controlled registration, upstream correlation, status, management entries |
| Headscale/Tailscale | Networking and reachability |
| MeshCentral/MeshAgent | Terminal, files, and remote maintenance |
| Initializer | Installation, configuration, enrollment, verification, retry |

The UI would offer minimal optional local authentication, normally behind Authelia; show serial, registration/last-seen times and Tailscale addresses; and link to MeshCentral without proxying/embedding its UI.

## 2. Image and deployment requirements

Finish business software setup first, install initialization last, shut down, and clone. Each clone obtains its own serial, sets hostname, and registers both upstreams using that name. Root OverlayFS is part of the intended lifecycle, while `/data` uses CoW and mount failure must not prevent boot.

Keep Tailscale/MeshAgent identity and state at default root paths, not `/data`. Persist them before overlay activation. Remove shared activation credentials, initializer, and first-boot service before enabling OverlayFS/reboot. Cleanup and recovery must be resumable. Deleting a shared token locally does not revoke other image copies.

Provide Docker/Compose and scripts. Initially colocate center, Headscale, and MeshCentral; later separation should require address/configuration changes rather than a redesign. Operator handles certificates, domains, and reverse proxy.

Pin MeshCentral 1.3.1 and Headscale 0.29.7; at the discussion stage their deployed interfaces/images were not yet validated. Track future breaking Headscale changes and verify registration/query/binding/migration before upgrades. Use SQLite for the initial single-center workload.

Choose Headscale's built-in DERP with client verification, without a separate tailscaled container solely for verification. A server tailnet client would have a separate purpose if the center later needed active node access.

## 3. Initial MVP proposal

Propose controlled enrollment tokens, identity registration/query, Tailscale/MeshAgent setup, explicit upstream mappings, retry/resume after partial success, connection/error displays, optional single-admin sign-in, MeshCentral entry links, Docker persistence, cloned-image hostname initialization, and eventual OverlayFS finalization.

Initially defer a custom terminal, files, permanent custom Agent, full monitoring, and recreating MeshCentral. Later native-terminal authorization supersedes that boundary; historical telemetry remains deferred.

## 4. Network and management flow

```text
Administrator -> authentication / proxy -> Center Web/API
                                           +-- Headscale API
                                           +-- MeshCentral control API
                                           +-- SQLite
Debian initializer -> Center registration
Tailscale client   -> Headscale / peers / DERP
MeshAgent          -> MeshCentral
Administrator      -> MeshCentral device page
```

Keep MeshCentral independently reachable so network coordination failure need not remove maintenance access. Shared hosts/egress still create common failure points. The center does not forward all business traffic.

## 5. Identity decisions

One immutable serial is the unique ID/display number/hostname/upstream name. The final discussed form is 12 secure-random characters from `0–9`, `A–Z`, with no extra UUID and no renaming. There are `36^12 = 4,738,381,338,321,616,896` combinations; use a database uniqueness constraint and collision retry, not probability alone.

Keep identity distinct from secrets: enrollment token controls admission; independent per-node credentials support later recovery/communication. Do not authenticate using serial, hostname, IP, or MAC.

Persist registration locally. Normal restart/network retry/incomplete initialization preserves identity. Rebuilding identity creates a new authorized registration/serial and retains the old archive. No deletion or number reuse. Templates must not carry enrolled identities; copied local keys are not a normal recovery strategy. Clone-conflict detection remained an open design point.

## 6. Registration and cleanup proposal

Prepare business software/dependencies, install initializer/service last, preinstall unregistered upstream binaries if desired, seal unique state, and shut down. Starting the sealed template again may trigger initialization and requires clear operating instructions.

On a clone's durable writable root:

1. Attempt `/data` mounting without blocking boot; only dependent tasks degrade/wait.
2. Persist a local request identity/progress before requesting a serial; reuse it after timeout/power loss.
3. Save identity and apply hostname consistently.
4. Join upstreams under the serial, retaining default root service state.
5. Remove activation early only once independent authentication can finish registration/recovery; otherwise wait for successful enrollment.
6. Verify the durable identity baseline, remove bootstrap secrets/program/service, and flush writes.
7. Prepare resumable finalization and a verification path that does not need the activation secret, then enable next-boot overlay and reboot.
8. Verify hostname/upstream connectivity and identity after reboot.

Record registration separately from full initialization, which includes cleanup and boot verification. Batch tokens support configurable expiry/use counts and unlimited early-image reuse. Successful enrollment of one node does not invalidate the batch token. Short-lived single-use Headscale preauth keys are a different credential.

Do not guess unverified upstream register/bind behavior. Match actual node keys and group/user identity, not display names alone. Preserve progress after a partially completed install.

## 7. Status semantics

Candidate records include serial, center registration/progress/error, Headscale Node ID/public key/IPv4/IPv6/state/last-seen, MeshCentral Node ID/group/state/last-seen, and source-specific sync timestamps/errors. Maintain center registration time independently of external record reconstruction.

Keep both upstream states separate. Explicit upstream online/offline observations can update state; failed queries preserve previous known state and show synchronization failure. Distinguish upstream last-seen, observed offline-to-online transitions, and exact historical login times that an API may not expose.

Use configurable 60-second batched polling for both offline and reconnect detection. Refresh basic information on first observation/reconnect and reconciliation after center restart or synchronization recovery. Refresh mutable addresses from results even during long online periods. Avoid per-device polling jobs where batching is possible; use backoff. Short transitions can be missed and upstream detection/request latency can exceed one interval.

## 8. UI, admission, and lifecycle

Proposed pages: node list/details with identities, both connection states, address/time/progress/errors; token creation with operating instructions; and verified MeshCentral links. Center authentication does not automatically authenticate the MeshCentral browser session.

Retain linked revocation: center credentials and both upstreams must reject the old identity, including offline devices reconnecting. Record each result and allow retry after partial failure. Hiding a node or re-registering a new one is not revocation. Shared batch tokens remain separately valid and may admit another identity unless a further policy blocks it.

No node deletion or old-record restoration. Reimage/new identity obtains a new serial and leaves the old record intact.

## 9. MeshCentral integration levels

| Level | Historical decision |
| --- | --- |
| Device link | First choice, exact version-specific URL to verify |
| Reverse proxy through center | Not implemented |
| Embedded UI | Deferred; requires session/frame-policy validation |
| Reimplemented features | Initially deferred; native SSH was later separately authorized |

MeshCentral itself must enforce rights; hiding a button is insufficient. Unified sign-in is a separate design.

## 10. Network coverage and future telemetry

Tailscale can use UDP direct paths or DERP; MeshCentral uses WebSockets/relay. Together they cover many NAT/outbound environments but not guaranteed allowlists, authenticated proxies, TLS inspection, or long-connection restrictions. Test UDP-blocked DERP operation and MeshCentral maintenance while Tailscale is down.

Initial guidance was to use existing Agents first. Reconsider collection only for historical CPU/memory/disk, business-service health, offline replay, or local remediation. A timer/script can cover a small periodic report; continuous monitoring merits a dedicated design. Existing MeshCentral inventory/update frequency requires validation and is not a full monitoring platform.

See the [telemetry draft](telemetry-draft.md); it remains outside current implementation.

## 11. Delivery, storage, and compatibility

Deliver center Dockerfile/Compose/scripts/configuration/persistence/backup instructions. External services keep their own persistent state; rebuilding containers must not lose identities. Initially one SQLite center on local durable storage, with migrations and consistent backup/restore; copying only a live main database is insufficient. Reevaluate the database only with actual write/concurrency/multi-instance needs.

Use Go `net/http`, explicit SQL/SQLite, Vue 3 + TypeScript + Vite, static frontend served by Go, adapters per upstream, and in-process synchronization. Avoid queues/microservices solely for speculative scale. A mature component library was considered but not fixed by this archive.

Headscale v0.29.7 built-in DERP supports `derp.server.verify_clients: true`. Separate derper alternatives were reference-only: `-verify-client-url` can use Headscale `/verify`, with `-verify-client-url-fail-open=false`; `--verify-clients` instead depends on local tailscaled visibility/socket. Validate flags against the actual derper version. The chosen phase uses built-in DERP only.

References: [Headscale DERP](https://github.com/juanfont/headscale/blob/v0.29.7/docs/ref/derp.md), [derper implementation](https://github.com/tailscale/tailscale/blob/main/cmd/derper/derper.go).

Store upstream management API/Login Token credentials and internal Docker addresses in a permission-restricted, read-only mounted local configuration, separately from public node/browser URLs. Do not send them to nodes, browser responses, logs, or images. Change configuration/recreate for rotation; no initial hot reload. Node service identity, enrollment token, and Web password are distinct credential classes.

## 12. Open questions at the discussion stage

Confirmed: 12-character serial without UUID, new identity on re-registration, no deletion, device-link integration only, a few hundred N2840 nodes, linked revocation, and no historical telemetry implementation.

Still open then: recovery-authentication details, precise OverlayFS upper/work and reboot persistence, `/data` partition/mount behavior, durable upgrades, deep-link verification, upstream least-privilege/API details, and multi-clone acceptance. Current manuals describe later resolutions without treating unimplemented items as complete.

## 13. Proposed acceptance

Verify first registration, unique serial/keys/machine identity across clones, matching names/case normalization, no duplicate records on retry, power/network interruption recovery, preserved identities after overlay reboot, degraded `/data` behavior, removal of bootstrap entrypoints/secrets, Docker persistence, consistent SQLite restore, pinned-version compatibility, accurate stale/offline UI, correctly authorized deep links, Authelia management/machine separation, DERP fallback, independent MeshCentral recovery, and linked revocation of online/offline old identities.

## 14. Communication identity alternatives

For future telemetry, generate per-device Ed25519 after cloning; a `/data` private-key location was a proposal only. Measure signing/TLS/sampling/write overhead on target hardware rather than assert unsupported throughput limits. Use standard formats and mature libraries.

Per-request HTTP signatures would need method/target/body digest, time/nonce replay protection, and a separate protocol. Another candidate was an independent random 32-byte Bearer token per node over HTTPS, hash-only centrally. Tokens are simpler but possession permits impersonation; never share one long-term token across devices. Ed25519 avoids storing node-impersonating private keys centrally, but copied device keys still permit impersonation. Both require revocation/rotation/scope.

The later candidate is HTTPS + Ed25519 challenge authentication + short-lived sessions, documented separately; none of this draft authorizes building telemetry now. Do not reuse upstream or SSH keys.

Do not primarily authorize field reimaging by country or fixed source IP. Activation files support limits/unlimited reuse and central revocation. Copying a token preserves its authority until expiry/exhaustion/revocation; deleting one local copy does not erase images or flash history. Delete local activation early only after independent recovery works, and remove bootstrap entrypoints before overlay activation. `/data` placement does not replace admission control or move current service identities there.

## References

- [Tailscale connection types](https://tailscale.com/docs/reference/connection-types)
- [Firewall ports](https://tailscale.com/docs/reference/faq/firewall-ports)
- [Historical Headscale registration reference](https://headscale.net/0.28.0/ref/registration/)
- [MeshCentral design](https://docs.meshcentral.com/design/)
- [MeshCtrl guide](https://meshcentral.com/docs/MeshCtrlUsersGuide.pdf)
- [Ed25519, RFC 8032](https://www.rfc-editor.org/rfc/rfc8032.html)
- [Bearer tokens, RFC 6750](https://www.rfc-editor.org/rfc/rfc6750)
- [HTTP message signatures, RFC 9421](https://www.rfc-editor.org/rfc/rfc9421.html)
