package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type Config struct {
	NutHost             string             `json:"nut_host"`
	NutPort             int                `json:"nut_port"`
	UPSName             string             `json:"ups_name"`
	PollInterval        int                `json:"poll_interval"`
	HistoryInterval     int                `json:"history_interval"`
	RetentionDays       int                `json:"retention_days"`
	LowBatteryThreshold int                `json:"low_battery_threshold"`
	WebhookURL          string             `json:"webhook_url"`
	WebhookTimeout      int                `json:"webhook_timeout"`
	Targets             []NUTTarget        `json:"targets,omitempty"`
	AlertRules          []AlertRule        `json:"alert_rules,omitempty"`
	Notification        NotificationConfig `json:"notification"`
	MQTT                MQTTConfig         `json:"mqtt"`
	APIToken            string             `json:"api_token,omitempty"`
	Shutdown            ShutdownPolicy     `json:"shutdown"`
	SelfTest            SelfTestConfig     `json:"self_test"`
}

type NUTTarget struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	UPSName  string `json:"ups_name"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Enabled  bool   `json:"enabled"`
}

type AlertRule struct {
	ID              string  `json:"id"`
	Metric          string  `json:"metric"`
	Operator        string  `json:"operator"`
	Threshold       float64 `json:"threshold"`
	DurationSeconds int     `json:"duration_seconds"`
	RecoveryDelta   float64 `json:"recovery_delta"`
	CooldownSeconds int     `json:"cooldown_seconds"`
	Severity        string  `json:"severity"`
	Enabled         bool    `json:"enabled"`
}

type NotificationConfig struct {
	MaxRetries   int                   `json:"max_retries"`
	RetrySeconds int                   `json:"retry_seconds"`
	Channels     []NotificationChannel `json:"channels,omitempty"`
}

type NotificationChannel struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	URL     string `json:"url,omitempty"`
	Token   string `json:"token,omitempty"`
	ChatID  string `json:"chat_id,omitempty"`
	Enabled bool   `json:"enabled"`
}

type MQTTConfig struct {
	Enabled  bool   `json:"enabled"`
	Broker   string `json:"broker"`
	Topic    string `json:"topic"`
	ClientID string `json:"client_id"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

type ShutdownPolicy struct {
	Enabled          bool   `json:"enabled"`
	DryRun           bool   `json:"dry_run"`
	OnBatterySeconds int    `json:"on_battery_seconds"`
	ChargeBelow      int    `json:"charge_below"`
	RuntimeBelow     int    `json:"runtime_below"`
	CountdownSeconds int    `json:"countdown_seconds"`
	Command          string `json:"command"`
	Confirmation     string `json:"confirmation"`
}

type SelfTestConfig struct {
	Enabled      bool   `json:"enabled"`
	IntervalDays int    `json:"interval_days"`
	Command      string `json:"command"`
}

func defaultConfig() Config {
	return Config{
		NutHost: "127.0.0.1", NutPort: 3493, PollInterval: 10, HistoryInterval: 60,
		RetentionDays: 30, LowBatteryThreshold: 25, WebhookTimeout: 5,
		Notification: NotificationConfig{MaxRetries: 5, RetrySeconds: 15},
		Shutdown:     ShutdownPolicy{DryRun: true, OnBatterySeconds: 300, ChargeBelow: 10, RuntimeBelow: 300, CountdownSeconds: 60, Command: "/sbin/poweroff"},
		SelfTest:     SelfTestConfig{IntervalDays: 30, Command: "test.battery.start.quick"},
	}
}

type ConfigStore struct {
	mu   sync.RWMutex
	path string
	c    Config
}

func OpenConfigStore(path string) (*ConfigStore, error) {
	store := &ConfigStore{path: path, c: defaultConfig()}
	if err := store.Load(); err != nil {
		return nil, err
	}
	return store, nil
}

