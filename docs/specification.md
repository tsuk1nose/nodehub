# Node Hub requirements and architecture

English | [简体中文](zh-CN/docs/specification.md)

Version: 1.3 · Date: 2026-11-12 · Status: confirmed requirements; MVP in development.

This document supersedes the historical [design discussion](design-discussion.md). Implementation status is maintained in the [README](../README.md). [Long-term telemetry](telemetry-draft.md) remains deferred.

## 1. Purpose and scale

Register Debian 13 edge nodes, orchestrate Headscale/MeshCentral enrollment, correlate device identities, display connection state, and provide remote management entry points. Initial deployments have a few hundred nodes, primarily Intel N2840 x86 devices. Thousands or tens of thousands of nodes are a future capacity decision, not a reason to introduce clustering now.

Headscale and MeshCentral are deployed independently by the operator; real upstream integration has been tested. Services initially share a Docker host and can later be separated by changing addresses. Domains, certificates, and reverse proxies are operator-managed.

## 2. Components

| Component | Choice and responsibility |
| --- | --- |
| Backend | Go, standard `net/http`; registration, authentication, orchestration, synchronization |
| Frontend | Vue 3, TypeScript, Vite; static assets served by Go |
| Database | SQLite, one center instance, explicit SQL and schema migrations |
| Headscale | 0.29.7, Tailscale coordination, built-in DERP with client verification |
| MeshCentral | 1.3.1, remote management, terminals, files |
| Delivery | Dockerfile, Compose, examples, scripts, operating manuals |

No Redis, message queue, or microservices are required. Pin Headscale versions and keep compatibility in adapters: future breaking changes require registration/query/binding/migration tests before upgrading. Do not proxy or embed the MeshCentral management UI.

Use `derp.server.verify_clients: true`. Built-in DERP verification does not require a separate server-side tailscaled container. Add one only if the center later needs to reach tailnet addresses itself.

## 3. Identity and credentials

One immutable serial serves as the node ID and displayed number: 12 cryptographically random characters from `0–9` and `A–Z`, with a database uniqueness constraint and collision retry. No extra UUID, renaming, or number reuse. Use the serial as hostname and upstream display name; tolerate upstream case normalization. MeshCentral's internal Node ID is generated independently and mapped to the serial.

Serials are public identifiers, not credentials. Before the first registration request, persist an independent request ID and node credential. Response loss or interruptions must resume the original registration without allocating another serial or consuming token quota again.

Enrollment tokens are supplied in activation files. Lifetime and registration count are configurable, including unlimited values for reusable early images. Store only token hashes; display the secret once. Support revocation. One successful node must not invalidate a shared token globally.

Recovery uses an independent per-node random Bearer credential over HTTPS; store its hash centrally. Optional native Agents have separate Ed25519 identities. Do not reuse Agent, SSH, Tailscale, or MeshAgent keys. Do not log credentials or commit them to source.

## 4. Configuration and authentication

Configure listener, database, polling, local sign-in, upstream internal/public URLs, Headscale API key/user/tags, MeshCentral Login Token/group, and optional validated deep-link template. Docker DNS names are internal only and must not be issued as public device addresses.

Read-only mount a permission-restricted local configuration; never bake credentials into images. Recreate after changes to reload and remount the file. Hot reload and automatic upstream credential rotation are outside the MVP. Each upstream has an explicit `tls_skip_verify` flag, default false, scoped only to its internal HTTPS/WSS client; Agent and node HTTPS validation remain independent.

Local single-administrator authentication is enabled by default with bcrypt, HttpOnly/SameSite cookies, and configurable Secure cookies. It can be disabled behind Authelia; the operator must protect all management routes. Machine routes use their own token/device authentication and must not be intercepted by interactive sign-in.

English is the default documentation and application language. Simplified Chinese manuals and an optional UI language preference are provided. Stable API/protocol fields, serials, and terminal content are not localized.

## 5. Images and initialization

### Template preparation

Install business software and dependencies first. Install the initializer, activation file, first-boot service, and trusted hooks last. Tailscale, MeshAgent binaries, and the optional native Agent may be preinstalled but must not register the template.

Remove device-specific identities, initialization progress, and completion flags. Prepare machine-id and SSH host identity correctly; complete clone acceptance remains pending. Shut down the template without starting initialization again.

### First boot of a clone

1. On a durable writable root filesystem, generate and persist a request identity and credential.
2. Obtain a serial using the enrollment token. Verify independent recovery authentication, then delete the local shared activation file.
3. Set hostname and related host configuration.
4. Obtain a short-lived, single-use Headscale preauth key and join under the serial.
5. Verify node key, registration source, user, and external node ID before binding.
6. Install/register MeshAgent under the serial; explicitly verify and bind its Node ID and group instead of matching names alone.
7. If enabled, enroll the native Agent and automatically install root SSH authorization, pin the host key, and confirm it.
8. Persist service identities at their default root filesystem paths. Explicit finalization removes bootstrap secrets, initializer, first-boot unit, and its optional Agent drop-in.
9. Run a device-validated hook to prepare OverlayFS for the next boot, then reboot.
10. Verify OverlayFS, hostname, connectivity, and preserved identities before confirming completion.

