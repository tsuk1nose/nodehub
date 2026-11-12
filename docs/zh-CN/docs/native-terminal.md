# Node Hub 原生终端

[English](../../native-terminal.md) | 简体中文

版本：0.5　日期：2026-11-12　状态：可选 MVP，Debian 13 / N2840 终端链路实测通过

## 1. 范围

在原中心中增加 Go Agent 和浏览器 SSH 终端。保留 Headscale 与 MeshCentral，不替代其既有注册和连接状态。该功能默认关闭；既有配置文件不含 `terminal` 时继续按原行为运行。

支持设备独立 Ed25519 认证、出站连接、心跳、自动重连、多终端、PTY 尺寸调整、SSH 主机公钥校验、单管理员授权、基本审计和原生 Agent 停用。

不实现任意 TCP 代理、原生 PTY、文件传输、自动升级、完整 RBAC、终端内容录像、历史指标采集或多实例调度。

## 2. 数据链路

```text
浏览器 xterm.js ── 已授权 WebSocket ── Node Hub SSH 客户端
                                         │
                                每会话独立 WSS 数据连接
                                         │
                                     Go Agent
                                         │
                                  127.0.0.1:22
                                         │
                                    现有 SSHD
```

Agent 常驻一条 WSS 控制连接，终端按需各开一条数据连接。第一版不实现单连接多路复用。Agent 只按本地配置连接 IPv4 回环地址的固定 SSH 端口，中心不能指定任意目标地址。它本身没有监听端口，不执行 shell，也不持有 SSH 登录私钥。

WSS 由 Agent 主动建立，不依赖 Tailscale 的连通性。中心使用 `golang.org/x/crypto/ssh`，SSH 的认证与加密覆盖整个中继流。

## 3. 设备身份与协议

复用既有节点的 12 位序列号。初始化恢复 Bearer 凭据仅用于登记新的 Agent 公钥，不用于长连接认证，也不转交给常驻 Agent。

Agent 在克隆设备首次初始化时生成 Ed25519 种子，先原子写入身份文件并 fsync，再向 `/api/node/{serial}/agent-enroll` 登记公钥。登记可重试；已有公钥不允许替换，已停用身份不能凭旧初始化恢复凭据重新激活。更换设备身份按新节点注册流程执行，不生成额外 UUID。

控制连接流程：

1. 中心发送随机 32 字节挑战，十六进制编码，绑定本次连接，10 秒内必须回答。
2. Agent 签名 `nodehub-agent-v1\n<serial>\n<challenge>`，发送类型 `auth`、序列号与 Base64 签名。
3. 中心用已登记公钥验证，检查停用状态，成功后返回 `authenticated`。
4. 后续通过 `heartbeat` / `heartbeat_ack` 维持连接。默认 30 秒心跳、90 秒超时，均可配置且两端应一致。
5. 相同序列号建立新连接时替换旧连接，并关闭旧连接的终端。

每个挑战只用于所在连接的一次认证，不能在其他连接重放。未认证握手同时最多 64 个，并设消息长度和超时限制；成功控制连接不继续占用该握手配额。

控制消息使用有大小上限的 JSON，不为少量心跳另造二进制控制协议。数据连接仅承载最大 16 KiB 的二进制字节流帧。

## 4. 终端授权与流控

浏览器先 POST 创建会话，再升级对应 WebSocket。会话 ID 不是授权凭据；WebSocket 仍检查管理登录、创建会话的同一登录会话和明确允许的 Origin。创建后 30 秒未连接则关闭。

中心通过已认证控制连接向 Agent 发送一次性数据票据。票据随机 32 字节，仅绑定该会话、节点及当前连接代次，通过数据连接第一条消息提交；不放 URL，不接受重复消费。旧代次不能接管新终端。

中心严格验证通过已认证初始化流程登记的 SSH 主机公钥，以该节点的独立 SSH 私钥登录 root，请求 PTY 和 shell。协商算法限定为所固定的主机密钥类型，避免 SSHD 提供多把密钥时误选；RSA 主机密钥使用 RSA-SHA2。SSH 建连与 PTY / shell 初始化有超时，不允许跳过主机公钥检查。

默认总会话上限 10、每节点 2、空闲 300 秒、最长 3600 秒。浏览器关闭、退出本地登录、登录过期、Agent 断开或替换、Agent 停用及中心关闭都会结束关联会话。用户输入或输出算活动，持续输出仍受最长时限约束。

浏览器输入队列最多 8 条，每条最多 4096 字节。终端输出每次最多 16 KiB，等待 xterm 实际处理确认后继续，20 秒不确认则失败；SSH stdout / stderr 共享有界输出通道。该策略优先控制内存，对大吞吐、高 RTT 输出的性能仍需后续测量。

## 5. 三类独立凭据

