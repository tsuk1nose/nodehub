# Docker deployment and builds

English | [简体中文](zh-CN/docs/docker.md)

The operator deploys Headscale and MeshCentral independently. These examples do not replace their data or configure domains, certificates, or reverse proxies. For the complete sequence, use the [first deployment guide](first-deployment.md).

| File | Purpose |
| --- | --- |
| `Dockerfile` | Frontend/Go stages, `server` default runtime, `agent-binary` export |
| `compose.yaml` | Center, read-only configuration, SQLite volume, management network |
| `compose.terminal.yaml` | Legacy manual SSH-key read-only mount only |
| `deploy/compose.example.yaml` | Standalone automatic root-terminal example; alternative to base Compose |
| `config/config.example.json` | Center configuration with native terminal disabled |
| `config/config.terminal.example.json` | Automatic root terminal enabled; no per-node fields |
| `config/README.md` | Configuration reference and credential instructions |
| `agent/config.example.json` | Native Agent configuration |
| `scripts/start.sh` | Normal startup; `--terminal` adds the legacy key mount |

## 1. Configuration and password hash

From the repository root:

```sh
cp config/config.example.json config/config.json
# Or copy config/config.terminal.example.json for automatic root terminals.
docker compose build
docker compose run --rm --no-deps nodehub -hash-password
```

Enter a password on standard input and paste the complete bcrypt hash into `auth.password_hash`. The center rejects the unchanged placeholder. HTTPS uses `secure_cookie=true`; only local HTTP debugging should set it false. Local sign-in can be explicitly disabled behind an existing protected entry.

The configuration is mounted read-only. The container runs as UID/GID 10001:

```sh
sudo chown 10001:10001 config/config.json
sudo chmod 600 config/config.json
```

After editing, recheck ownership because editors can atomically replace files. The script does not recursively change existing SSH-key ownership. Bind mounts use `create_host_path: false`: a missing configuration is an error rather than a silently created directory.

## 2. Normal startup and persistence

```sh
./scripts/start.sh
# Equivalent:
docker compose up -d --build
```

The listener is published on host loopback `127.0.0.1:8080`. SQLite uses a named volume. Recreate after configuration changes so the file is remounted:

```sh
docker compose up -d --force-recreate
docker compose logs --tail=100 -f nodehub
docker compose down
```

`down` keeps the data volume; do not add `-v` when retaining identities. Normal runtime contains compiled Go and static assets, not Node.js. The container root is read-only, `/tmp` is a bounded tmpfs, and `/data` is writable center storage. This `/data` is unrelated to node `/data`.

`build.context` identifies the source tree; `dockerfile` defaults to `Dockerfile`, and `target: server` selects the runtime stage. The basic Compose uses a Docker-managed named volume. A host-directory bind is also possible, but the directory must already have UID 10001 write permissions and its identity data must be retained across rebuilds.

## 3. Automatic root terminals

Set `terminal.enabled=true` and the actual browser Origin in `allowed_origins`. Do not configure per-node SSH entries. Install Agent/OpenSSH on the template and run the initializer with `--agent-config`; follow the [native terminal manual](native-terminal.md).

```sh
./scripts/start.sh
```

Per-node private keys live in SQLite, and nodes automatically install root public authorization and report their host keys. No `secrets/ssh`, extra mount, `--terminal`, or per-node restart is required. Database backups now contain SSH login private keys.

For legacy `terminal.nodes` file-based targets only, prepare `secrets/ssh`, readable by UID 10001 with private files mode 600, and use:

```sh
docker compose -f compose.yaml -f compose.terminal.yaml up -d --build
```

Keep both `-f` arguments on later rebuilds of that legacy deployment. The standalone current example has no SSH mount:

```sh
docker compose -p node_hub -f deploy/compose.example.yaml up -d --build
```

Use one deployment, not two simultaneous stacks. Preserve the original project name when switching; otherwise Compose can create a different database volume.

## 4. Existing upstreams

Obtain credentials using [Headscale API key creation](first-deployment.md#create-a-headscale-management-api-key-docker) and [MeshCentral Login Tokens](first-deployment.md#obtain-a-meshcentral-login-token). Find the full group ID using the [browser lookup procedure](first-deployment.md#find-the-meshcentral-device-group-id).

- Headscale `internal_url` can be `http://headscale:8080`; `public_url` must be reachable by nodes.
- MeshCentral `internal_url` is a complete `ws://…/control.ashx` or `wss://…/control.ashx`; `public_url` is its browser console.
- Login Token `tokenUser`/`tokenPass` map to `username`/`password`.
- A group name or device Node ID cannot substitute for `mesh_id`.

To use Docker DNS, add the existing upstream service to the center-created network:

```yaml
services:
  headscale:
    networks:
      - management
networks:
  management:
    external: true
    name: nodehub-management
```

This is a network fragment, not a complete Headscale installation. Start the center first to create the network, then recreate upstream containers with the added connection. Retain their other required networks.

For initial testing, use public HTTPS/WSS URLs such as `https://headscale.example.com` and `wss://meshcentral.example.com/control.ashx`. TLS verification is mandatory; do not disable it to accommodate Docker names that do not match certificates. Add trusted private CAs when needed.

A reverse proxy running in another container must reach the center through a shared network or deliberate host routing. Its own `127.0.0.1` is not the center.

## 5. Agent binaries

The Agent runs directly on Debian under systemd to reach its local SSHD. It is not containerized by default.

```sh
docker build --target agent-binary --output type=local,dest=./bin .
```

This exports `bin/nodehub-agent` for the build platform. Copy the installer, unit, configuration example, and binary to the node before running its installer. For explicit cross-compilation with Go:

```sh
./scripts/build-agent.sh amd64
./scripts/build-agent.sh arm64
```

Generated files are `bin/nodehub-agent-linux-amd64` / `bin/nodehub-agent-linux-arm64`. Installation does not enroll or start the template. Configure and enroll only after cloning; see the [native terminal procedure](native-terminal.md).

## 6. Validation

Base Compose, legacy overlay, and standalone example pass `docker compose config --quiet`. Backend/frontend builds and real N2840 browser SSH tests have passed. Configuration validation does not establish container runtime acceptance.

The earlier complete image build was blocked while pulling base images by the host Docker daemon's proxy connection. No host proxy changes were made. Production Docker runtime, OverlayFS, and full clone acceptance remain separate deployment checks.
