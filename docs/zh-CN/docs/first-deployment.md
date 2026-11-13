# 服务端首次部署与自行验收

[English](../../first-deployment.md) | 简体中文

本文以新中心、新节点为测试对象。Headscale 固定 0.29.7、MeshCentral 固定 1.3.1，两者由你单独部署；NodeHub 的 Compose 只启动中心。域名、证书和入口沿用你的部署。

## 1. 准备中心配置

在仓库根目录执行：

```sh
cp config/config.example.json config/config.json
docker compose build
docker compose run --rm --no-deps nodehub -hash-password
```

需要原生 root 终端时，可将第一条复制命令改用 `config/config.terminal.example.json`；仍须填写 Origin、密码哈希和上游配置。两种示例的字段说明见 [配置手册](../config/README.md)。

最后一步输入管理员密码并回车，取得 bcrypt 哈希。填写 `config/config.json`：

| 字段 | 首次联调填写方式 |
| --- | --- |
| `auth.username` / `auth.password_hash` | 你的本地管理员账号与生成的哈希 |
| `auth.secure_cookie` | HTTPS 入口保持 `true` |
| `headscale.enabled` | `true` |
| `headscale.internal_url` | 初次可填 `https://headscale.example.com`，后续可改容器内地址 |
| `headscale.public_url` | `https://headscale.example.com`，供节点注册使用 |
| `headscale.api_key` | 你当前有效的 Headscale 管理 API key |
| `headscale.user_id` | 实际目标 Headscale 用户的数字 ID，以字符串填写，不默认假定为 `1` |
| `meshcentral.enabled` | `true` |
| `meshcentral.internal_url` | 匹配实际监听：本地明文可用 `ws://meshcentral:888/control.ashx`，监听本身提供 TLS 才用 WSS |
| `meshcentral.public_url` | `https://meshcentral.example.com/` |
| `meshcentral.username` / `password` | 有目标设备组权限的 Login Token 用户名（`~t:…`）及配套密码 |
| `meshcentral.mesh_id` | 完整设备组 ID，形如 `mesh//…`，不是设备组名称或某台设备的 `node//…` |
| `meshcentral.device_url_template` | 先留空，界面提供控制台入口 |
| `poll_interval_seconds` | `60` |
| `terminal.enabled` | 基础测试先保持 `false`，需要原生终端时按第 4 节启用 |

### 创建 Headscale 管理 API key（Docker）

以下命令在 **Headscale 所在的 Compose 目录**执行，假设服务名为 `headscale`。如果服务名不同，请替换；这不是在 NodeHub 容器里执行。

```sh
# 创建 365 天有效的管理 API key。
docker compose exec headscale headscale apikeys create --expiration 365d

# 查看现有密钥的 ID、前缀和到期时间，不会显示完整密钥。
docker compose exec headscale headscale apikeys list

# 查看目标用户 ID，填写到 NodeHub 的 headscale.user_id。
docker compose exec headscale headscale users list
```

若只知道容器名，可用以下等价形式（`HEADSCALE_CONTAINER` 替换成实际容器名）：

```sh
docker exec -it HEADSCALE_CONTAINER headscale apikeys create --expiration 365d
docker exec -it HEADSCALE_CONTAINER headscale apikeys list
```

Headscale 0.29.7 的 `--expiration`（简写 `-e`）设置从创建时起的有效时长，默认 **90 天**。例如 `30m` 为 30 分钟、`24h` 为 24 小时、`365d` 为 365 天。需要更长有效期可显式设置如 `3650d`，这仍是有到期时间的密钥；不要把 NodeHub 初始化令牌的“无限期”设置套用到此参数。

创建时立即保存完整输出，填入 `headscale.api_key`。完整 API key 只在创建时显示，列表不能找回；丢失后创建新 key。这里创建的是**中心调用 Headscale 管理接口的凭据**，不是节点加入网络使用的 preauth key，也不是 NodeHub 初始化令牌。