| 凭据 | 所在位置及用途 |
| --- | --- |
| Agent Ed25519 私钥 | 节点 `/var/lib/nodehub-agent/identity.json`，仅验证设备连接 |
| SSH 客户端私钥 | 中心自动生成，每节点独立，保存在中心 SQLite；不下发设备 |
| SSH 主机公钥 | 节点通过 HTTPS 与独立恢复凭据上报，中心首次固定，重试不得替换 |

Agent 通信身份与 root SSH 登录密钥独立。自动配置直接使用 root，不新建维护账号；中心管理员具有节点完整系统管理权限。根目录 OverlayFS 生效前完成身份文件、SSH 主机密钥、authorized_keys 与 Agent 服务的持久基线配置，不依赖 `/data` 挂载。

## 6. 中心配置

可直接复制 `config/config.terminal.example.json` 为实际配置，或在现有中心配置中启用以下字段（Origin 替换成你的中心地址）：

```json
"terminal": {
  "enabled": true,
  "allowed_origins": ["https://你的中心域名"],
  "heartbeat_seconds": 30,
  "timeout_seconds": 90,
  "max_sessions": 10,
  "max_sessions_per_node": 2,
  "idle_seconds": 300,
  "max_duration_seconds": 3600
}
```

允许的 Origin 必须是浏览器访问中心的实际 origin（协议、主机、非默认端口），不是 Headscale 或 MeshCentral 地址。本地开发可显式配置 `http://localhost:5173`；Agent 始终要求 HTTPS / WSS。

正常自动初始化不需要 `nodes` 字段，无需逐节点配置，也不需要 SSH 私钥目录挂载。直接使用基础 Compose 启动中心即可：

```sh
./scripts/start.sh
```

`nodes` 仅作为旧部署的手工配置兼容入口；尚无已确认自动配置记录时可使用。旧文件方式继续要求私钥 0600、容器 UID 10001 可读，以及受信主机公钥。只有使用旧文件方式才需要 `compose.terminal.yaml` 的额外挂载。已有自动配置记录优先使用数据库，不能靠手工配置覆盖已登记主机身份。

现有外部入口需支持 `/agent/control`、`/agent/data` 和浏览器终端 WebSocket；机器通道不能被 Authelia 交互登录拦截。域名、证书和代理配置仍由用户管理。中心配置改变后重启生效，活动终端不跨中心重启恢复。

## 7. Agent 安装与登记

构建（Go 1.26+，当前构建镜像固定 Go 1.27.3）：

```sh
./scripts/build-agent.sh amd64
# 或 arm64；N2840 使用实际安装的 64 位 Linux 时选择 amd64。
```

可以仅用 Docker 导出当前平台二进制：

```sh
docker build --target agent-binary --output ./bin .
```

将安装脚本、unit、配置示例和二进制复制到设备，用 root 安装：

```sh
./agent/install.sh ./bin/nodehub-agent-linux-amd64
```

安装不会登记设备或启动服务。修改 `/etc/nodehub-agent/config.json` 的中心地址；`serial` 可留空，实际身份从既有初始化状态取得。

克隆后的设备通过初始化工具完成 Agent 登记和 SSH 自动配置：

```sh
/usr/local/sbin/nodehub-init \
  --mesh-install-hook /usr/local/lib/nodehub/mesh-install \
  --agent-config /etc/nodehub-agent/config.json
```

