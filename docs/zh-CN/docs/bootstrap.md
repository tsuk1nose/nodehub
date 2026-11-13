# HTTPS 节点安装器

[English](../../bootstrap.md) | 简体中文

中心提供初始化工具、服务文件、可选 Agent 和校验清单；前端“初始化授权”页面按当前浏览器 Origin 生成安装命令，不把实际域名或令牌写入仓库。

## 服务端准备

现有配置增加下列字段，然后重新构建并重建中心；Dockerfile 会打包脚本和原生 Agent 二进制：

```json
"bootstrap": {
  "enabled": true,
  "assets_dir": "/app/bootstrap"
}
```

自动 MeshAgent 安装目前面向 Debian 13 amd64，符合 N2840。其他架构继续使用手动受信 hook；有原生 Agent 二进制不代表已支持相应 MeshAgent 的自动安装。

镜像预装 Python 3、curl、CA 证书、Tailscale、systemd、OpenSSH server，并配置网络和时间。安装器不分区、格式化、安装未知来源的 Tailscale 脚本。需预装 overlayroot 并检查当前 initramfs。登记成功后会自动启用根目录 OverlayFS 并重启。

入口允许 `/install.sh`、`/bootstrap/manifest`、`/bootstrap/files/…` 直接访问，不经过交互登录。这些接口只分发白名单代码，不提供实际配置、节点状态或私钥。已有机器接口继续使用各自认证；管理接口仍需保护。

## 节点交互安装

在前端创建初始化令牌并保存一次性显示的原文，复制命令后在节点以 root 执行：

```sh
curl --fail --silent --show-error --proto '=https' --tlsv1.2 \
  'https://hub.example.com/install.sh' | bash -s -- --server 'https://hub.example.com'
```

替换占位地址；前端自动使用当前实际地址。节点从控制终端隐藏输入令牌，不放命令参数、环境变量、URL 或 shell 历史。没有可安全隐藏输入的终端时拒绝继续，不回退到明文输入。

安装器检查前置条件、串行化安装，通过 HTTPS 获取资源并校验清单大小/SHA256，安装初始化服务和可选 Agent，使用 root-only 激活文件运行初始化。已有同中心状态会复用，中心地址不一致会拒绝覆盖。已经取得序列号的重试不需要重新提供共享令牌。运行交互流程期间会停止已安装的首次启动服务，避免并发执行。

确认独立节点恢复认证可用后删除本机激活文件；失败保留恢复状态。节点登记后才能领取本组 MeshCentral 下载元数据，中心管理凭据不下发。MeshAgent 从公开 HTTPS 入口下载，限制大小、拒绝重定向和非 ELF 二进制，并交给已有受信 hook 安装；遇到无关的已有 Agent 会拒绝接管。

下载目录为 root-only `/opt/nodehub/bootstrap`，本地配置保存下载时的 SHA256。该摘要用于与已认证 HTTPS 下载保持一致，不是独立的厂家签名。MeshCentral 公网下载入口也不能被交互登录拦截；内部连接的 `tls_skip_verify` 不会影响节点下载验证，私有 CA 需提前预置。

节点认证后，中心自动匹配指定设备组中序列号相符的唯一设备，不需要人工确认 Node ID。未出现则自动重试，重复或关联冲突则拒绝收尾。该方式信任 MeshCentral 设备组和命名，不是 MeshAgent 证书的密码学证明。全部配置完成后由独立收尾服务准备 OverlayFS、给 /data 增加 nofail 和等待超时、清理初始化工具和文件，然后重启；见 [OverlayFS 方案](overlayroot.md)。

## 制作未登记模板

增加 `--prepare-image` 会隐藏输入并保存批量令牌，只安装工具、不登记、不启动：

```sh
curl --fail --silent --show-error --proto '=https' --tlsv1.2 \
  'https://hub.example.com/install.sh' | bash -s -- \
  --server 'https://hub.example.com' --prepare-image
```

该选项启用首次启动服务但不启动，并拒绝已有 NodeHub/Agent 身份。它不会替你清理已登记的 Tailscale、MeshAgent、machine-id 或 SSH 身份，不能用来直接克隆生产节点。

批量令牌保存在 root 拥有、mode 600 的 `/etc/nodehub/activation.json`。选择足够次数和期限的可重复使用令牌；支持无限期无限次。随后关机制作镜像，不在模板上启动登记。克隆机首次启动自动取得独立序列号和凭据，认证恢复成功后删除本机批量令牌；暂时失败可恢复重试。源镜像中的令牌不会因某台克隆机删除而消失，需更新镜像或在中心吊销。成功后自动删除初始化服务及覆盖配置、初始化程序、Mesh 安装辅助程序/设置/下载缓存、初始化状态和凭据；保留各常驻服务及其默认目录下身份。中心保持 awaiting_overlay，重启效果仍需拉通验收。

## 验证边界

测试覆盖代码白名单、SHA256 清单、关闭或缺少资源、符号链接拒绝、节点认证、组配置非公开、凭据隔离、HTTPS 参数和禁止不安全输入回退。前端构建及 Python 编译通过。

此次检查没有登记或修改干净模板。新的真实在线下载、完整克隆初始化、OverlayFS 重启及 `/data` 故障还需在有真实令牌的指定节点验收；过去 root 终端实测不能替代这些新流程的测试。

## MeshAgent 持久安装修正

首次真实克隆测试已自动清理并重启，根目录 OverlayFS、可写 Btrfs `/data`、Tailscale 和原生 Agent 均生效。但 MeshAgent 生成的服务指向安装临时目录，该目录删除后启动失败，身份数据库也随之丢失。因此曾经注册成功、瞬间 active 不足以证明安装成功。

安装现改为永久目录 `/data/nodehub/meshagent`，保留程序、设置和数据库；安装后重启并检查服务路径与工作目录。所有权标记保留在 `/var/lib/nodehub-mesh/install.json`。收尾之前检查持久程序/设置/数据库、启动路径及服务运行状态，失败不删除初始化工具。这些保护已通过单元测试，修正后的完整设备安装仍需再次实测。

受影响节点应先查找原身份数据库。数据库确实丢失时，重新安装会生成不同 MeshCentral 身份，需显式恢复备份或重新关联，不应静默替换中心绑定。OverlayFS 模式下仅修改当前根目录会在重启后丢失，持久修复需受控维护启动。

## 当前存储方案

仅将 MeshAgent 整个安装目录放到节点 `/data/nodehub/meshagent`。初始化要求 `/data` 已挂载为可写 Btrfs。服务通过 `RequiresMountsFor=/data`、`BindsTo=data.mount`、挂载点条件及启动前检查依赖该挂载；未挂载时不启动 MeshAgent、不生成备用身份，也不阻止系统开机。保留 `/data` 的 nofail 和有限等待时间。

Tailscale 和原生 Agent 身份保持原根目录路径。初始化不再启用顶层 OverlayFS 可写修复服务，此前的可写 workaround 已被新安装方案取代；不会自动迁移旧测试机。MeshAgent 目录权限为 0700、设置为 0600，`/data/nodehub` 须为 root 拥有且其他用户不可写。新方案需新机验证跨重启身份、连接与缺少 /data 时开机行为；MeshAgent 测试不能替代只读根目录下 Tailscale 续期及重新登录验证。