换 key 时先创建新 key，更新 NodeHub 的实际配置并重建中心，确认同步恢复后再使旧 key 失效：

```sh
# 先从 apikeys list 确认旧 key 的 ID，再替换下面的占位值。
docker compose exec headscale headscale apikeys expire --id OLD_KEY_ID
```

凭据到期或被撤销后，中心无法继续正常调用该管理 API；这不是删除已有 Headscale 节点的操作。命令使用官方镜像默认配置路径；如果你的部署使用自定义配置文件，命令也需提供对应 `--config` 路径。

依据：[Headscale 0.29.7 API key 命令实现](https://github.com/juanfont/headscale/blob/v0.29.7/cmd/headscale/cli/api_key.go)。

### 获取 MeshCentral Login Token

MeshCentral 1.3.1 的 Login Token 是一组用于自动化登录的**临时用户名和密码**。NodeHub 使用这组值连接控制 WebSocket，不是填写一个单独的 Bearer token。

1. 使用目标自动化账号的正常登录方式登录 MeshCentral。该账号应具有目标设备组的访问权限。
2. 打开 **My Account（我的账户）**，点击 **Create login token（创建登录令牌）**。
3. 填写易识别的名称，例如 `nodehub`，并选择 **Expire Time（有效期）**。1.3.1 默认页面提供 **Unlimited（不限期）**以及从 1 分钟到 7 天的多个选项；早期长期联调可选择 Unlimited，仍可随时手动删除。
4. 创建后立即保存弹窗中的 **Username** 和 **Password**。用户名通常以 `~t:` 开头。后续列表可查看令牌和期限、删除令牌，不能靠列表重新取得原密码；遗失时重新创建。
5. 将这组值写入 NodeHub 的实际配置文件：

```json
"meshcentral": {
  "username": "~t:REPLACE_WITH_TOKEN_USERNAME",
  "password": "REPLACE_WITH_TOKEN_PASSWORD"
}
```

这里只展示凭据字段，地址、设备组 ID 等仍需填写。不要把账号原始用户名与 token 密码混用，也不要把这组凭据分发到边缘节点。

若没有“创建登录令牌”入口，检查是否正在使用另一个 Login Token 登录；该登录方式不能继续创建 token。还需检查服务端是否通过 `passwordRequirements.loginTokens` 禁用或限制了该功能；不要为了显示按钮直接放开所有账号。名称显示可能随界面语言不同。

更换时创建新 token，更新 NodeHub 配置并重建中心，确认同步成功后，再到 My Account 的登录令牌列表删除旧 token。`noauth-2d` 等认证失败提示应优先检查用户名 / 密码是否配对，以及 token 是否过期或被删除。

依据：[MeshCentral 1.3.1 令牌创建页面](https://github.com/Ylianst/MeshCentral/blob/1.3.1/views/default.handlebars)、[服务端创建与权限检查](https://github.com/Ylianst/MeshCentral/blob/1.3.1/meshuser.js)。实际凭据仅放本地配置，不回填仓库示例。

受信内部 HTTPS/WSS 可在对应的 `headscale` / `meshcentral` 中设置 `tls_skip_verify: true`（默认 false），只跳过中心访问该上游的证书校验；节点和 Agent 的 HTTPS 校验不变。HTTP/WS 地址不受该字段影响。

### 查询 MeshCentral 设备组 ID

`meshcentral.mesh_id` 是完整字符串 ID，不是设备组显示名称，也不是单台设备的 Node ID。MeshCentral 1.3.1 可在已登录的管理网页查询：

1. 使用可访问目标设备组的账号登录 MeshCentral，等待页面加载完成。
2. 按 **F12** 打开浏览器开发者工具，切换到 **Console（控制台）**，确认执行上下文为 MeshCentral 主页面。
3. 执行下面的只读查询：

```js
console.table(
  Object.entries(meshes).map(([id, group]) => ({
    name: group.name,
    mesh_id: id
  }))
);
```

4. 按 `name` 找到目标设备组，复制同一行 `mesh_id` 的完整字符串，保留 `mesh/…/…` 前缀。默认域通常为 `mesh//…`；使用 MeshCentral 域时可能为 `mesh/域标识/…`，不要自行增删前缀。
5. 填入中心配置，例如（占位内容必须替换）：

```json
"meshcentral": {
  "mesh_id": "mesh//REPLACE_WITH_ACTUAL_GROUP_ID",
  "device_url_template": ""
}
```

上面只展示两个字段，其余 MeshCentral 配置仍按完整示例填写。`device_url_template` 暂时留空，界面提供控制台入口。

**常见问题：**

- `meshes is not defined`：确认控制台属于已登录的 MeshCentral 主页面，而不是 NodeHub 页面、Agent 终端或 iframe；此方法基于 1.3.1 默认管理页面，定制页面或后续版本需要重新核对。
- 没有目标设备组：确认当前网页登录账号具有设备组访问权限；中心使用的 Login Token 所属账号也必须有相应权限。
- 不要填写组名、`node/…` 设备 ID，或直接复制安装链接中可能被截短、编码的 `id` 参数。
- 填写后按下文重建中心，检查 MeshCentral 同步提示；新中心没有已关联节点时，列表为空不等于设备组配置错误。

查询依据：[MeshCentral 1.3.1 默认管理页面源码](https://github.com/Ylianst/MeshCentral/blob/1.3.1/views/default.handlebars)。不要将实际 ID 或凭据回填到仓库示例文件。

### 完成配置并启动

不要把示例占位符、过期令牌或此前临时测试配置直接当作正式凭据。中心配置保留示例中的容器内数据库 `/data/nodehub.db`、静态页面 `/app/web` 和监听地址 `:8080`。

容器以 UID/GID 10001 运行。填好配置后设置权限：

```sh
sudo chown 10001:10001 config/config.json
sudo chmod 600 config/config.json
docker compose config --quiet
./scripts/start.sh
```

不需要为了首次测试先接入共享 Docker 网络：中心可以先走上面的两个公网入口。内部网络接法见 [Docker 说明](docker.md)。配置文件被只读挂载；修改后建议执行 `docker compose up -d --force-recreate`，重新挂载并加载新内容。只修改配置不需要重新构建镜像。

## 2. 检查服务端

```sh
docker compose ps
docker compose logs --tail=100 nodehub
curl --fail http://127.0.0.1:8080/healthz
```

通过你准备的 HTTPS 入口访问中心并登录。验收：

- 页面可加载，管理员登录成功。
- 顶部 Headscale、MeshCentral 同步提示均正常；新数据库节点列表为空是正常现象，不会自动导入上游的所有设备。
- 可以创建初始化令牌。初期可选择无限期、无限次数；令牌原文只在创建时显示，需要立即保存到激活文件。

健康检查只说明中心 HTTP 服务可用，不保证上游认证成功。上游失败时先核对地址、TLS、Headscale 用户及 API key、MeshCentral Login Token 和设备组权限。

宿主机端口默认仅绑定 `127.0.0.1:8080`。如果入口本身也在容器中，应通过共享网络访问服务；那个容器自己的 `127.0.0.1` 不是宿主机或 NodeHub。

## 3. 用新节点测试初始化

选择干净的 Debian 13 节点或新克隆镜像；先不要用已经登记到临时中心的 `此前的实测节点`。准备 Python 3、CA 证书、Tailscale，并按照 [客户端说明](../client/README.md) 安装初始化工具和 MeshAgent hook。

激活文件 `/etc/nodehub/activation.json` 使用 [示例](../../../client/activation.example.json)，填写**节点实际可访问的长期中心 HTTPS 地址**及刚创建的初始化令牌，root 拥有、权限 600。不要填写中心容器名、容器内端口地址或临时转发地址。

MeshAgent hook 使用 [配置示例](../../../client/mesh-install.example.json)，准备未注册二进制、经过核实的 SHA256 和同一目标设备组下载的 `.msh`。程序和配置文件必须 root 拥有且其他用户不可写。不要把已安装 Agent 的数据库或证书打进模板。

在测试节点手动执行一次：

```sh
/usr/local/sbin/nodehub-init --mesh-install-hook /usr/local/lib/nodehub/mesh-install
```

验收顺序：

1. 中心出现一个 12 位大写字母与数字序列号；设备 hostname 与之相同。
2. Tailscale 向指定 Headscale 注册，中心显示地址和在线状态。
3. MeshCentral 对应设备组出现同名在线设备。核实该设备的完整 Node ID，在 NodeHub 节点详情中确认绑定。
4. 等待一轮同步（默认 60 秒），中心显示两侧在线。
5. 再执行同一初始化命令：应继续使用原序列号及外部身份，不生成第二条记录。检查激活文件已删除。

本阶段完成后界面仍可显示 `awaiting_binding`，不等于关联失败；当前工具不会在管理员确认关联后自动执行最终化。先完成基本验收；OverlayFS 的最终化需另行准备并验证 hook，不能直接照占位示例执行。不要执行 `--finalize` 或清空状态来消除阶段提示。

## 4. 按需测试 NodeHub 原生终端

详细安装和配置见 [原生终端说明](native-terminal.md)。按以下顺序进行：

1. 中心配置 `terminal.enabled=true`，`allowed_origins` 填浏览器访问中心的实际 Origin，含非默认端口。
2. 在节点预装 Agent 和 OpenSSH server，配置同一个长期中心 HTTPS 地址；初始化命令增加 `--agent-config /etc/nodehub-agent/config.json`，自动登记 Agent、安装 root 公钥授权并确认 SSH 配置。
3. 无需填写 `terminal.nodes`。中心自动保存每节点登录私钥和首次上报的主机公钥，不需逐台配置；Agent 的 Ed25519 身份与 SSH 登录密钥是独立凭据。
4. 用基础 `./scripts/start.sh` 启动中心即可；启用功能及 Origin 的初次配置改变需要重建，后续节点登记即时生效。
5. 节点详情显示 Agent 在线并可打开终端。执行 `hostname`、`id -un`、`uname -m`，测试调整窗口和关闭。登录用户为 root，可直接进行系统维护。

Agent 机器通道须可直接访问中心 `/api/register`、`/api/node/…`、`/agent/control` 和 `/agent/data`，不能被交互登录页面拦截；接口本身仍执行令牌或设备认证。WebSocket 通道也需可用。管理界面可继续使用本地登录或你已有的前置认证。

## 5. 数据保留与已知边界

SQLite 保存在 Compose 命名卷。停止服务时可用 `docker compose down`，保留数据时不要加 `-v`。一致性备份与恢复见 [README](../README.md#备份)。数据库、备份、配置和设备恢复文件均按秘密文件保存，不提交仓库。

现有 `此前的实测节点` 已注册为 已分配的序列号，Tailscale 与 MeshAgent 保持运行，NodeHub Agent 停止且未设自启动。原中心数据库和配置位于本工作区被忽略的 `tmp/live-test/`；这些文件不随代码分发。设备恢复状态及 Agent 身份绑定临时地址 `https://localhost:18443`。新建中心不会自动接管该设备；当前无自动迁移工具，复用前需单独处理数据库和身份地址迁移，保留原状态，不能简单改配置或删身份重来。

已实测上游注册、关联、重试、在线状态和 N2840 浏览器终端；Docker 镜像及容器部署仍由此次自行测试验收。OverlayFS、`/data` 挂载失败降级、完整克隆首次启动及联动吊销仍未完成验收；联动吊销接口当前返回 501。长期历史数据采集不在本阶段实现。

## 新增交互安装

前端指令、节点输入令牌与模板准备见 [HTTPS 安装器](bootstrap.md)，挂载与维护策略见 [OverlayFS 方案](overlayroot.md)。