func clamp(value, defaultValue, low, high int) int {
	if value == 0 {
		value = defaultValue
	}
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func validateConfig(input Config) (Config, error) {
	config := input
	config.NutHost = strings.TrimSpace(config.NutHost)
	if config.NutHost == "" || len(config.NutHost) > 255 || strings.ContainsAny(config.NutHost, "\r\n\x00") {
		return Config{}, errors.New("NUT 主机地址无效")
	}
	config.NutPort = clamp(config.NutPort, 3493, 1, 65535)
	config.UPSName = strings.TrimSpace(config.UPSName)
	if len(config.UPSName) > 128 || strings.ContainsAny(config.UPSName, " \r\n\x00") {
		return Config{}, errors.New("UPS 名称无效")
	}
	config.PollInterval = clamp(config.PollInterval, 10, 5, 300)
	config.HistoryInterval = clamp(config.HistoryInterval, 60, 30, 3600)
	config.RetentionDays = clamp(config.RetentionDays, 30, 1, 365)
	config.LowBatteryThreshold = clamp(config.LowBatteryThreshold, 25, 1, 99)
	config.WebhookTimeout = clamp(config.WebhookTimeout, 5, 1, 30)
	config.WebhookURL = strings.TrimSpace(config.WebhookURL)
	if len(config.WebhookURL) > 2048 {
		return Config{}, errors.New("Webhook URL 过长")
	}
	if config.WebhookURL != "" {
		parsed, err := url.Parse(config.WebhookURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return Config{}, errors.New("Webhook 仅支持有效的 http/https URL")
		}
	}
	if len(config.Targets) > 32 {
		return Config{}, errors.New("NUT 目标最多支持 32 个")
	}
	seenTargets := map[string]bool{}
	for index := range config.Targets {
		target := &config.Targets[index]
		target.ID = strings.TrimSpace(target.ID)
		target.Name = strings.TrimSpace(target.Name)
		target.Host = strings.TrimSpace(target.Host)
		target.UPSName = strings.TrimSpace(target.UPSName)
		if target.ID == "" || len(target.ID) > 64 || strings.ContainsAny(target.ID, " /\\\r\n\x00") {
			return Config{}, fmt.Errorf("第 %d 个 NUT 目标 ID 无效", index+1)
		}
		if seenTargets[target.ID] {
			return Config{}, fmt.Errorf("NUT 目标 ID '%s' 重复", target.ID)
		}
		seenTargets[target.ID] = true
		if target.Name == "" {
			target.Name = target.ID
		}
		if target.Host == "" || len(target.Host) > 255 || strings.ContainsAny(target.Host, "\r\n\x00") {
			return Config{}, fmt.Errorf("NUT 目标 '%s' 主机无效", target.ID)
		}
		target.Port = clamp(target.Port, 3493, 1, 65535)
		if len(target.UPSName) > 128 || strings.ContainsAny(target.UPSName, " \r\n\x00") {
			return Config{}, fmt.Errorf("NUT 目标 '%s' UPS 名称无效", target.ID)
		}
		if len(target.Username) > 128 || strings.ContainsAny(target.Username, " \r\n\x00") || len(target.Password) > 512 || strings.ContainsAny(target.Password, "\r\n\x00") {
			return Config{}, fmt.Errorf("NUT 目标 '%s' 凭据无效", target.ID)
		}
	}
	if len(config.AlertRules) > 64 {
		return Config{}, errors.New("告警规则最多支持 64 条")
	}
	for index := range config.AlertRules {
		rule := &config.AlertRules[index]
		rule.ID = strings.TrimSpace(rule.ID)
		if rule.ID == "" || len(rule.ID) > 64 {
			return Config{}, fmt.Errorf("第 %d 条告警规则 ID 无效", index+1)
		}
		switch rule.Metric {
		case "charge", "runtime", "load", "input_voltage", "output_voltage", "battery_voltage", "input_frequency", "real_power", "temperature":
		default:
			return Config{}, fmt.Errorf("告警规则 '%s' 指标无效", rule.ID)
		}
		switch rule.Operator {
		case "lt", "lte", "gt", "gte":
		default:
			return Config{}, fmt.Errorf("告警规则 '%s' 运算符无效", rule.ID)
		}
		rule.DurationSeconds = clamp(rule.DurationSeconds, 0, 0, 86400)
		rule.CooldownSeconds = clamp(rule.CooldownSeconds, 300, 0, 86400)
		if rule.Severity == "" {
			rule.Severity = "warning"
		}
		switch rule.Severity {
		case "info", "warning", "critical":
		default:
			return Config{}, fmt.Errorf("告警规则 '%s' 严重级别无效", rule.ID)
		}
	}
	config.Notification.MaxRetries = clamp(config.Notification.MaxRetries, 5, 0, 20)
	config.Notification.RetrySeconds = clamp(config.Notification.RetrySeconds, 15, 1, 3600)
	if len(config.Notification.Channels) > 16 {
		return Config{}, errors.New("通知渠道最多支持 16 个")
	}
	channelIDs := map[string]bool{}
	for index := range config.Notification.Channels {
		channel := &config.Notification.Channels[index]
		channel.ID = strings.TrimSpace(channel.ID)
		channel.Type = strings.TrimSpace(channel.Type)
		channel.URL = strings.TrimSpace(channel.URL)
		if channel.ID == "" || len(channel.ID) > 64 || strings.ContainsAny(channel.ID, " /\\\r\n\x00") {
			return Config{}, fmt.Errorf("第 %d 个通知渠道 ID 无效", index+1)
		}
		if channelIDs[channel.ID] {
			return Config{}, fmt.Errorf("通知渠道 ID '%s' 重复", channel.ID)
		}
		channelIDs[channel.ID] = true
		switch channel.Type {
		case "webhook", "ntfy", "gotify", "telegram", "wecom", "dingtalk":
		default:
			return Config{}, fmt.Errorf("通知渠道 '%s' 类型无效", channel.ID)
		}
		if channel.Enabled && channel.Type != "telegram" && channel.URL == "" {
			return Config{}, fmt.Errorf("通知渠道 '%s' 缺少 URL", channel.ID)
		}
		if channel.Enabled && channel.Type == "telegram" && (channel.Token == "" || channel.ChatID == "") {
			return Config{}, fmt.Errorf("Telegram 渠道 '%s' 缺少 Token 或 Chat ID", channel.ID)
		}
		if channel.URL != "" {
			parsed, err := url.Parse(channel.URL)
			if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
				return Config{}, fmt.Errorf("通知渠道 '%s' URL 无效", channel.ID)
			}
		}
	}
	config.APIToken = strings.TrimSpace(config.APIToken)
	if len(config.APIToken) > 512 || strings.ContainsAny(config.APIToken, "\r\n\x00") {
		return Config{}, errors.New("API Token 无效")
	}
	config.MQTT.Broker = strings.TrimSpace(config.MQTT.Broker)
	config.MQTT.Topic = strings.Trim(strings.TrimSpace(config.MQTT.Topic), "/")
	if config.MQTT.Enabled && (config.MQTT.Broker == "" || config.MQTT.Topic == "") {
		return Config{}, errors.New("启用 MQTT 时必须配置 Broker 和 Topic")
	}
	if strings.ContainsAny(config.MQTT.Broker+config.MQTT.Topic+config.MQTT.Username+config.MQTT.Password, "\r\n\x00") {
		return Config{}, errors.New("MQTT 配置包含无效字符")
	}
	if config.MQTT.Password != "" && config.MQTT.Username == "" {
		return Config{}, errors.New("MQTT 配置密码时必须同时配置用户名")
	}
	config.Shutdown.OnBatterySeconds = clamp(config.Shutdown.OnBatterySeconds, 300, 30, 86400)
	config.Shutdown.ChargeBelow = clamp(config.Shutdown.ChargeBelow, 10, 1, 99)
	config.Shutdown.RuntimeBelow = clamp(config.Shutdown.RuntimeBelow, 300, 30, 86400)
	config.Shutdown.CountdownSeconds = clamp(config.Shutdown.CountdownSeconds, 60, 10, 600)
	config.Shutdown.Command = strings.TrimSpace(config.Shutdown.Command)
	if config.Shutdown.Command == "" {
		config.Shutdown.Command = "/sbin/poweroff"
	}
	if config.Shutdown.Command != "/sbin/poweroff" && config.Shutdown.Command != "/sbin/shutdown -h now" {
		return Config{}, errors.New("仅允许受支持的系统关机命令")
	}
	config.SelfTest.IntervalDays = clamp(config.SelfTest.IntervalDays, 30, 1, 365)
	if config.SelfTest.Command == "" {
		config.SelfTest.Command = "test.battery.start.quick"
	}
	switch config.SelfTest.Command {
	case "test.battery.start.quick", "test.battery.start.deep":
	default:
		return Config{}, errors.New("计划自检命令无效")
	}
	return config, nil
}

