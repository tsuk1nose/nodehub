# 只读根目录与临时可写目录

[English](../../readonly-runtime.md) | 简体中文

根 OverlayFS 保持只读；仅配置中的目录覆盖独立的内存写入层。已有文件仍可读取，修改和删除仅影响内存，系统重启后恢复基线。`/etc` 默认只读，不再启用整个根目录可写的服务。

## 配置

HTTPS 初始化安装 `/usr/local/lib/nodehub/readonly-runtime`。安装时立即在持久根目录上创建 `/etc/nodehub/readonly-runtime.toml`，可在制镜像前编辑；收尾读取该配置并安装启动挂载服务，默认包含 NetworkManager（16 MiB）和 `/usr/local/mesh_services/meshagent`（64 MiB，允许执行），均允许目录不存在时跳过。缺少 `secret_key` 不影响安装或启动。

例如，在已创建对应目录后，配置可改为：

```toml
# NetworkManager runtime state.
[[temporary_directories]]
path = "/var/lib/NetworkManager"
memory_mb = 16
optional = true

# MeshAgent executable, settings, and identity database.
[[temporary_directories]]
path = "/usr/local/mesh_services/meshagent"
memory_mb = 64
optional = true
allow_exec = true

# Optional application state (create this directory before imaging).
[[temporary_directories]]
path = "/etc/animactrl"
memory_mb = 4
```

示例中的 `/var/lib/animactrl` 需与实际服务的 StateDirectory 一致，并提前按服务要求设置所有者和权限。`/etc/animactrl` 可写只开放该子目录，其他 `/etc` 路径仍只读。目录内全部文件均可临时修改；不支持配置单个文件。

`path` 必须是已存在、无符号链接的绝对目录路径；目录之间不能重复或嵌套。`memory_mb` 是该目录内存文件系统容量上限（1–1024 MiB），不是启动即预留的内存。`optional: true` 允许不存在的目录被跳过。不能覆盖 `/`、`/run`、`/dev`、`/proc`、`/sys`、`/data`、`/media` 及其子目录。不要把整个 `/var/lib` 列入配置，以免使所有服务身份更新都变成临时数据。

## 安装与修改

在制作模板前或 `overlayroot=disabled` 的持久维护启动中：创建所需目录、编辑配置，然后运行：

```sh
python3 /usr/local/lib/nodehub/readonly-runtime
systemctl daemon-reload
```

安装会保留管理员自定义配置；只读取 TOML，不兼容或迁移旧 JSON。若已经安装新路径 MeshAgent，自定义列表必须包含其目录并允许执行，否则拒绝收尾。安装并迁移已知的旧 NodeHub 根目录可写服务和 NetworkManager 专用挂载服务。它不在线切换现有挂载，也不会重启网络；重启进入 OverlayFS 后应用。现有节点需要先安装更新后的 helper，不能只在临时层修改配置。

`nodehub-temporary-writes.service` 在 systemd 根目录重挂载之后、本地文件系统准备和普通服务之前运行。它验证磁盘底层只读、根上层位于 tmpfs，并确保合并根目录只读；之后按配置挂载局部 overlay。配置无效或挂载失败会阻止 `local-fs.target` 正常启动，应准备本地恢复控制台。维护启动不修改挂载。

每个目录保留基线所有者、权限与文件，写入层位于 `/run/nodehub-temporary-writes`，始终具有 nosuid/nodev 限制；默认 noexec，只有 `allow_exec: true` 的目录允许执行，tmpfs 和 overlay 两层均使用该选项。journald 使用 `/run` 中的易失日志，容量策略为 RuntimeMaxUse=64M、RuntimeKeepFree=32M。MeshAgent 的首次身份保存在各节点的根目录基线。旧 `/data` 安装不会自动迁移，需新模板测试；其他服务的既有身份路径不迁移。

刷新 token、设备身份等如果必须跨重启保留，应放到 `/data`；不要将其当作临时写入数据。

## 验收

检查根 `/` 为 `ro`、配置目录为 `rw`，并用独立测试文件验证重启后临时修改丢失、基线文件恢复。验证热点、业务服务和管理连接正常；测试缺少目录、挂载失败及内存限额场景。测试不会代替真实节点重启验收。
