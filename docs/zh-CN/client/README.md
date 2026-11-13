# Debian 13 初始化工具（分阶段初版）

[English](../../../client/README.md) | 简体中文

当前提供可恢复的登记和 Tailscale 注册流程，MeshAgent 安装提供经实机验证的二进制 hook，也可替换为自己的本地脚本。已给出 OverlayFS 与 Btrfs 挂载建议及示例 hook，重启和故障验收尚未完成，不自动修改分区或启动配置。

## 制作模板

1. 安装 Python 3、CA 证书、Tailscale、systemd；配置网络与系统时间。
2. 按 Debian 模板封装规范处理 machine-id、SSH 主机密钥；不得留下 Tailscale / MeshAgent 已注册身份。这一步尚需实机验证，不应直接复制生产设备状态制作镜像。
3. 以 root 执行 `client/install.sh`，仅启用服务，不启动。
4. 将激活文件放在 `/etc/nodehub/activation.json`，内容参照示例，文件权限 600、root 拥有。中心中创建的无限令牌可供同一批镜像复用。
5. 放置 `/usr/local/lib/nodehub/mesh-install`：root 拥有、其他用户不可写、可执行的安装脚本。通过 `NODEHUB_SERIAL` 环境变量取得序列号，使用实际 MeshCentral 生成的 Debian 安装方式。脚本必须可重试，安装后保留默认根目录服务状态；不要在输出或参数中暴露管理账号密码。
6. 确认 `/var/lib/nodehub-init` 内没有已生成的状态及秘密；关机克隆，避免在模板执行首次初始化。

## 首次启动

服务等待网络后执行，失败每 60 秒重试。本机在调用中心前持久保存独立请求身份和凭据；中心验证该凭据可恢复后，本机删除激活文件。它不会在中心全局撤销批量令牌。

工具设置 hostname 和 `/etc/hosts`，通过中心申请 Headscale key，Tailscale 使用临时密钥文件加入网络，密钥文件随后删除。服务身份仍在默认根目录位置。MeshAgent hook 成功后进度持久化，后续不会重复调用该 hook。

管理员在中心节点详情中核实并绑定 MeshCentral Node ID。若绑定失败，检查外部 ID、设备组、名称与主机名；工具不会单凭同名自动选取设备。

## 最终化与清理

初版将这一阶段保留为显式操作。先准备一个 root 拥有、可执行、不可被其他用户写入的 OverlayFS hook。它必须：

- 在根目录仍持久可写时运行，可重复执行。
- 仅准备**下次启动**的 OverlayFS，不在内部重启或立即切到易失上层。
- 保留默认位置里的服务凭据、hostname、machine-id 及 SSH 身份。
- `/data` 采用 CoW，挂载失败不阻止系统开机，不让服务凭据依赖 `/data`。
- 明确后续根目录维护与运行期凭据更新策略。

在已有远程终端或本机终端执行（不要将最终化放入首次启动服务）：

```sh
/usr/local/sbin/nodehub-init --finalize --overlay-hook /usr/local/lib/nodehub/enable-overlay
```

工具要求两个上游均已绑定，标记等待重启，先停用并删除首次启动服务及可选 `agent.conf` / `bootstrap.conf` 覆盖文件、删除激活文件与安装的初始化程序，落盘后执行 hook 准备下次 OverlayFS，再重启。这样清理中断不会提前启用 OverlayFS。保留 `/var/lib/nodehub-init/state.json` 里的本机独立凭据及恢复进度，不保留共享激活令牌。

清理途中断电时，通过 MeshCentral 或外部恢复介质重新提供同版工具，使用既有状态重试，不能清空状态重新登记。生产无人值守最终化的断电恢复还需 Debian 实机测试，当前不声称已验收。

重启后通过现有远程通道检查 hostname、两套服务身份与连接、根目录挂载及 `/data` 降级行为。可临时从受信工作站重新提供同版工具，执行 `--confirm-after-reboot`，它验证根目录类型为 overlay 并向中心标记完成；之后移除这份临时工具。工具本身的检查不能替代连接与存储验收。

## 存储边界

