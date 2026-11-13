# Center configuration reference

English | [简体中文](../docs/zh-CN/config/README.md)

Copy one example to `config/config.json`, then edit it:

| Example | Purpose |
| --- | --- |
| `config.example.json` | Enrollment, upstream status, and MeshCentral links; native terminal disabled |
| `config.terminal.example.json` | Also enable automatic root terminals; no per-node configuration |

Examples contain placeholders only. Actual domains, API keys, and Login Tokens belong in ignored local configuration. The container uses UID/GID 10001; make its read-only configuration readable with mode 600. Recreate the container after edits so atomically replaced files are remounted.

## Basic settings and sign-in

| Field | Meaning |
| --- | --- |
| `listen` | Container default `:8080` |
| `database` | Center SQLite path, `/data/nodehub.db` in the container; requires persistence |
| `web_dir` | Built frontend directory, `/app/web` in the container |
| `poll_interval_seconds` | Upstream/UI refresh interval, default 60 seconds, minimum 5 |
| `auth.enabled` | Local administrator sign-in; protect management routes externally if disabled |
| `auth.username` | Local administrator username |
| `auth.password_hash` | Complete bcrypt hash, not plaintext; password length 12–72 bytes |
| `auth.secure_cookie` | Keep true for HTTPS; explicitly set false only for local HTTP development |
| `auth.session_hours` | Local session lifetime in hours; example value 12 |

From the repository root:

```sh
docker compose build
docker compose run --rm --no-deps nodehub -hash-password
```

Enter the password and press Enter. Paste the full result into `auth.password_hash`; `$` characters need no escaping in JSON. Sign in using the original password.

## Upstream settings

| Field | Meaning |
| --- | --- |
| `headscale.enabled` | Enable only after this section has real values |
| `headscale.internal_url` | Management address reachable by the center, using Docker DNS or HTTPS |
| `headscale.tls_skip_verify` | Default false; explicitly skip certificate verification only for this upstream’s internal HTTPS connection |
| `headscale.public_url` | Login address reachable by nodes |
| `headscale.api_key` | Management API key, not a node preauth key |
| `headscale.user_id` | Numeric target user ID encoded as a JSON string; example `1` is not guaranteed correct |
| `headscale.tags` | Preauth tags according to your Headscale policy; may be empty |
| `meshcentral.enabled` | Enable only after this section has real values |
| `meshcentral.internal_url` | Complete control WebSocket URL; local plaintext listeners may need `ws://meshcentral:888/control.ashx`, while TLS listeners use `wss://…/control.ashx` |
| `meshcentral.tls_skip_verify` | Default false; explicitly skip certificate verification only for this upstream’s internal WSS connection |
| `meshcentral.public_url` | Browser-accessible console URL |
| `meshcentral.username` / `password` | The paired Login Token username (`~t:…`) and password |
| `meshcentral.mesh_id` | Complete device-group ID, such as `mesh//…`; not a name or Node ID |
| `meshcentral.device_url_template` | Verified HTTPS device-link template containing `{node_id}`; leave empty to use the console |

Procedures: [Headscale API key and expiry](../docs/first-deployment.md#create-a-headscale-management-api-key-docker), [MeshCentral Login Token](../docs/first-deployment.md#obtain-a-meshcentral-login-token), [device-group ID](../docs/first-deployment.md#find-the-meshcentral-device-group-id).

## Automatic root terminals

| Field | Meaning |
| --- | --- |
| `terminal.enabled` | Enable the native Agent/browser terminal; false in the basic example |
| `terminal.allowed_origins` | Actual Node Hub browser origin, including scheme and nondefault port, without a path; not an upstream URL |
| `terminal.heartbeat_seconds` | Agent heartbeat interval, default 30 seconds; match the node configuration |
| `terminal.timeout_seconds` | Agent timeout, default 90 seconds; must exceed the heartbeat interval |
| `terminal.max_sessions` | Center-wide terminal limit, default 10 |
| `terminal.max_sessions_per_node` | Per-node limit, default 2 |
| `terminal.idle_seconds` | Close after no input/output for this duration, default 300 seconds |
| `terminal.max_duration_seconds` | Maximum session duration, default 3600 seconds |

Automatic provisioning uses root. Do not add `nodes`, per-node usernames, private-key paths, or host keys to current configuration. The initializer's `--agent-config` flow installs root authorization, pins the host key, and confirms installation. SQLite targets become available immediately without restarting the center.

Use the base Compose or `deploy/compose.example.yaml`, without `secrets/ssh`. `compose.terminal.yaml` is retained only for legacy manual file-based targets. Existing root authorizations are preserved. The Agent is an unprivileged relay; the shell runs as root. Database backups contain login private keys.

The UI defaults to English and offers Simplified Chinese in its language selector; language is a browser preference, not a server configuration field. Setup and migration are covered by the [native terminal manual](../docs/native-terminal.md).

For local MeshCentral, see [WS versus WSS](../docs/docker.md#choose-ws-or-wss-for-a-local-meshcentral-listener). The TLS-skip flag does not fix a protocol mismatch and has no effect on WS.


## HTTPS bootstrap distribution

`bootstrap.enabled` enables the code-only installer endpoints; `bootstrap.assets_dir` is the packaged asset root, `/app/bootstrap` in Docker. New examples enable it. Existing configs must add the section explicitly and deploy a rebuilt image. For local development, point it at the repository root after building `bin/` assets. The UI exposes the copyable command when assets are available. No token belongs in this section. See [bootstrap setup](../docs/bootstrap.md).
