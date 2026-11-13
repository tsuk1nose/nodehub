# Debian 13 根目录 OverlayFS 方案

[English](../../overlayroot.md) | 简体中文

日期：2026-11-13。本方案已通过设备只读检查，但**未启用 OverlayFS、未修改远程 fstab 或启动配置、未重启验收**。

## 当前状态与推荐配置

检查到的 N2840 模板约有 4 GB RAM，EFI 独立分区、约 15 GB ext4 根分区、约 44 GB Btrfs `/data`。`/data` 可读写且目录没有 NoCoW 标记，默认已启用 CoW，不必重建文件系统或使用 `chattr +C`。

已安装 `overlayroot` 0.18.debian14 和 initramfs-tools；当前 initramfs 已包含 OverlayFS 模块与 overlayroot 启动钩子。OverlayFS 尚未启用，NodeHub/Agent 尚无已登记身份。`/data` 当前 fstab 只有 defaults，缺少 nofail，尚不满足挂载失败不影响开机的要求。

建议 ext4 根目录作为只读底层、RAM 作为临时写入层，重启丢弃根目录运行期变更；`/data` 保持独立可写 Btrfs。在持久根目录创建 `/etc/overlayroot.local.conf`：

```sh
overlayroot="tmpfs:recurse=0,swap=0"
overlayroot_cfgdisk="disabled"
```

`recurse=0` 很关键，避免独立 `/data` 也变成只读。不要把根目录 fstab 的类型直接改成 overlay。安装包实际启动脚本会读取真实根目录中的配置；当前 initramfs 已包含钩子，核心启用动作是持久写入上述配置并在验收后重启。初次安装或修改相关包后仍应 `update-initramfs -u -k all`，并检查实际启动的内核。

tmpfs 默认上限约半数 RAM，非预先占满；限制日志和缓存，并测量业务内存。例如制作模板时可配置 journald `Storage=volatile`、`RuntimeMaxUse=64M`。独立 EFI 等挂载不受根目录 overlay 保护。

## 安装系统或制作模板时可完成

预装 overlayroot、initramfs-tools、btrfs-progs、python3、curl、ca-certificates、openssh-server；Tailscale 按你的版本和软件源预装。保留已有分区，不格式化。

`/etc/fstab` 的 `/data` 条目保留本机实际 UUID，推荐：

```fstab
UUID=REPLACE_WITH_DATA_UUID /data btrfs defaults,nofail,x-systemd.device-timeout=5s,x-systemd.mount-timeout=15s 0 0
```

用 `findmnt --verify --verbose` 检查，再执行 `systemctl daemon-reload`。超时参数应匹配硬件；它们限制等待，不负责修复文件系统。依赖 `/data` 的业务服务可加 `RequiresMountsFor=/data`，但 SSH、Tailscale、MeshAgent、NodeHub Agent 不应依赖它。业务程序不能在未挂载时向同名空目录落盘。不要未经评估加入 automount，它会改变首次访问时的阻塞行为。

封装好 machine-id 与 SSH 主机身份；不要把任何已登记的节点状态和密钥克隆到其他机器。可用 curl 安装器的 `--prepare-image` 只安装工具、不登记、不启动；无人值守启动可另行注入共享激活文件，或节点上线时交互输入令牌。

## 克隆后启用与验收

1. 持久可写根目录下完成交互初始化。
2. 验证序列号、hostname、两套上游和 root 终端，人工确认 MeshCentral 关联。
3. 检查 `/data` 策略及独立远程恢复通道。
4. 审核并安装 `client/enable-overlayroot.example.sh` 为 root 拥有的可执行 hook。开启安装资源后，可从 `/bootstrap/files/client/enable-overlayroot.example.sh` 下载；HTTPS 安装器现会自动安装并在收尾时运行它。
5. 手动安装路径可执行 `nodehub-init --finalize --overlay-hook /usr/local/lib/nodehub/enable-overlayroot`。工具先清理初始化入口和激活秘密，在准备配置后清理全部初始化工具、状态和共享令牌，再重启；hook 本身不重启。

curl 的 --prepare-image 路径会保存批量令牌；克隆机首次启动自动完成关联、收尾、启用 overlay 并重启，无需执行上述手动步骤。示例 hook 检查持久根目录、initramfs 钩子和 `/data` 的 Btrfs/nofail 配置；遇到不同的已有 overlay 配置会拒绝覆盖。

重启后确认根目录为 overlay，`/data` 为可写 Btrfs。再创建根目录临时文件和 `/data` 持久文件，第二次重启确认前者消失、后者保留；验证身份和 hostname 不变。在有恢复手段的测试环境验证 `/data` 缺失仍能开机，之后通过既有的 `--confirm-after-reboot` 流程确认完成。

## 维护和必须明确的长期边界

GRUB 单次启动参数 `overlayroot=disabled` 可覆盖配置，进入持久维护模式；在此更新系统、Agent、配置和身份基线，再恢复普通启动。系统自带 `overlayroot-chroot` 可用于受控底层维护，但不是服务状态自动持久化方案，不要把活动写入层整体复制回底层。

RAM overlay 只保留初始化时写入底层的身份，之后的 Tailscale 状态/密钥续期、MeshAgent 自更新等写入会在重启时回滚。当前方案遵守凭据留在默认根目录位置、不放 `/data` 的要求，但不能因此承诺长期无人值守身份更新已解决。

首轮测试可采用持久基线加维护启动；生产前必须对长期更新策略另行验收。如果需要可持续写入且不能使用 `/data`，可考虑独立系统状态分区并绑定到默认服务目录，但这需要明确分区方案，本次未实施。如果准备重新安装系统，可先讨论预留约 2–4 GB 的独立系统状态分区，再分配其余 Btrfs `/data`；通过 bind 保持服务默认路径，并分别验收挂载缺失和迁移。不要据此直接缩容或重新分区当前设备。

参考：[Debian overlayroot-chroot](https://manpages.debian.org/trixie/overlayroot/overlayroot-chroot.8.en.html)、[systemd 挂载语义](https://manpages.debian.org/trixie/systemd/systemd.mount.5.en.html)。设备内安装包自带配置注释和脚本是本版本的直接依据。

## 当前存储方案

仅将 MeshAgent 整个安装目录放到节点 `/data/nodehub/meshagent`。初始化要求 `/data` 已挂载为可写 Btrfs。服务通过 `RequiresMountsFor=/data`、`BindsTo=data.mount`、挂载点条件及启动前检查依赖该挂载；未挂载时不启动 MeshAgent、不生成备用身份，也不阻止系统开机。保留 `/data` 的 nofail 和有限等待时间。

Tailscale 和原生 Agent 身份保持原根目录路径。初始化不再启用顶层 OverlayFS 可写修复服务，此前的可写 workaround 已被新安装方案取代；不会自动迁移旧测试机。MeshAgent 目录权限为 0700、设置为 0600，`/data/nodehub` 须为 root 拥有且其他用户不可写。新方案需新机验证跨重启身份、连接与缺少 /data 时开机行为；MeshAgent 测试不能替代只读根目录下 Tailscale 续期及重新登录验证。
