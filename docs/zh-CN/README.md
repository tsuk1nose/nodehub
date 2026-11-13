# Node Hub

[English](../../README.md) | 简体中文

仓库以英文文档和提示为默认。界面右上角可切换“简体中文”，选择会保存在当前浏览器；API 字段、设备编号和终端输出不翻译。

面向 Debian 13 边缘节点的中心管理程序。Go + Vue 3 + SQLite，中心通过 Docker 部署，连接 Headscale 0.29.7 与 MeshCentral 1.3.1。

服务端首次部署与自行测试请按 [首次部署与验收](docs/first-deployment.md) 操作；N2840 的 [实机联调记录](docs/live-test-2026-11-12.md) 已更新。

正式需求见 [架构规范](docs/specification.md)。此前讨论保留在 [讨论稿](docs/design-discussion.md)。[长期采集草稿](docs/telemetry-draft.md)当前不实现。

## 可选原生终端（新增）

已加入 Go Agent、Ed25519 设备认证、WSS 控制连接、独立 SSH 会话数据通道、xterm.js 终端、尺寸调整、并发与时限控制、会话审计和原生 Agent 停用。默认关闭，保留 MeshCentral。详见 [原生终端配置与安装](docs/native-terminal.md)。

Agent 仅连接本机固定 SSH 端口，不接受任意转发目标；SSH 登录私钥由中心按节点生成并存入 SQLite，设备主机公钥在已认证的初始化时固定登记；root 公钥授权自动安装，无需逐台配置。不实现长期资源指标采集。配置示例新增 `terminal`，现有配置文件可继续使用。

## 当前初版

已实现配置文件加载、SQLite 建表、默认管理员登录、可关闭认证、初始化令牌创建及撤销、12 位序列号登记、可恢复注册、节点独立凭据、Headscale 预认证密钥申请与公钥绑定、两个上游批量状态同步，以及节点与令牌管理界面。