func (s *ConfigStore) Load() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0750); err != nil {
		return fmt.Errorf("创建配置目录: %w", err)
	}
	contents, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s.Save(s.c)
	}
	if err != nil {
		return fmt.Errorf("读取配置: %w", err)
	}
	var config Config
	if err := json.Unmarshal(contents, &config); err != nil {
		return fmt.Errorf("解析配置: %w", err)
	}
	mergeConfigDefaults(&config)
	valid, err := validateConfig(config)
	if err != nil {
		return fmt.Errorf("校验配置: %w", err)
	}
	s.c = valid
	return s.Save(valid)
}

func mergeConfigDefaults(config *Config) {
	defaults := defaultConfig()
	if config.NutHost == "" {
		config.NutHost = defaults.NutHost
	}
	if config.NutPort == 0 {
		config.NutPort = defaults.NutPort
	}
	if config.PollInterval == 0 {
		config.PollInterval = defaults.PollInterval
	}
	if config.HistoryInterval == 0 {
		config.HistoryInterval = defaults.HistoryInterval
	}
	if config.RetentionDays == 0 {
		config.RetentionDays = defaults.RetentionDays
	}
	if config.LowBatteryThreshold == 0 {
		config.LowBatteryThreshold = defaults.LowBatteryThreshold
	}
	if config.WebhookTimeout == 0 {
		config.WebhookTimeout = defaults.WebhookTimeout
	}
	if config.Notification.MaxRetries == 0 {
		config.Notification.MaxRetries = defaults.Notification.MaxRetries
	}
	if config.Notification.RetrySeconds == 0 {
		config.Notification.RetrySeconds = defaults.Notification.RetrySeconds
	}
	if config.Shutdown.OnBatterySeconds == 0 {
		config.Shutdown.OnBatterySeconds = defaults.Shutdown.OnBatterySeconds
	}
	if config.Shutdown.ChargeBelow == 0 {
		config.Shutdown.ChargeBelow = defaults.Shutdown.ChargeBelow
	}
	if config.Shutdown.RuntimeBelow == 0 {
		config.Shutdown.RuntimeBelow = defaults.Shutdown.RuntimeBelow
	}
	if config.Shutdown.CountdownSeconds == 0 {
		config.Shutdown.CountdownSeconds = defaults.Shutdown.CountdownSeconds
	}
	if config.Shutdown.Command == "" {
		config.Shutdown.Command = defaults.Shutdown.Command
	}
	if config.SelfTest.IntervalDays == 0 {
		config.SelfTest.IntervalDays = defaults.SelfTest.IntervalDays
	}
	if config.SelfTest.Command == "" {
		config.SelfTest.Command = defaults.SelfTest.Command
	}
	if !config.Shutdown.Enabled && !config.Shutdown.DryRun && config.Shutdown.Confirmation == "" {
		config.Shutdown.DryRun = true
	}
}

