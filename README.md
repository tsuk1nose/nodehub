# Node Hub

English | [简体中文](docs/zh-CN/README.md)

A lightweight management center for Debian 13 edge nodes. Go + Vue 3 + SQLite, deployed with Docker, integrating Headscale **0.29.7** and MeshCentral **1.3.1**.

Start with the [deployment and acceptance guide](docs/first-deployment.md). See the [configuration reference](config/README.md), [architecture specification](docs/specification.md), and [device validation report](docs/live-test-2026-11-12.md). The [design discussion](docs/design-discussion.md) is historical; [long-term telemetry](docs/telemetry-draft.md) is explicitly deferred.

## Current capabilities

- Twelve-character random serials (`0–9`, `A–Z`), recoverable initialization, independent node credentials, and configurable enrollment tokens.
- Headscale registration, verified node bindings, batched upstream status synchronization, and a node/token management UI.
- MeshCentral control WebSocket authentication using a Login Token. Administrators verify and bind an external Node ID; the center checks its name and group instead of matching names alone.
- Optional native Agent and browser SSH terminal: Ed25519 device authentication, outbound WSS, independent session data connections, pinned SSH host keys, bounded buffers, concurrency/time limits, and metadata auditing.
- Automatic **root** SSH authorization. Per-node login private keys remain in center SQLite; the initializer installs the public key while preserving existing root authorizations. No per-node JSON or SSH key mount is needed.

English is the default for documentation, UI, command help, and application messages. Choose Simplified Chinese in the UI language selector; the selection is saved locally. Chinese manuals are linked from their English counterparts. Node serials, API fields, protocol values, user-entered content, and terminal output are never translated.

Write new documentation, code comments, examples, and runtime messages in English. Keep optional Chinese manuals under `docs/zh-CN/` with mirrored repository-relative paths and bidirectional language links. Maintain both versions when behavior changes; keep Chinese UI strings in `web/src/i18n.ts`.

## Known limits

- Docker configuration is validated; a complete container build/run still requires deployment acceptance. The earlier build attempt was blocked by the host Docker daemon's proxy.
- MeshAgent installation uses a trusted local binary hook. Supply the binary, verified SHA256, and group `.msh` settings; no automatic online download is provided.
- OverlayFS requires an explicitly supplied, device-validated hook. Partitioning, CoW setup, full clone lifecycle, and power-loss recovery are not automatically implemented or fully validated.
- Linked Headscale/MeshCentral revocation remains unimplemented and returns HTTP 501. Revoking the native Agent affects only that channel.
- No node deletion, historical telemetry, terminal recording, or full multi-user RBAC.
- Device deep links remain optional and unverified; the UI falls back to the MeshCentral console.

## Docker quick start

Headscale and MeshCentral are deployed separately. From this repository root:

```sh
cp config/config.example.json config/config.json
# For automatic root terminals, use config/config.terminal.example.json instead.
docker compose build
docker compose run --rm --no-deps nodehub -hash-password
```

Enter a 12–72 byte administrator password on standard input. Put the complete bcrypt result in `auth.password_hash`, then fill in upstream addresses and credentials. Keep actual deployment information in the ignored `config/config.json`, not the examples.

```sh
sudo chown 10001:10001 config/config.json
sudo chmod 600 config/config.json
./scripts/start.sh
```

The center publishes `127.0.0.1:8080`. Use your existing HTTPS entry; Secure cookies are enabled by default. For local HTTP development only, explicitly set `auth.secure_cookie=false`. Disabling local authentication requires an already protected management entry.

The container runs as UID/GID 10001. Its root filesystem is read-only; SQLite uses a persistent `/data` volume. Recreate after configuration changes with `docker compose up -d --force-recreate`; image rebuilding is unnecessary for configuration-only changes. Never use `down -v` when retaining identities.

See [Docker deployment](docs/docker.md) for networks, standalone Compose, Agent exports, and legacy manual-key compatibility. `--terminal` on the start script is only for the legacy key mount; automatic terminals use the base Compose.

## Upstream configuration

Separate internal management URLs from public URLs used by devices or browsers. Headscale accepts an internal HTTP address on a controlled network; MeshCentral requires a complete `ws://…/control.ashx` or `wss://…/control.ashx` URL. TLS verification cannot be disabled. Install private CA trust if necessary.

