# fnOS UPS Monitor 0.1.4 (x86)

这是针对 fnOS x86_64 NAS 的修复版 UPS 监控 FPK 项目。


## 0.1.4 UI 更新

- 删除暗色主题左侧导航栏，恢复单页监控仪表盘布局。
- 亮色与暗色主题现在使用完全相同的 DOM、卡片位置、尺寸与图标，只改变颜色、边框和阴影。
- 右上角放置亮色 / 暗色切换和连接状态，主题继续保存在 localStorage。
- 两种主题统一使用 UPS 设备主视觉，不再在亮色和暗色之间切换不同的电池/设备布局。
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

## 官方 fnpack 1.2.3 构建

项目提供 `build-with-fnpack.sh`，会从飞牛官方地址下载 Linux amd64 fnpack 1.2.3，并校验固定 SHA-256 后构建：

```bash
./audit.sh
./build-with-fnpack.sh
```

输出：

```text
dist/fnos-ups-monitor_0.1.4_x86.fpk
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
