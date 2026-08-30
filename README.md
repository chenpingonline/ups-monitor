<p align="center">
  <img src="package/ICON_256.PNG" alt="UPS Monitor" width="112" />
</p>

<h1 align="center">UPS Monitor for fnOS</h1>

<p align="center">
  面向飞牛 fnOS 的原生 UPS 监控应用，通过本机或远程 NUT 服务查看设备状态、历史趋势和事件告警。
</p>

<p align="center">
  <a href="https://github.com/chenpingonline/ups-monitor/releases/latest"><img src="https://img.shields.io/github/v/release/chenpingonline/ups-monitor?label=%E6%9C%80%E6%96%B0%E7%89%88%E6%9C%AC&color=blue" alt="Latest Release" /></a>
  <a href="https://github.com/chenpingonline/ups-monitor/releases"><img src="https://img.shields.io/github/downloads/chenpingonline/ups-monitor/total?label=%E4%B8%8B%E8%BD%BD" alt="Downloads" /></a>
  <img src="https://img.shields.io/badge/fnOS-x86__64%20%7C%20ARM64-26a269" alt="fnOS x86_64 and ARM64" />
</p>

> [!IMPORTANT]
> 这是社区维护的非官方第三方应用。应用只读取 NUT 服务提供的 UPS 数据，不会安装 NUT，也不会修改 fnOS 或 UPS 的系统配置。

## 功能

- **实时状态**：集中显示供电状态、电池电量、预计续航、负载、功率和电压。
- **历史趋势**：查看 1 小时、24 小时、7 天和 30 天的电量、负载、电压、续航及功率变化。
- **事件记录**：按时间筛选并分页查看告警，最新事件优先显示。
- **设备信息**：展示型号、序列号、固件、NUT 驱动、电池信息和原始变量。
- **多 UPS 监控**：支持配置多个本机或远程 NUT 目标。
- **告警与通知**：支持 Webhook、ntfy、Gotify、Telegram、企业微信和钉钉。
- **设备能力检测**：通过 NUT 自动识别设备支持的命令和可调参数，仅展示实际可用的自检操作。
- **运行集成**：支持 MQTT 状态发布、Prometheus 指标、历史 CSV 导出和 API Token。
- **安全关机策略**：默认关闭且处于演练模式，真实关机需要同时通过多重安全门禁。
- **fnOS 风格界面**：提供状态、趋势、事件、设备和设置五个页面，支持浅色、暗色及跟随系统主题。

## 使用要求

- 飞牛 fnOS NAS，处理器架构为 x86_64 或 ARM64。
- NAS 本机或局域网内已有可访问的 [Network UPS Tools（NUT）](https://networkupstools.org/) 服务。
- NUT 默认端口为 `3493`；只读监控通常不需要控制账号。
- 执行 UPS 自检等控制操作时，需要在 NUT 中配置具备相应权限的用户名和密码。

## 下载

请只从本仓库的 [GitHub Releases](https://github.com/chenpingonline/ups-monitor/releases/latest) 下载正式安装包。

| fnOS 设备架构 | Release 文件 |
| --- | --- |
| Intel / AMD x86_64 | `fnos-ups-monitor_<版本>_x86.fpk` |
| ARM64 | `fnos-ups-monitor_<版本>_arm.fpk` |

每个安装包都附带同名 `.sha256` 校验文件。架构不匹配的 FPK 无法正常安装或运行。

## 安装

1. 打开 [最新 Release](https://github.com/chenpingonline/ups-monitor/releases/latest)。
2. 下载与 NAS 处理器架构匹配的 `.fpk` 文件。
3. 进入 fnOS「应用中心」，选择「手动安装」并上传安装包。
4. 安装完成后，从 fnOS 桌面打开 **UPS Monitor**。
5. 进入「设置 → 基础连接与告警」，填写 NUT 主机、端口和 UPS 名称，然后点击「测试连接」。

首次使用可以保留以下默认值：

| 配置项 | 默认值 | 说明 |
| --- | --- | --- |
| NUT 主机 | `127.0.0.1` | NUT 运行在其他设备时改为对应局域网地址 |
| NUT 端口 | `3493` | NUT `upsd` 的监听端口 |
| UPS 名称 | 留空 | 自动选择 NUT 返回的第一台 UPS |
| 轮询间隔 | `10` 秒 | 实时状态刷新频率 |
| 历史保留 | `30` 天 | 可在设置中调整 |
| 低电量阈值 | `25%` | 用于基础低电量提醒 |

## 安全说明

- 建议仅在可信局域网中使用，不要将应用或 NUT 端口直接暴露到公网。
- NUT 控制账号、通知 Token 和 API Token 都属于敏感信息，请勿提交到公开仓库或问题反馈中。
- 实际系统关机默认不可执行。启用前请先长期使用演练模式，并阅读[高级配置与关机安全门禁](docs/CONFIG.md)。
- UPS 型号和固件的上报字段可能不同，页面会以设备实际提供的数据和能力为准。

## 从源码构建

项目使用 Go 静态编译后端，并分别生成 x86_64 与 ARM64 安装包。在 macOS 或 Linux 上执行：

```bash
./build-release.sh all
```

也可以只构建一个架构：

```bash
./build-release.sh x86
./build-release.sh arm
```

构建产物位于 `dist/`。每次运行打包脚本都会先将 `VERSION` 的补丁版本递增一次；`all` 会同时构建两个架构，但只递增一次版本号。

如需使用飞牛官方 `fnpack 1.2.3`，请在 Linux amd64 环境执行：

```bash
./audit.sh
./build-with-fnpack.sh x86
./build-with-fnpack.sh arm
```

## 运行检查

安装后的应用日志：

```bash
tail -f /var/apps/fnos-ups-monitor/var/ups-monitor.log
```

检查运行状态：

```bash
/var/apps/fnos-ups-monitor/cmd/main status
echo $?
```

返回 `0` 表示正在运行，返回 `3` 表示未运行。

## 文档与反馈

- [高级配置、通知、MQTT 与安全关机](docs/CONFIG.md)
- [下载最新版本](https://github.com/chenpingonline/ups-monitor/releases/latest)
- [提交问题](https://github.com/chenpingonline/ups-monitor/issues)