Keep an upstream disabled until configured. Enabling it requires valid credentials, the intended Headscale numeric user ID, and the full MeshCentral group ID. Obtain them using the [credential and group lookup procedures](docs/first-deployment.md).

Leave `device_url_template` empty until verified. A configured HTTPS template must include `{node_id}`, use the same host as `public_url`, and receives URL-encoded IDs.

## Development and checks

Requires Go 1.26+ and Node.js 22 or a version supported by Vite. The current Docker toolchain is pinned to Go 1.27.3.

```sh
go mod download
go test -race ./...
go vet ./...
cd web
npm ci
npm run build
```

For a local center, set the database to `./data/nodehub.db` and `web_dir` to `./web/dist`. Generate a hash with `go run -buildvcs=false ./cmd/nodehub -hash-password`, then start:

```sh
go run -buildvcs=false ./cmd/nodehub -config config/config.json
```

Run `npm run dev` inside `web` for the development frontend; its API proxy targets local port 8080. `GET /healthz` checks HTTP availability, not upstream authentication. Polling defaults to 60 seconds, with a minimum of 5; upstream errors preserve the last known state.

## Registration API

All JSON writes require `X-NodeHub-Request: 1`. Never put credentials in URLs.

| Endpoint | Authentication | Purpose |
| --- | --- | --- |
| `POST /api/register` | Enrollment token; node credential for recovery | Submit `token`, `request_id`, `credential`; receive the serial |
| `GET /api/node/{serial}` | Node Bearer credential | Read local identity and public Headscale URL |
| `POST /api/node/{serial}/headscale-key` | Node Bearer credential | Obtain a short-lived, single-use preauth key |
| `POST /api/node/{serial}/headscale-bind` | Node Bearer credential | Submit and verify `node_key` |
| `POST /api/node/{serial}/stage` | Node Bearer credential | Update progress; completion requires both bindings |
| `POST /api/nodes/{serial}/meshcentral-bind` | Management session | Bind the verified external `node_id` |

The client generates `request_id` and `credential` as independent 32-byte random values encoded as 64 hexadecimal characters and persists them before requesting registration. The same request ID can be recovered only with the same node credential, even after enrollment-token expiry or revocation; recovery creates no new identity.

Node recovery state lives in `/var/lib/nodehub-init`, not `/data`. The center stores credential/token hashes; a short-lived Headscale key is temporarily cached for retries and removed after binding. Automatic SSH private keys are also stored in center SQLite. Treat databases and backups as secrets.

## Debian images and native root terminals

Follow the [client manual](client/README.md). Do not register the template, clone existing identities, or activate OverlayFS before durable registration and cleanup.

For native root terminals, configure the center's `allowed_origins`, install Agent/OpenSSH on the template, and use the [first-boot service override](client/nodehub-init-agent.conf.example). The initializer runs with `--agent-config /etc/nodehub-agent/config.json`. The Agent itself runs as an unprivileged relay; sshd executes the terminal as root. Old maintenance-account migration is described in the [native terminal manual](docs/native-terminal.md).

## Backup and restore

SQLite uses WAL. Create a consistent backup instead of copying a live main database file:

```sh
docker compose exec nodehub /app/nodehub -config /app/config.json -backup /data/backup-20261112.db
```

The destination must not exist. Export it securely and retain the configuration. Stop the center before restoring; replace the database with the backup, avoid stale WAL/SHM files, and restore UID 10001 permissions. Backups contain SSH login private keys and registration secrets and must never be committed.

## Validation status

Go compilation, race tests, static checks, frontend production builds, Python tests, and Compose configuration validation have passed. Network tests cover authentication, replay rejection, ticket consumption, session ownership, limits, host-key verification, reconnects, revocation, logout, and auditing.

Debian 13 / Intel N2840 device tests passed for upstream enrollment/binding, initialization retries, automatic SSH provisioning, root migration, preservation of existing root keys, and real browser terminal input/output, resize, and close. The temporary center is stopped. Existing test identities remain tied to its temporary URL: use a clean new node for a new center, or plan a database/address migration without deleting device state. See the [redacted report](docs/live-test-2026-11-12.md).