这是一条完整初始化命令，仍需准备激活文件、Tailscale 与 MeshAgent hook；已登记但未最终化的节点可用同一独立恢复状态重试。模板统一安装 [首次启动服务覆盖示例](../../../client/nodehub-init-agent.conf.example)，在 ExecStart 中加入 `--agent-config /etc/nodehub-agent/config.json`，随后每台克隆设备自动完成，不逐台操作。安装命令见 [客户端手册](../client/README.md#模板启用自动-root-终端)。不要在模板执行登记或保留登记后的身份。

自动流程：

1. 登记 Agent Ed25519 身份，拒绝已停用身份。
2. 生成缺失的 SSH 主机密钥，启用、启动并重新加载 Debian `ssh.service`，从实际 Ed25519 主机私钥导出公钥并上报。
3. 中心生成每节点独立 SSH 登录密钥，仅返回授权公钥；私钥保存在 SQLite。相同请求重试复用原登录密钥，主机公钥改变则拒绝覆盖。
4. 节点将公钥原子写入 root 的 `authorized_keys` 中按序列号标记的 NodeHub 区块，保留所有其他授权；重试不重复追加。仅对该密钥禁用端口、Agent、X11 转发，保留 root 交互终端。
5. 写入落盘后确认安装，中心动态启用该节点的 SSH 目标，节点启动 Agent。无需修改 JSON 或重启中心。

登录用户为 root，home 取系统账号配置（Debian 通常是 `/root`），home / `.ssh` 权限 700，`authorized_keys` 权限 600，均由 root 拥有。不通过 sudo 间接提权，不依赖 root 密码。Agent 常驻进程仍以独立普通用户运行，只中继本机 SSH；终端由 sshd 以 root 执行。

初始化检查 root 公钥登录的有效 SSH 配置。已有 `PermitRootLogin yes` / `prohibit-password` 时保留；否则通过 `/etc/ssh/sshd_config.d/00-nodehub-root.conf` 设置 `PermitRootLogin prohibit-password`，执行配置检查、重新加载并确认生效，不新增密码登录许可。自定义更早的规则、Match、AllowUsers 或非默认授权路径仍可能阻止登录，应在最终化前实测。

从旧 `nodehub-maint` 版本升级：先更新中心，再用新初始化工具及原状态重试。登录密钥与主机公钥保持不变；只有节点确认 root 授权落盘后，中心才把该节点目标切为 root。客户端随后仅移除自己先前写入且内容完全匹配的旧账号公钥文件，保留旧账号、目录和管理员额外授权；不自动删除账号或数据。

`ssh-provision.json` 本地进度记录在 `/var/lib/nodehub-init`。初始化最终化之前应验证终端可用；常驻 Agent 单独执行 `-enroll` 只登记通信身份，不会安装 root SSH 授权，完整自动配置需要运行上面的初始化命令。

中心数据库与一致性备份现在包含 SSH 登录私钥，须按私钥文件保护。密钥本版未另外做应用层加密，数据库与备份不可公开；现有中心数据卷可直接用于保存，无需额外 SSH 密钥卷。重刷或更换 SSH 主机身份按新节点处理，不允许原初始化凭据静默替换旧主机公钥。

## 8. 审计与停用

`terminal_audit` 保存请求、操作者、节点、开始结束时间、结束原因和接入源地址；不保存终端输入、输出或私钥。中心重启将未结束的记录标为 `server_restarted`。

`security_audit` 保存成功登录、Agent 登记、认证、连接断开与停用等事件。查询接口为 `/api/terminal-audit` 和 `/api/agent-events`，都需要管理认证。

接入源记录的是中心直接看到的地址，经过反向代理时可能是代理地址，不盲目信任外部转发头。关闭本地登录时操作者统一记录为 `external-admin`，本版尚未接入 Authelia 用户身份或外部会话持续有效性检查，因此不声称提供多用户审计或设备级 RBAC。

节点详情中的“停用此 Agent”只撤销 Node Hub 原生身份、关闭连接和终端，并阻止该公钥重新登记；不删除节点，不影响两套上游。Headscale / MeshCentral 联动吊销仍是独立的待联调功能。

## 9. 验证与限制

自动测试覆盖 TLS 中心与 Agent、真实 SSH 协议模拟服务、终端输入输出、PTY resize、错误主机公钥、挑战重放拒绝、Origin 校验、登录会话绑定、并发上限、数据票据单次消费、退出登录、停用、重连及慢浏览器时限。

N2840 / Debian 13 与实际 OpenSSH 终端链路已通过首次实测；Dropbear、OverlayFS 重启、长时间网络波动及持续资源占用仍待验证。不承诺尚未测量的 RSS、吞吐或最大设备数量。不实现会话迁移或断线后恢复原 shell。

安全扫描发现原依赖的 SSH 已知问题后，将 `golang.org/x/crypto` 升级至 v0.58.0；后续依赖更新仍应运行 `govulncheck` 和协议测试。

本次真实浏览器检查已验证登录、节点详情、终端输入回显、窗口变化和关闭，无脚本或 CSP 错误。`go test -race ./...`、前端构建与静态检查通过。升级后的安全扫描为 0 个代码可达漏洞，另有 1 个仅在依赖模块内的未使用发现。amd64 / arm64 二进制已构建。Docker 本次仅验证合并配置，尚未重新验证完整镜像构建或部署。


## 2026-11-12 实机补充

实际浏览器通过独立系统用户运行的 Agent 连接 N2840 SSHD，执行系统信息命令、调整窗口及关闭会话通过。约 6 MB 服务内存仅为单次观测。详见 [联调报告](live-test-2026-11-12.md)。

Agent 身份文件和初始化恢复状态均绑定登记时的中心地址，不支持仅修改配置后切换中心。首次自行部署应先确定节点可长期访问的中心 HTTPS 地址，再创建激活文件并注册新节点。原临时测试设备需保留数据库及设备身份，按迁移方案处理，不能覆盖身份文件规避地址校验。


## 自动 SSH 配置变更验证

已覆盖：独立节点凭据认证、Agent 必须先登记、停用身份拒绝、重复及并发请求复用密钥、不同节点密钥隔离、主机公钥防替换、安装确认前目标不可用、确认后动态读取、SQLite 备份恢复，以及客户端保留已有 root 授权、权限、确认失败恢复和旧维护账号向 root 迁移。前端类型检查及构建通过。

当前 root 版本已完成完整 Go 网络集成测试、9 项客户端测试及静态检查，并在 Debian 13 / N2840 验证旧维护账号迁移、保留既有 root 授权、重复初始化和真实浏览器 root 终端。详见 [root 模型实测记录](live-test-2026-11-12.md#root-终端权限模型追加验收)。
