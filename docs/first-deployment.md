# First deployment and acceptance guide

English | [简体中文](zh-CN/docs/first-deployment.md)

Use a new center and a clean Debian 13 node for this procedure. Deploy Headscale **0.29.7** and MeshCentral **1.3.1** separately; Node Hub Compose starts only the center. Keep your existing certificate/domain/entry setup.

## 1. Prepare the center

From the repository root:

```sh
cp config/config.example.json config/config.json
docker compose build
docker compose run --rm --no-deps nodehub -hash-password
```

For native root terminals, copy `config/config.terminal.example.json` instead. Enter a 12–72 byte administrator password and copy its complete bcrypt hash. Keep `$` characters unchanged in JSON. Sign in with the original password. See the [configuration reference](../config/README.md).

| Field | Initial value or source |
| --- | --- |
| `auth.username` / `password_hash` | Your local administrator name and generated bcrypt hash |
| `auth.secure_cookie` | Keep true behind HTTPS |
| `headscale.enabled` | true after filling this section |
| `headscale.internal_url` | For an initial test, `https://headscale.example.com`, replaced with your actual entry |
| `headscale.tls_skip_verify` | false by default; optional override for trusted internal HTTPS only |
| `headscale.public_url` | The actual Headscale login address reachable by nodes |
| `headscale.api_key` | A current management API key |
| `headscale.user_id` | Actual target numeric user ID as a JSON string; do not assume `1` |
| `meshcentral.enabled` | true after filling this section |
| `meshcentral.internal_url` | Match the listener: local plaintext may use `ws://meshcentral:888/control.ashx`; use WSS only for a TLS listener |
| `meshcentral.tls_skip_verify` | false by default; set true for the requested internal WSS certificate override |
| `meshcentral.public_url` | Actual browser console entry |
| `meshcentral.username` / `password` | Paired Login Token username (`~t:…`) and password |
| `meshcentral.mesh_id` | Full group ID (`mesh//…` or `mesh/domain/…`), not a name/Node ID |
| `meshcentral.device_url_template` | Empty until a deep-link template is verified |
| `poll_interval_seconds` | 60 |
| `terminal.enabled` | false for basic enrollment, true when testing native terminals |

### Create a Headscale management API key (Docker)

Run in the **Headscale Compose directory**, with its service named `headscale`; replace the service name if different. These commands run Headscale's CLI, not the Node Hub binary.

```sh
# A management key valid for 365 days.
docker compose exec headscale headscale apikeys create --expiration 365d

# Show IDs, prefixes, and expiry; full secrets are not retrievable here.
docker compose exec headscale headscale apikeys list

# Find the numeric user ID for headscale.user_id.
docker compose exec headscale headscale users list
```

If using a container name instead of Compose:

```sh
docker exec -it HEADSCALE_CONTAINER headscale apikeys create --expiration 365d
docker exec -it HEADSCALE_CONTAINER headscale apikeys list
```

Replace `HEADSCALE_CONTAINER`. In 0.29.7, `--expiration` (or `-e`) is a relative lifetime, default **90 days**. Examples: `30m`, `24h`, `365d`. A long value such as `3650d` still expires; do not transfer Node Hub's unlimited enrollment-token semantics to this flag.

Save the full key immediately into `headscale.api_key`; it is shown only at creation. If lost, create a replacement. This key authorizes the center's management API calls. It is not a node preauth key or a Node Hub enrollment token.

Rotate by creating a new key, updating local configuration, recreating Node Hub, checking successful sync, then expiring the old key using its ID from the list:

```sh
docker compose exec headscale headscale apikeys expire --id OLD_KEY_ID
```

Replace `OLD_KEY_ID`. Expiring a management key does not delete Headscale nodes. If Headscale uses a nondefault configuration path, supply its corresponding `--config` flag.