func (s *ConfigStore) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.c
}

func (s *ConfigStore) Save(input Config) error {
	config, err := validateConfig(input)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0750); err != nil {
		return fmt.Errorf("创建配置目录: %w", err)
	}
	contents, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("编码配置: %w", err)
	}
	temporaryPath := s.path + ".tmp"
	if err := os.WriteFile(temporaryPath, append(contents, '\n'), 0640); err != nil {
		return fmt.Errorf("写入配置: %w", err)
	}
	if err := os.Rename(temporaryPath, s.path); err != nil {
		return fmt.Errorf("替换配置: %w", err)
	}
	s.c = config
	return nil
}

func mergeConfig(destination *Config, source Config) {
	if source.NutHost != "" {
		destination.NutHost = source.NutHost
	}
	if source.NutPort != 0 {
		destination.NutPort = source.NutPort
	}
	destination.UPSName = source.UPSName
	if source.PollInterval != 0 {
		destination.PollInterval = source.PollInterval
	}
	if source.HistoryInterval != 0 {
		destination.HistoryInterval = source.HistoryInterval
	}
	if source.RetentionDays != 0 {
		destination.RetentionDays = source.RetentionDays
	}
	if source.LowBatteryThreshold != 0 {
		destination.LowBatteryThreshold = source.LowBatteryThreshold
	}
	destination.WebhookURL = source.WebhookURL
	if source.WebhookTimeout != 0 {
		destination.WebhookTimeout = source.WebhookTimeout
	}
	if source.Targets != nil {
		destination.Targets = source.Targets
	}
	if source.AlertRules != nil {
		destination.AlertRules = source.AlertRules
	}
	if source.Notification.MaxRetries != 0 {
		destination.Notification.MaxRetries = source.Notification.MaxRetries
	}
	if source.Notification.RetrySeconds != 0 {
		destination.Notification.RetrySeconds = source.Notification.RetrySeconds
	}
	if source.MQTT != (MQTTConfig{}) {
		destination.MQTT = source.MQTT
	}
	if source.APIToken != "" {
		destination.APIToken = source.APIToken
	}
	if source.Shutdown != (ShutdownPolicy{}) {
		destination.Shutdown = source.Shutdown
	}
	if source.SelfTest != (SelfTestConfig{}) {
		destination.SelfTest = source.SelfTest
	}
}

func (c Config) EffectiveTargets() []NUTTarget {
	if len(c.Targets) > 0 {
		result := make([]NUTTarget, 0, len(c.Targets))
		for _, target := range c.Targets {
			if target.Enabled {
				result = append(result, target)
			}
		}
		return result
	}
	return []NUTTarget{{ID: "default", Name: "默认 UPS", Host: c.NutHost, Port: c.NutPort, UPSName: c.UPSName, Enabled: true}}
}