MeshCentral 使用 1.3.1 控制 WebSocket 的自动化账号认证与设备查询协议。设备关联暂由管理员在节点详情输入经过核实的 Node ID，中心同时校验名称和设备组，不按同名自动绑定。设备直达 URL 通过配置模板启用，未验证前界面显示控制台入口。设备组 ID 的获取方式见 [操作步骤](docs/first-deployment.md#查询-meshcentral-设备组-id)。

**待联调与未完成：**

- Headscale / MeshCentral 已在真实部署完成注册、绑定和在线状态联调；Docker 镜像构建及容器运行仍需本次部署验收。
- Debian 工具提供分阶段初始化；Tailscale 须预装，MeshAgent 提供本地受信二进制安装 hook，需自行准备二进制、SHA256 与设备组 `.msh` 文件，不自动在线下载安装。
- 已给出设备检查后的 OverlayFS 方案与示例 hook，仍只支持显式调用经审核 hook，不自动假设分区、文件系统或修改 initramfs。`/data` CoW 的具体格式化与挂载配置尚未实现。
- 联动吊销尚未实现；API 明确返回 501，不会只处理一边后误报成功。
- 不实现节点删除或长期数据采集。

## Docker 启动

完整文件清单、自动终端及旧手工挂载与上游网络示例见 [Docker 部署说明](docs/docker.md)。原生终端自动配置使用基础启动脚本；`--terminal` 仅用于旧手工私钥挂载方式。

需要 Docker Compose。上游由用户部署，本项目 Compose 当前只启动中心。

```sh
cp config/config.example.json config/config.json
docker compose build
docker compose run --rm --no-deps nodehub -hash-password
```

上一步从标准输入读取密码并输出 bcrypt 哈希。填入 `auth.password_hash`；默认要求 12–72 字节密码。不要把真实密码写在命令行参数中。设置配置文件权限使容器 UID 10001 可读取，例如在宿主机使用 `chown 10001:10001 config/config.json` 和 `chmod 600 config/config.json`。非 root 启动者须先完成该权限设置。

```sh
./scripts/start.sh
```

中心监听宿主机 `127.0.0.1:8080`，由用户现有入口转发。默认 Secure cookie 需要通过 HTTPS 访问；纯本地 HTTP 开发时显式设 `auth.secure_cookie=false`。关闭认证仅适用于已经保护管理入口的部署。

容器间可共同加入 `nodehub-management` 网络，在配置中使用相应服务名。也可以填写现有网络可达地址。Headscale 的 HTTP 内部地址与公开登录地址分开；MeshCentral 内部 URL 是完整 `wss://…/control.ashx`，子域/路径按实际部署填写。默认验证 TLS 信任链与域名；受信内部连接可对相应上游设置 `tls_skip_verify: true`，仅影响中心访问该上游；私有 CA 需加入容器信任库，或使用受控内部明文网络并显式填写 `ws://` / `http://`。

外部服务没有准备好时保持 `enabled=false`，界面显示“尚未配置”。配置启用后必须填写实际 API key、user ID、MeshCentral 账号和设备组 ID。凭据或地址改变后执行 `docker compose up -d --force-recreate` 重新加载配置，无需重新构建镜像。

`device_url_template` 默认空。验证 MeshCentral 直达 URL 后填入包含 `{node_id}` 的 HTTPS 模板，要求与 `public_url` 同主机；替换值经过 URL 编码。

## 本地开发与验证

Go 1.26+、Node.js 22（或满足 Vite 要求的版本）。

```sh
go mod download
go test -race ./...
go vet ./...
cd web
npm ci
npm run build
```

创建本地配置：数据库例如 `./data/nodehub.db`，`web_dir` 为 `./web/dist`。密码哈希可通过 `go run -buildvcs=false ./cmd/nodehub -hash-password` 生成。

```sh
go run -buildvcs=false ./cmd/nodehub -config config/config.json
```

开发前端可在另一个终端于 `web` 中运行 `npm run dev`，它将 API 请求转发到本地 8080。

健康检查为 `GET /healthz`。状态查询默认 60 秒，可通过 `poll_interval_seconds` 修改，最小 5 秒；界面自动刷新使用相同间隔。查询失败时设备状态可能过期，查看顶部上游同步提示。

## 初始化接口

所有 JSON 写请求须带 `X-NodeHub-Request: 1`。凭据不放 URL。

| 接口 | 认证 | 用途 |
| --- | --- | --- |
| `POST /api/register` | 请求体初始化令牌；恢复时本机凭据 | 提交 `token`、`request_id`、`credential`，返回序列号 |
| `GET /api/node/{serial}` | Bearer 节点凭据 | 查询本机资料和外部 Headscale URL |
| `POST /api/node/{serial}/headscale-key` | Bearer 节点凭据 | 领取短期单次 preauth key |
| `POST /api/node/{serial}/headscale-bind` | Bearer 节点凭据 | 提交本机 `node_key` 并校验上游身份 |
| `POST /api/node/{serial}/stage` | Bearer 节点凭据 | 更新阶段；两套关联未完成不能标记完成 |
| `POST /api/nodes/{serial}/meshcentral-bind` | 管理会话 | 提交经管理员核实的 `node_id` |

`request_id` 和 `credential` 均为客户端生成的 32 字节随机数的 64 位十六进制编码。请求前持久保存。相同请求 ID 只能由相同节点凭据恢复，即便激活令牌已过期或撤销；它恢复既有身份，不创建新身份。

节点自身默认状态目录为 `/var/lib/nodehub-init`，不在 `/data`。中心只保存节点凭据和初始化令牌摘要；用于重试的短期 Headscale 密钥暂存中心数据库，绑定后移除。数据库及备份都应作为秘密文件保护。

## Debian 镜像与首次启动

详见 [客户端操作说明](client/README.md)。当前为可恢复的分阶段初版，首次启动工具不改变 `/data` 挂载，不在模板自动运行，不自动格式化分区或启用未经验证的 OverlayFS。

## 备份

中心使用 SQLite WAL。通过工具的一致性备份生成文件，不能只拷贝运行中的主数据库文件：

```sh
docker compose exec nodehub /app/nodehub -config /app/config.json -backup /data/backup-20261109.db
```

目标必须不存在。将备份安全导出并保存配置。恢复时停止中心，将备份作为数据库恢复，避免残留原 WAL/SHM，确认 UID 10001 的读写权限后启动。备份包含节点认证摘要及暂存的注册秘密，不提交 Git。

## 基础初版验证记录（2026-11-09）

- Go 后端编译、`go test -race ./...` 和 `go vet ./...` 通过。
- 测试覆盖注册幂等、并发额度、过期和撤销令牌、登录退出、备份恢复、上游故障保留状态，以及 Headscale / MeshCentral 模拟协议和设备绑定。
- Vue TypeScript 检查与生产构建通过。
- 本地启动检查通过：健康检查、管理员登录、令牌创建、节点注册与重试、列表查询及前端静态资源。
- Python 初始化工具语法检查与 shell 脚本语法检查通过；未在 Debian 13 实机运行。
- `docker compose config --quiet` 通过；镜像构建因当前 Docker daemon 的镜像代理连接失败而未完成，不代表 Docker 镜像已验证可运行。

## 原生终端验证（2026-11-10）

- 新增 TLS 中心 → Agent → SSH 服务的端到端测试，验证输入输出、窗口尺寸及结束审计。
- 覆盖挑战重放、数据票据单次消费、Origin、登录会话归属、并发限制、错误主机公钥、退出登录、Agent 停用与自动重连、慢浏览器时限。
- Go SSH 依赖升级为 `golang.org/x/crypto v0.58.0`，模块最低 Go 版本提升至 1.26；Docker 构建工具链固定为 Go 1.27.3。
- 安装脚本、unit 和可选初始化集成已提供；N2840 / Debian 13 与实际 SSHD 已于 2026-11-12 验证；OverlayFS 仍待验证。

- 真实 Chromium 浏览器测试通过：管理登录、节点详情、xterm 输入回显、窗口调整与关闭，无脚本或 CSP 错误；仅使用本机隔离 SSH 测试服务。
- `govulncheck` 升级后扫描结果为 0 个代码可达漏洞；另有 1 个仅存在于依赖模块、当前未导入或调用的发现，不等于完整安全审计。
- Linux amd64 / arm64 Agent 静态二进制已构建至 `bin/`（该目录忽略提交）。可选 Compose 配置合并与脚本语法检查通过。

## 实机验证更新（2026-11-12）

Debian 13 / Intel N2840 已完成真实 Headscale、MeshCentral 注册与关联、重复初始化恢复，以及 NodeHub 浏览器 SSH 终端测试。修复包含 Headscale 掩码密钥兼容、MeshAgent `-install` 参数和多主机密钥 SSH 协商；Go 并发测试与静态检查通过。完整记录见 [实机报告](docs/live-test-2026-11-12.md)。

测试节点保留已注册身份，但临时 NodeHub 中心已停止；其恢复状态与 Agent 身份仍绑定临时地址。**自行测试建议使用干净的新节点或新克隆镜像**；不要清空原测试节点状态重新登记，也不要将其注册后状态当模板。复用该节点及原记录须先处理中心数据库与身份地址迁移，当前没有自动迁移工具。

## 当前 root 终端部署方式

中心可从 [自动终端完整配置](../../config/config.terminal.example.json) 创建实际配置，填写管理密码哈希与上游凭据、替换中心 Origin，无需配置逐节点 SSH 参数；使用基础 Compose 即可，不需要 SSH 密钥挂载。[字段说明](config/README.md)解释各项配置及凭据获取方式。

节点初始化指定 `--agent-config /etc/nodehub-agent/config.json` 后，自动登记 Agent、安装 root 公钥授权并确认 SSH 配置。模板可统一使用 [首次启动服务覆盖示例](../../client/nodehub-init-agent.conf.example)，不需要逐台配置或重启中心。Agent 中继进程以普通用户运行，终端由 sshd 以 root 执行。中心数据库及备份包含每节点 SSH 私钥。

完整 Go 网络集成测试、9 项客户端测试及静态检查通过；旧维护账号向 root 迁移、保留原有授权、重复初始化及浏览器 root 终端已在 N2840 实测通过。[操作与升级说明](docs/native-terminal.md)，[实机记录](docs/live-test-2026-11-12.md)。

## 新增安装与存储方案

前端可复制 HTTPS 安装指令，节点本地隐藏输入授权令牌；现有配置需开启 bootstrap 并重新构建镜像。见 [交互安装](docs/bootstrap.md) 和 [设备检查后的 OverlayFS 方案](docs/overlayroot.md)。安装不会启用 OverlayFS 或重启。
