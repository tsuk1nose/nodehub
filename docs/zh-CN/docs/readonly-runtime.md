# 只读节点的运行目录

[English](../../readonly-runtime.md) | 简体中文

HTTPS 初始化会安装 `/usr/local/lib/nodehub/readonly-runtime`，在启用根目录 OverlayFS、清理初始化工具前为下次启动配置运行目录。它是保留的启动组件，不含初始化令牌。

## NetworkManager

存在 `/var/lib/NetworkManager` 时，要求它是真实目录。基线已有的 `secret_key` 会保留，但缺少该文件不阻止收尾或启动，由 NetworkManager 自行管理。仅在内存层生成的网络身份可能在系统重启后改变，本节点方案接受这一行为。machine-id 仍遵循镜像封存流程。

OverlayFS 启动时，`nodehub-network-state.service` 把原状态目录只读绑定到 `/run/nodehub-network-state/lower`，用独立、上限 16 MiB 的 tmpfs 提供 upper/work，再将局部 overlay 挂到 `/var/lib/NetworkManager`。底层文件仍可读取，热点 DHCP 租约等运行写入在重启后清空。需要长期保存的状态修改在维护模式完成；已有 Wi-Fi profile 持久化方案保持不变，本工具不迁移连接配置。

NetworkManager 必须等待挂载服务成功；目录无效或挂载失败时不启动，避免继续使用不可写目录。服务重启保留本次开机的内存数据，系统重启才清空。非 overlay 的维护启动不挂内存层，修改落到真实目录。没有该目录的系统只安装日志策略，之后安装 NetworkManager 时需在维护模式重新运行配置。

## 日志和范围

journald 新增配置 `Storage=volatile`、`RuntimeMaxUse=64M`、`RuntimeKeepFree=32M`，写到 `/run/log/journal`，重启后清空。这是日志保留限额，不是整个 `/run` 的硬限制，也不约束其他程序的日志。相同路径已有不同配置会报错，不覆盖管理员设置；策略在重启后生效，安装时不重启网络或日志服务。

`/run`、`/tmp` 保留系统原挂载及限额；不新增 `/var/tmp`、整个 `/var/log`、`/etc` 或 `/var/lib` 的可写挂载。MeshAgent 继续位于 `/data/nodehub/meshagent`，Tailscale 和原生 Agent 路径不变，不打开根目录整体可写。本次不解决 systemd 根目录重挂载、apt、GRUB 启动记录和 wtmpdb 的独立适配问题。

## 新机验收

重建中心镜像，让安装清单包含 `client/readonly-runtime.py`，再按正常流程准备新模板。手工安装时，也要先将此文件安装至 `/usr/local/lib/nodehub/readonly-runtime`，root 拥有、0700 权限。

重启后检查：

```sh
systemctl status nodehub-network-state NetworkManager --no-pager
findmnt --target /var/lib/NetworkManager
findmnt --target /run/nodehub-network-state
findmnt --target /
journalctl --disk-usage
```

确认根目录只读、NetworkManager 局部目录可写、tmpfs 上限 16 MiB，以及热点客户端能获取 DHCP 租约。验证热点启停、客户端重连、NetworkManager 重启和系统重启，检查 machine-id 稳定、Wi-Fi 配置保留。仅在内存层生成的 NetworkManager 网络标识允许重启后变化，需验证联网与管理连接恢复。挂载失败测试应准备本地控制台，预期 NetworkManager 不启动。内存层写满会导致写入失败，不代表热点有自动恢复能力。

单元测试覆盖维护模式跳过、缺少身份时拒绝、挂载顺序/限额、服务依赖及配置保护；真实热点重启验收仍待新机执行。截图中的热点业务程序不在本仓库，其“失败却显示运行”和重试恢复逻辑需在所属程序另行修改。
