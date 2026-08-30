# fnOS UPS Monitor 1.2.1（x86 / ARM64 独立包）

这是面向 fnOS x86_64 与 ARM64 NAS 的轻量 UPS 监控 FPK 项目。两种架构使用同一套源码，分别生成安装包。

## 1.2.1

- 首页重构为状态驾驶舱，设备状态、电量、续航、负载、估算功率和电压一屏可见。
- 趋势与最近事件形成主工作区，设备详情、电池健康、运行报告和设置收拢为底部快捷入口。
- 高级配置、自检和原始 NUT 数据移入弹窗，避免监控首页被配置 JSON 占满。
- 加入施耐德 APC UPS 产品图，并适配桌面、平板和窄屏布局。

## 1.1.1

- 概览和设备详情直接显示设备上报的低续航阈值。
- 设备详情增加蜂鸣器状态、NUT 驱动版本、驱动数据版本和内部驱动版本。
- 历史趋势支持切换电量/负载、输入/输出电压、电池电压、预计续航和功率。
- 不同单位使用独立自适应纵轴，保留 1 小时、24 小时、7 天和 30 天范围。

## 1.1.0

- 自动识别施耐德 APC Back-UPS BK650M2-CH，展示 650VA、390W 和铅酸电池资料。
- 设备未直接报告功率时，按额定功率与负载率显示明确标注的估算功率。
- 显示输入电压、允许切换范围和灵敏度，直接判断当前市电质量。
- 按相近负载、充足电量的历史续航判断电池老化趋势，不依赖不可靠的生产日期。
- 通过 NUT `LIST CMD` 和 `LIST RW` 自动发现命令及可调参数，只显示设备实际支持的自检按钮。
- 针对 BK650M2-CH 过滤 `OL DISCHRG` 和短暂 `LB/RB` 误报，计划自检执行前也会验证设备能力。

## 1.0.1

- 设备详情改为状态摘要以及设备、电池、输入、输出分组信息卡，原始 NUT 数据默认折叠。
- 30 天报告、电池健康和关机计划改为中文可视化摘要。
- `ERR USERNAME-REQUIRED` 等 NUT 错误显示中文原因和配置建议。

## 1.0.0

- 多 NUT 服务器、多 UPS 目标统一监控，旧版单目标配置自动兼容。
- 按设备查看实时数据、完整 NUT 原始变量、1 小时至 30 天趋势和事件。
- 支持电量、续航、负载、电压、频率、功率、温度规则告警，包含持续时间、恢复迟滞和冷却提醒。
- Webhook、ntfy、Gotify、Telegram、企业微信、钉钉通知使用磁盘持久化队列和指数退避重试。
- 电池健康评分、UPS 快速/深度自检白名单和计划自检。
- Prometheus `/metrics`、MQTT 状态发布、API Token。
- 停电次数/时长、能耗估算、JSON/CSV 导出。
- 默认关闭且默认为演练模式的关机策略；实际关机需要配置确认短语和进程环境门禁，并在倒计时结束前再次检查市电状态。

## 0.2.0

- 新增 x86 与 ARM64 独立构建，避免在一个 FPK 中混装不同架构二进制。
- Go 后端按配置、NUT、存储、监控与 HTTP 职责拆分源码文件，并补充自动化测试。
- 新增 `/api/health` 与 `/api/readiness`，暴露版本、最近轮询、最近成功采集及存储/Webhook 错误。
- 配置损坏时不再用默认配置静默覆盖原文件。
- 历史、事件和 Webhook 错误不再静默忽略，测试 Webhook 会返回真实投递结果。
- 历史查询范围扩展至一年，为后续 7 天、30 天图表做准备。


## 0.1.4 UI 更新

- 使用 fnOS 风格的“画布 + 浮动窗口”布局：亮色画布为 `#F3F3F3`，暗色画布为 `#0C0C0D`。
- 亮色与暗色主题使用完全相同的 DOM、卡片位置、尺寸与图标，只改变颜色、边框和阴影。
- 主题默认跟随系统，可在跟随系统、亮色、暗色之间循环切换；用户选择保存在 localStorage，跟随模式会响应系统主题变化。
- 所有主题统一使用 UPS 设备主视觉，不在不同主题之间切换设备布局。
- 保留紧凑线性 SVG 图标、6 个指标卡片、24 小时趋势、最近事件和折叠设置区。
- 页面内部不再重复显示 UPS Monitor 大标题和副标题，fnOS 窗口标题栏负责显示应用名称。

## 0.1.4 修复点

- `platform=all` 改为 `platform=x86`。
- 移除 `install_dep_apps=python312`，避免应用中心在安装前查询 Python Runtime。
- 后端改为静态链接 Linux amd64 Go 单文件，无 Python / Node / Docker 依赖。
- `cmd/` 包含 fnpack 1.2.3 项目骨架要求的 9 个生命周期脚本：`main`、install/upgrade/uninstall/config 的 init + callback。
- `config/privilege`、`config/resource`、`app/ui/config`、4 个 wizard 文件均为严格 JSON。
- 继续使用 fnOS Unified Gateway：`/app/fnos-ups-monitor` -> `ups-monitor.sock`。
- 为隔离“应用包不符合系统要求”的原因，本测试版暂不声明 `os_min_version`；确认设备版本后发布版再恢复最低版本声明。
- 设置和历史改为 JSON / JSONL 持久化，不依赖 SQLite 动态库。

## 跨平台构建

高级配置示例和安全门禁说明见 [`docs/CONFIG.md`](docs/CONFIG.md)。

在 macOS 或 Linux 上生成两个独立候选包：

```bash
./build-release.sh all
```

每次执行打包命令都会先将 `VERSION` 的补丁版本自动加 1，并同步更新 `package/manifest`。一次 `all` 同时生成 x86 和 ARM 安装包，只递增一次版本号。

也可以只构建一个架构：

```bash
./build-release.sh x86
./build-release.sh arm
```

输出：

```text
dist/fnos-ups-monitor_1.2.1_x86.fpk
dist/fnos-ups-monitor_1.2.1_arm.fpk
```

## 官方 fnpack 1.2.3 构建

在 Linux amd64 构建机上，`build-with-fnpack.sh` 会从飞牛官方地址下载 fnpack 1.2.3、校验固定 SHA-256，并构建指定架构：

```bash
./audit.sh
./build-with-fnpack.sh x86
./build-with-fnpack.sh arm
```

输出：

```text
dist/fnos-ups-monitor_1.2.1_x86.fpk
dist/fnos-ups-monitor_1.2.1_arm.fpk
```

## 安装后的日志

```bash
tail -f /var/apps/fnos-ups-monitor/var/ups-monitor.log
```

状态检查：

```bash
/var/apps/fnos-ups-monitor/cmd/main status
echo $?
```

返回 `0` 表示运行，`3` 表示未运行。
