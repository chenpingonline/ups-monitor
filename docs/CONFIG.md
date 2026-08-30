# UPS Monitor 1.1.0 高级配置

高级配置可以在 Web UI 中编辑。保存前会执行严格校验，旧版 `nut_host`、`nut_port`、`ups_name` 配置仍然兼容。

```json
{
  "targets": [
    {
      "id": "rack-a",
      "name": "机柜 A",
      "host": "127.0.0.1",
      "port": 3493,
      "ups_name": "main",
      "username": "monitor",
      "password": "replace-me",
      "enabled": true
    }
  ],
  "alert_rules": [
    {
      "id": "high-load",
      "metric": "load",
      "operator": "gte",
      "threshold": 90,
      "duration_seconds": 60,
      "recovery_delta": 5,
      "cooldown_seconds": 900,
      "severity": "warning",
      "enabled": true
    }
  ],
  "notification": {
    "max_retries": 5,
    "retry_seconds": 15,
    "channels": [
      {
        "id": "ntfy-home",
        "type": "ntfy",
        "url": "https://ntfy.sh/replace-topic",
        "enabled": false
      }
    ]
  },
  "mqtt": {
    "enabled": false,
    "broker": "192.0.2.10:1883",
    "topic": "fnos/ups",
    "client_id": "fnos-ups-monitor"
  },
  "api_token": "",
  "self_test": {
    "enabled": false,
    "interval_days": 30,
    "command": "test.battery.start.quick"
  },
  "shutdown": {
    "enabled": false,
    "dry_run": true,
    "on_battery_seconds": 300,
    "charge_below": 10,
    "runtime_below": 300,
    "countdown_seconds": 60,
    "command": "/sbin/poweroff",
    "confirmation": ""
  }
}
```

告警指标支持 `charge`、`runtime`、`load`、`input_voltage`、`output_voltage`、`battery_voltage`、`input_frequency`、`real_power`、`temperature`。运算符支持 `lt`、`lte`、`gt`、`gte`。

## UPS 控制账号与能力检测

监控状态不需要 NUT 控制账号。快速自检、深度自检和停止自检等控制操作需要在对应 `targets` 中配置 `username` 与 `password`，并在 NUT 的 `upsd.users` 中授予相应命令权限。

页面会通过 NUT `LIST CMD` 与 `LIST RW` 查询当前硬件实际支持的命令和可调参数。未被设备报告的自检按钮不会显示；计划自检也会先检查命令是否受支持，再发送控制命令。可调参数当前仅展示，不会自动修改 UPS 设置。

通知类型支持 `webhook`、`ntfy`、`gotify`、`telegram`、`wecom`、`dingtalk`。待发送任务保存在应用数据目录的 `notification-queue/` 中；成功后删除，失败时指数退避重试，重试耗尽后写入事件记录。

Prometheus 默认地址为 `/app/fnos-ups-monitor/metrics`。配置 `api_token` 后，请使用 `Authorization: Bearer <token>` 请求指标。

MQTT 使用 3.1.1 QoS 0，将每台设备状态发布到 `<topic>/<target-id>/status`。

## 关机安全门禁

默认不会关闭系统。实际关机必须同时满足：

1. `shutdown.enabled=true`；
2. `shutdown.dry_run=false`；
3. `shutdown.confirmation="I_UNDERSTAND_POWER_OFF"`；
4. 启动进程时存在 `UPS_MONITOR_ALLOW_SYSTEM_SHUTDOWN=1`；
5. 电池供电时至少一个触发条件成立；
6. 安全倒计时结束后 UPS 仍然处于电池供电，且管理员没有取消。

应用仅允许 `/sbin/poweroff` 或 `/sbin/shutdown -h now`，不接受任意 Shell 命令。建议先长期运行演练模式，确认事件时间线符合预期后再考虑开启实际关机。