Reference: [Headscale 0.29.7 API key CLI](https://github.com/juanfont/headscale/blob/v0.29.7/cmd/headscale/cli/api_key.go).

### Obtain a MeshCentral Login Token

A MeshCentral Login Token is a pair of temporary login credentials, not a single Bearer token.

1. Sign into MeshCentral using the intended automation account's normal authentication. It must have access to the target group.
2. Open **My Account**, then **Create login token**.
3. Set a recognizable name, such as `nodehub`, and an **Expire Time**. The 1.3.1 default page offers **Unlimited** and finite options from 1 minute through 7 days. Unlimited is suitable for the requested early testing setup and remains manually revocable.
4. Immediately save both **Username** (usually beginning `~t:`) and **Password**. The later list cannot recover the original password; create a new token if lost.
5. Fill the actual center configuration:

```json
"meshcentral": {
  "username": "~t:REPLACE_WITH_TOKEN_USERNAME",
  "password": "REPLACE_WITH_TOKEN_PASSWORD"
}
```

This snippet shows credentials only; keep the other fields. Do not mix an account's ordinary username with a token password, and never distribute these management credentials to nodes.

If creation is missing, check whether you signed in with another Login Token: such sessions cannot create more tokens. The server can also disable/restrict the feature through `passwordRequirements.loginTokens`. Do not remove restrictions indiscriminately just to display the button.

Rotate by creating a new token, updating/recreating the center, checking synchronization, then deleting the old token from My Account. Authentication failures such as `noauth-2d` warrant checking the pair, expiry, and revocation.

References: [1.3.1 UI](https://github.com/Ylianst/MeshCentral/blob/1.3.1/views/default.handlebars), [server validation](https://github.com/Ylianst/MeshCentral/blob/1.3.1/meshuser.js).

### Find the MeshCentral device-group ID

The group ID is a string identifier, not its display name or a device Node ID. In the logged-in MeshCentral 1.3.1 default management page:

1. Wait for the page to finish loading with an account that can access the group.
2. Press **F12**, select **Console**, and use the main MeshCentral page execution context.
3. Run this read-only lookup:

```js
console.table(
  Object.entries(meshes).map(([id, group]) => ({
    name: group.name,
    mesh_id: id
  }))
);
```

Find the intended `name` and copy the complete `mesh_id`, including `mesh//…`. A configured domain may produce `mesh/domain/…`; do not invent or remove the prefix.

```json
"meshcentral": {
  "mesh_id": "mesh//REPLACE_WITH_ACTUAL_GROUP_ID",
  "device_url_template": ""
}
```

The snippet omits other required fields. Leave the URL template empty until validated.

If `meshes is not defined`, verify that this is the logged-in MeshCentral main page, not Node Hub, an Agent console, or an iframe. Customized pages/future versions require rechecking. Missing groups indicate account permissions; the token's account must also have access. Do not paste a group name, `node/…` ID, or a shortened/encoded installer URL parameter.

Reference: [MeshCentral 1.3.1 page source](https://github.com/Ylianst/MeshCentral/blob/1.3.1/views/default.handlebars). Never put actual IDs/credentials back into repository examples.

### Finish configuration and start

Keep container paths `/data/nodehub.db`, `/app/web`, and `:8080`. Set permissions and start:

```sh
sudo chown 10001:10001 config/config.json
sudo chmod 600 config/config.json
docker compose config --quiet
./scripts/start.sh
```

For initial testing, public upstream URLs avoid having to join a shared Docker network first. See [network instructions](docker.md). After later edits, use `docker compose up -d --force-recreate` to remount/reload configuration; no rebuild is needed for configuration alone.

## 2. Check the center

```sh
docker compose ps
docker compose logs --tail=100 nodehub
curl --fail http://127.0.0.1:8080/healthz
```

Open your HTTPS entry and sign in. Check page loading, local authentication, and healthy Headscale/MeshCentral sync indicators. A new database has no nodes: it does not import every upstream device automatically.

Create an enrollment token; unlimited lifetime/uses are supported for early images. Save its one-time secret in the activation file. A successful health check only establishes HTTP availability, not upstream authentication.

Host port publishing is loopback-only. A containerized reverse proxy should use a shared network or deliberate host routing; its own localhost is not the center.

## 3. Initialize a clean node

Use a clean Debian 13 device/clone, not the earlier already-enrolled test node. Prepare Python 3, CA certificates, and Tailscale, then follow the [client manual](../client/README.md).

Create root-owned mode-600 `/etc/nodehub/activation.json` from the [example](../client/activation.example.json). Use the node-reachable **long-term center HTTPS URL** and enrollment token, not a Docker service name or temporary tunnel address.

Configure the [MeshAgent hook example](../client/mesh-install.example.json) with an unregistered binary, verified SHA256, and settings downloaded from the same target group. Files must be root-owned and not writable by other users. Do not clone an installed Agent database/certificate.

Run once on the test node:

```sh
/usr/local/sbin/nodehub-init --mesh-install-hook /usr/local/lib/nodehub/mesh-install
```

Acceptance:

1. One 12-character serial appears centrally and becomes the hostname.
2. Tailscale joins the specified Headscale; the center displays its addresses/state.
3. The matching MeshCentral group contains an online device with that display name. Verify its full Node ID and explicitly confirm the binding in Node Hub.
4. Wait one polling interval for both upstream states.
5. Repeat the same initialization command. It must reuse the serial/external identities and leave the activation file deleted.

With HTTPS bootstrap, MeshCentral association and finalization are automatic. `awaiting_binding` indicates a pending upstream match; duplicates/conflicts stop progression. `awaiting_overlay` indicates cleanup/reboot scheduled, without claiming verified boot acceptance. Custom manual installations can still use `--finalize`.

## 4. Optional automatic root terminal

Follow the [native terminal manual](native-terminal.md):

1. Enable `terminal.enabled` and the actual Node Hub browser Origin.
2. Preinstall Agent/OpenSSH and set the same long-term center URL. Add `--agent-config /etc/nodehub-agent/config.json` to initialization, or install the template-wide [service drop-in](../client/nodehub-init-agent.conf.example).
3. Do not add `terminal.nodes`. Keys and host pins are provisioned automatically; Agent and SSH identities remain separate.
4. Use base `./scripts/start.sh`. The initial feature/Origin edit requires recreation; subsequent device provisioning is dynamic.
5. Open the node terminal and run `hostname`, `id -un`, and `uname -m`. Expect root. Test resize and close before finalization.

Machine routes `/api/register`, `/api/node/…`, `/agent/control`, and `/agent/data` must bypass interactive authentication pages while retaining their own authentication. Allow their WebSocket traffic. Management pages can retain local sign-in or existing external authentication.

## 5. Persistence and limits

Keep the Compose SQLite volume; `docker compose down` retains it, `down -v` does not. Use [consistent backup/restore](../README.md#backup-and-restore). Databases, backups, configuration, and recovery files contain secrets or hashes and are not source artifacts.

The earlier test device retains upstream identities and a Node Hub identity tied to `https://localhost:18443`. Its temporary center configuration/database are in ignored `tmp/live-test/`, not distributed with the source. The native Agent is stopped and disabled; upstream/SSH services remain active. A new center cannot take over just by changing a URL: preserve database/state and plan address migration. There is no automated migration tool; do not delete identity files to bypass checks.

Real upstream enrollment, retries, automatic root authorization/migration, and browser terminals have passed device tests. Production Docker runtime, complete clone/OverlayFS lifecycle, CoW mount-failure behavior, power-loss recovery, and linked revocation remain pending. Linked revocation returns 501; historical telemetry is not implemented.

## Interactive installer and OverlayFS

For the new HTTPS command and local hidden token prompt, see [bootstrap](bootstrap.md). Template prerequisites, Btrfs mount degradation, next-boot activation, maintenance, and mutable-identity limits are covered by the [OverlayFS plan](overlayroot.md). Neither is automatically finalized by installation.
