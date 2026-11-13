# 中心配置填写说明

[English](../../../config/README.md) | 简体中文

配置入口二选一，均复制为 `config/config.json` 后再填写：

| 示例 | 适用场景 |
| --- | --- |
| `config.example.json` | 先测试登记、上游状态和 MeshCentral 入口，原生终端关闭 |
| `config.terminal.example.json` | 同时启用 NodeHub 自动 root 终端，无需逐节点参数 |

示例只含占位地址，不填写实际域名、API key 或 Login Token。真实 `config.json` 已被 Git 和 Docker 构建忽略。容器 UID/GID 为 10001，配置文件需可读、权限 600；修改后重建容器重新挂载并加载配置。

## 基础配置与登录

| 字段 | 含义及填写方式 |
| --- | --- |
| `listen` | 容器默认 `:8080` |
| `database` | 中心数据库，容器默认 `/data/nodehub.db`，需持久卷 |
| `web_dir` | 编译后的前端资源，容器默认 `/app/web` |
| `poll_interval_seconds` | 上游及界面刷新间隔，默认 60 秒，最小 5 秒 |
| `auth.enabled` | 本地管理员登录；关闭时由部署方保护管理入口 |
| `auth.username` | 本地管理员用户名 |
| `auth.password_hash` | bcrypt 哈希，不能填明文；密码 12–72 字节 |
| `auth.secure_cookie` | HTTPS 保持 true；仅本地 HTTP 调试时显式设 false |
| `auth.session_hours` | 本地登录会话有效小时数，默认示例为 12 |

生成管理员密码哈希，在仓库根目录执行：

```sh
docker compose build
docker compose run --rm --no-deps nodehub -hash-password
```

输入密码并回车，将输出完整填入 `auth.password_hash`。JSON 中的 `$` 原样保留，无需转义；登录使用原始密码。

## 上游配置

| 字段 | 含义 |
| --- | --- |
| `headscale.enabled` | 是否连接 Headscale；启用前填写本组实际值 |
| `headscale.internal_url` | 中心可访问的管理接口地址，可用容器服务名或 HTTPS 入口 |
| `headscale.tls_skip_verify` | 默认 false，仅控制中心访问该上游 HTTPS 时是否跳过证书校验 |
| `headscale.public_url` | 节点实际可访问的 Headscale 登录地址 |
| `headscale.api_key` | Headscale 管理 API key，不是节点 preauth key |
| `headscale.user_id` | 目标 Headscale 用户的数字 ID，以 JSON 字符串填写；示例 `1` 不保证适合实际部署 |
| `headscale.tags` | 新节点预授权的标签列表，按实际 Headscale 策略配置，可留空 |
| `meshcentral.enabled` | 是否连接 MeshCentral；启用前填写本组实际值 |
| `meshcentral.internal_url` | 完整控制 WebSocket 地址；本地明文监听可能应填 `ws://meshcentral:888/control.ashx`，TLS 监听才使用 WSS |
| `meshcentral.tls_skip_verify` | 默认 false，受信内部 WSS 连接可显式设为 true |
| `meshcentral.public_url` | 浏览器可访问的 MeshCentral 控制台地址 |
| `meshcentral.username` / `password` | 同一 Login Token 生成的用户名 `~t:…` 与配套密码 |
| `meshcentral.mesh_id` | 完整设备组 ID，形如 `mesh//…`，不是组名或设备 Node ID |
| `meshcentral.device_url_template` | 设备直达 URL 模板，需含 `{node_id}`；未实测时保持空，界面提供控制台入口 |

具体操作：[Docker 创建 Headscale API key 与有效期](../docs/first-deployment.md#创建-headscale-管理-api-keydocker)、[获取 MeshCentral Login Token](../docs/first-deployment.md#获取-meshcentral-login-token)、[查询设备组 ID](../docs/first-deployment.md#查询-meshcentral-设备组-id)。

## 自动 root 终端

| 字段 | 含义 |
| --- | --- |
| `terminal.enabled` | 是否启用原生 Agent / 浏览器终端，基础示例默认 false |
| `terminal.allowed_origins` | NodeHub 网页实际访问的 Origin，含协议和非默认端口，不带路径；不是上游服务地址 |
| `terminal.heartbeat_seconds` | Agent 心跳间隔，默认 30 秒，节点 Agent 应与之匹配 |
| `terminal.timeout_seconds` | Agent 通信超时，默认 90 秒，需大于心跳间隔 |
| `terminal.max_sessions` | 中心终端会话总数上限，默认 10 |
| `terminal.max_sessions_per_node` | 每节点终端上限，默认 2 |
| `terminal.idle_seconds` | 无输入输出时自动关闭会话的秒数，默认 300 |
| `terminal.max_duration_seconds` | 会话最长秒数，默认 3600 |

自动流程不需要 `nodes` 字段，固定以 root 登录，无需额外填写 `username`、私钥路径或节点主机公钥。节点通过初始化工具 `--agent-config` 安装 root 授权、固定主机公钥并确认；中心即时读取 SQLite 中的每节点私钥，无需为新节点重启。

使用基础 `compose.yaml` 或独立 `deploy/compose.example.yaml`，不需要 `secrets/ssh`。`compose.terminal.yaml` 仅用于旧手工文件配置。root 的其他授权保留，重试不重复追加；Agent 中继进程保持普通用户，实际 shell 为 root。数据库和备份包含登录私钥，应按秘密保存。

完整模板准备、安装和旧账号升级见 [原生终端手册](../docs/native-terminal.md)。

本地 MeshCentral 详见 [WS 与 WSS 的区别](../docs/docker.md#本地-meshcentral-可能使用-ws-而非-wss)。跳过证书校验无法解决协议不匹配，对 WS 不生效。

## HTTPS 安装资源

`bootstrap.enabled` 开启代码安装入口，`bootstrap.assets_dir` 为资源目录，Docker 默认 `/app/bootstrap`。新示例已开启；现有配置需增加该段并重建镜像。前端在资源可用时显示复制指令，令牌由节点本地输入，不写在此配置。见 [安装手册](../docs/bootstrap.md)。