Registration and finalization have distinct progress. Failed steps must remain resumable and must not report completion. After initializer removal, recovery uses existing management channels or external media. Power-loss recovery during finalization still requires complete device acceptance.

## 6. Storage and boot

MeshAgent is installed entirely at `/data/nodehub/meshagent`; missing `/data` stops only MeshAgent. Tailscale, native Agent, hostname, machine-id, SSH host keys, and authorization retain their existing root filesystem paths. Establish the durable root baseline before OverlayFS. No writable-root overlay helper is enabled.

Node `/data` uses CoW; the final filesystem and mount setup are not implemented. Mount failure must not block boot or root-based remote management. Dependent business tasks should degrade or wait, not write into an unmounted fallback directory.

CoW and root OverlayFS are separate decisions. Upper/work placement, reboot persistence, maintenance mode, runtime key rotation, and upgrades need an explicit design. Deleting a root file after an ephemeral overlay starts does not erase its lower-layer secret. Data-directory deletion requires checking the real mount and does not revoke other token copies.

## 7. Status and UI

Batch-poll both upstreams every 60 seconds by default, configurable with a minimum of 5 seconds. Detect both offline and online transitions. Reconcile on first observation, reconnect, center restart, or restored synchronization; refresh mutable addresses from upstream results.

Keep separate upstream states: online, offline, unknown/unbound, and missing. Query errors preserve the last successful state/time and display a synchronization failure rather than declaring devices offline. Last seen is upstream time or the center's observation, not a guaranteed historical login timestamp. Short transitions between polls may be missed.

Show serial, registration time/stage/error, upstream IDs/states/last-seen times, and Tailscale addresses. Support token creation/revocation. Use a clearly labeled console link until a device deep-link template is validated. No historical metrics are implied by the optional Agent's basic heartbeat information.

## 8. Lifecycle

Do not delete nodes or reuse serials. A reimage/new identity registers as a new node; retain the old record. Normal reboots and incomplete initialization retries preserve identity.

Linked revocation must revoke center access and block both upstream identities, including offline devices reconnecting later. Record each result and retry partial failure without claiming success. MeshCentral 1.3.1 enforcement still needs validation; the current endpoint returns 501. Re-registration does not itself revoke old credentials, and a shared enrollment token can still authorize new identities.

## 9. Optional native root terminal

An unprivileged Go Agent maintains outbound WSS control and an independent data connection per terminal, forwarding only its configured local IPv4 SSH port. No arbitrary destination or initial multiplexing implementation.

Enroll Ed25519 keys through the node's recovery credential. Challenge authentication is short-lived and connection-bound. Require administrator sessions, explicit Origin validation, device identity checks, and pinned SSH host keys. First registration automatically generates a distinct center-held SSH private key, installs root authorization, and confirms before dynamically enabling the target. No per-node JSON/restart is required.

The center administrator has full node maintenance power. Agent relay privileges remain separate from the root shell. Bound connections, concurrency, input/output buffers, acknowledgments, and duration. Audit session metadata and security events, not terminal contents. Native Agent revocation affects only that channel.

See the [native terminal manual](native-terminal.md) for protocol, storage, setup, migration, and validation.

## 10. Acceptance and limits

Verify reproducible backend/frontend builds, persistent SQLite and consistent backups, local/external authentication boundaries, credential separation, serial uniqueness, registration recovery/quota behavior, and stale-state handling.

Real upstream registration/binding, trusted binary installation, automatic root authorization/migration, and N2840 browser terminals have passed initial device testing. Docker container operation, complete clone/OverlayFS lifecycle, CoW mount failure, power-loss recovery, and linked revocation remain pending or unimplemented. Keep these limits explicit in the README; protocol documentation alone is not deployment acceptance.

## References

- [Headscale v0.29.7](https://github.com/juanfont/headscale/tree/v0.29.7)
- [MeshCentral 1.3.1](https://github.com/Ylianst/MeshCentral/tree/1.3.1)
- [Telemetry draft and security references](telemetry-draft.md)


## 11. HTTPS bootstrap and inspected overlay plan

The center can distribute an allowlisted installer/package manifest. The UI generates a root command from its current origin, while the node prompts for enrollment authorization through a no-echo terminal. Node-authenticated metadata supplies MeshCentral download URLs without center management credentials. Image preparation prompts for and saves the batch token without enrolling the template. Clone first boot automatically associates the unique MeshCentral group/serial match, cleans bootstrap files and credentials, prepares root OverlayFS, and reboots. Real boot acceptance remains pending. See [bootstrap](bootstrap.md).

The inspected Debian template supports `overlayroot=tmpfs:recurse=0,swap=0`, with independent writable Btrfs `/data` and a reviewed nofail policy. The [OverlayFS plan](overlayroot.md) separates template work, activation, acceptance, recovery, and the unresolved durability of post-boot credential updates. No boot changes were applied during inspection.
