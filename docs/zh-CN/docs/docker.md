# Docker 部署与构建

[English](../../docker.md) | 简体中文

本项目已提供以下文件。Headscale 与 MeshCentral 由用户独立部署，示例不会重建或覆盖这两个服务，也不包含证书或反向代理配置。

| 文件 | 用途 |
| --- | --- |
| `Dockerfile` | 前端与 Go 多阶段构建，默认 `server` 阶段运行中心 |
| `compose.yaml` | 中心服务、配置文件、SQLite 数据卷及管理网络 |
| `compose.terminal.yaml` | 兼容旧手工 SSH 配置，增加私钥只读挂载 |
| `deploy/compose.example.yaml` | 自动 root 终端的独立完整示例，与基础 Compose 二选一 |
| `config/config.example.json` | 默认关闭原生终端的中心配置 |
| `config/config.terminal.example.json` | 开启自动 root 终端的完整配置，无逐节点参数 |
| `config/README.md` | 配置字段、填写方法和凭据获取入口 |
| `agent/config.example.json` | Debian 原生 Agent 配置示例 |
| `scripts/start.sh` | 启动中心；`--terminal` 为旧手工配置启用 SSH 挂载 |

首次完整部署及节点测试顺序见 [首次部署与验收](first-deployment.md)。默认先测试基础中心；原生终端按需开启。

## 1. 创建配置和密码哈希

在仓库根目录执行：

```sh
cp config/config.example.json config/config.json
docker compose build
# 从标准输入读取管理员密码，输出哈希，填入配置的 auth.password_hash。
docker compose run --rm --no-deps nodehub -hash-password
```

默认开启本地登录，密码哈希未替换时中心拒绝启动。`secure_cookie=true` 用于 HTTPS 入口；仅本地 HTTP 调试时显式改为 false。若已有前置认证，可显式关闭本地登录。

配置文件只读挂载给容器，容器以 UID/GID 10001 运行。将配置内容填好后设置权限（需要宿主机相应权限）：

```sh
sudo chown 10001:10001 config/config.json
sudo chmod 600 config/config.json
```

修改内容后再次确认所有者和权限，避免编辑器原子替换文件导致所有者变化。配置可读性和 SSH 密钥权限由部署方保证；脚本不会递归更改现有 SSH 密钥的所有者。

## 2. 普通启动

```sh
./scripts/start.sh
# 等价：
docker compose up -d --build
```

默认只将中心端口发布到宿主机 `127.0.0.1:8080`。SQLite 在命名卷中，配置更新后重建容器生效（推荐 `docker compose up -d --force-recreate`，确保原子替换的配置文件重新挂载）。`docker compose down` 保留数据；不要在需要保留数据时添加 `-v`。

```sh
docker compose logs --tail=100 -f nodehub
docker compose restart nodehub
docker compose down
```

容器内是已编译的 Go 二进制和静态前端，不运行 Node.js。根文件系统只读，`/data` 为中心数据库卷，`/tmp` 为有限内存盘；这里的 `/data` 与边缘节点磁盘上的 `/data` 是不同存储。

## 3. 启用原生终端

在配置中开启 `terminal.enabled`，`allowed_origins` 填实际中心 Origin，不需要 `nodes` 字段。按照 [原生终端](native-terminal.md) 在模板中预装 Agent / OpenSSH server，并让初始化工具带 `--agent-config` 运行。

```sh
./scripts/start.sh
```

中心自动生成每节点 SSH 登录密钥，保存在已有 SQLite 数据卷；节点安装授权公钥并上报主机公钥。正常批量部署不需要手工私钥文件、不需要 `--terminal` 或额外卷，也不需要逐节点改配置。

`compose.terminal.yaml` 和 `scripts/start.sh --terminal` 的 SSH 挂载仅用于兼容旧的 `terminal.nodes` 手工文件配置。独立 `deploy/compose.example.yaml` 已按自动 root 流程移除 SSH 挂载，不需要 `secrets/ssh`。只有使用该方式才准备 `secrets/ssh`，私钥须 UID 10001 可读、权限 600。覆盖文件重建时继续携带两份 `-f`；从基础 Compose 切换独立示例时须保持项目名，避免换到另一个数据库卷。

中心数据库和备份包含自动生成的登录私钥，保护方式与私钥相同；不要公开数据库或使用 `down -v` 丢弃身份。

## 4. 对接已部署的上游

凭据获取步骤见 [Headscale API key（Docker 命令与有效期）](first-deployment.md#创建-headscale-管理-api-keydocker) 和 [MeshCentral Login Token](first-deployment.md#获取-meshcentral-login-token)。

中心配置区分容器内部地址和外部地址：

- Headscale：`internal_url` 可为 `http://headscale:8080`；`public_url` 是节点实际使用的 `https://headscale.example.com`。
- MeshCentral：`internal_url` 为实际可达的 `ws://…/control.ashx` 或 `wss://…/control.ashx`；`public_url` 是 `https://meshcentral.example.com/`。
- MeshCentral 的 Login Token 生成的 `tokenUser` / `tokenPass` 对应配置的 `username` / `password`。
- `mesh_id` 填完整设备组 ID；具体浏览器查询步骤见 [查询 MeshCentral 设备组 ID](first-deployment.md#查询-meshcentral-设备组-id)，不要填写设备组名称。

使用服务名通信时，上游 Compose 可加入由中心创建的 `nodehub-management` 网络：

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

这是对现有上游 Compose 的网络配置片段，不是完整的 Headscale 部署文件。先启动中心以创建网络，再启动或重建加入它的上游。若上游需要原有其他网络，应保留原有连接。

也可直接使用公网入口进行初次联调，例如 Headscale 内部调用地址填 `https://headscale.example.com`，MeshCentral 填 `wss://meshcentral.example.com/control.ashx`。TLS 必须正常验证；Docker 服务名与证书域名不匹配时，不要通过关闭证书验证解决。

## 5. 导出 Debian Agent

Agent 按当前设计直接运行在节点系统上，由 systemd 管理，方便访问本机 SSHD；本项目不默认将它放入 Docker。

使用 Docker 构建并导出与构建平台相同架构的二进制：

```sh
docker build --target agent-binary --output type=local,dest=./bin .
```

结果为 `bin/nodehub-agent`。复制到节点后：

```sh
./agent/install.sh ./bin/nodehub-agent
```

跨架构构建优先使用现有 Go 脚本：

```sh
./scripts/build-agent.sh amd64
./scripts/build-agent.sh arm64
```

安装不会自动登记或启动 Agent，镜像模板中不要包含已生成身份。设备刷机启动、拿到序列号后再登记，具体步骤见 [原生终端说明](native-terminal.md)。

## 6. 验证状态

基础 Compose、终端覆盖文件和独立示例均可通过 `docker compose config --quiet` 校验。应用编译、前端构建及本地真实浏览器链路已验证；完整镜像构建和实际部署是否通过，以本机 Docker 构建结果为准，不用配置校验代替运行验收。

2026-11-10 实际构建尝试：在拉取基础镜像阶段被当前 Docker daemon 的代理连接拒绝阻塞，尚未完成镜像构建或容器运行验收；不修改宿主机代理配置。


2026-11-12 更新：N2840 / Debian 13 设备与真实上游、实际浏览器 SSH 终端已通过主机进程方式联调；这不替代 Docker 镜像及容器部署验收。此次未启用 OverlayFS 或重启。