初始化工具不创建或挂载 `/data`，不将 Tailscale / MeshAgent 凭据迁入其中，不依赖其挂载成功。所有首次注册结果必须在启用 OverlayFS 前成为根目录持久基线。

## 可选原生终端 Agent

可预装独立 Agent，但不得在模板生成身份或运行登记。克隆设备已取得序列号后，可为初始化工具增加 `--agent-config /etc/nodehub-agent/config.json`，使用本机恢复凭据登记独立公钥并启用 Agent。中心需先启用 `terminal.enabled`。最终化时删除的仍是初始化入口，不删除常驻 Agent 服务和设备私钥。详见 [原生终端部署](../docs/native-terminal.md)。

## 内置 MeshAgent 二进制安装 hook

将 `mesh-install.py` 安装为 `/usr/local/lib/nodehub/mesh-install`（root 拥有、权限 700），将 `mesh-install.example.json` 配置为 `/etc/nodehub/mesh-install.json`（权限 600）。配置中指定原始未注册的 Linux amd64 二进制、从目标设备组下载的 `.msh` 设置文件，以及验证过的二进制 SHA256；这些文件必须由 root 拥有且其他用户不可写。

实测提供的 Linux 二进制安装参数为 `-install --copy-msh=1 --agentName=<序列号>`，`-fullinstall` 可能返回成功但不安装。hook 在 hostname 更新后运行，在临时目录合并 `agentName` 与 `StartupType=1`，安装后检查 `meshagent.service` 正在运行，并保存可重试记录。已有未被本工具记录的 MeshAgent 服务时拒绝接管。

显示名称使用序列号；MeshCentral 内部 Node ID 仍由 Agent 身份生成。安装前修改系统 hostname 会直接影响新启动的 Agent，无需重新登录现有终端。不要将安装后的数据库、证书或设备身份带入克隆模板。


## 自动配置原生 SSH 终端

模板预装并启用 Debian OpenSSH server 与 NodeHub Agent，保持 Agent 无身份、未登记。中心启用 `terminal.enabled` 后，初始化工具使用 `--agent-config /etc/nodehub-agent/config.json`，自动生成缺失的 SSH 主机密钥、上报主机公钥、取得中心为本节点生成的授权公钥、保留原 root 授权并加入本节点公钥，并在落盘确认后启动 Agent。模板的初始化服务 ExecStart 只需统一加一次该参数；中心无需配置逐节点 SSH 参数。

不创建新的维护账号，不覆盖 root 的其他授权；已有主机公钥、登录公钥和节点身份不允许重试时替换。重复执行同一初始化命令恢复进度，不能删除状态重新生成。终端直接以 root 执行，无需 sudo；根目录默认位置持久化全部授权与状态，须在最终化及 OverlayFS 前验证。详见 [原生终端](../docs/native-terminal.md)。

## 模板启用自动 root 终端

完成 Agent 安装、填写 `/etc/nodehub-agent/config.json`，中心开启 `terminal.enabled` 后，在模板上以 root 安装一次服务覆盖文件：

```sh
install -D -m 644 client/nodehub-init-agent.conf.example \
  /etc/systemd/system/nodehub-init.service.d/agent.conf
systemctl daemon-reload
```

命令在本仓库根目录执行。此时不要启动初始化或登记模板：确认没有已生成的节点 / Agent 身份，关机克隆。覆盖文件会让所有克隆节点首次启动时带 `--agent-config` 自动安装 root 授权。仍需按前文准备激活文件和 MeshAgent hook。

不启用原生终端时不用安装该覆盖文件。升级旧部署时先更新中心与初始化程序，再用原状态重试，不删除节点身份。覆盖文件含有可选参数；更改首次启动服务后需检查实际 ExecStart，并在最终化前验证浏览器终端返回 `root`。

## HTTPS 安装与 OverlayFS

可使用 [交互安装器](../docs/bootstrap.md) 在节点隐藏输入令牌，也可通过 `--prepare-image` 输入并保存批量令牌、安装模板工具而不登记。克隆机首次启动自动关联 MeshCentral、清理初始化文件、启用根目录 OverlayFS 并重启。挂载与维护策略见 [OverlayFS 方案](../docs/overlayroot.md)。
