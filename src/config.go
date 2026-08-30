package main

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type Config struct {
	NutHost             string `json:"nut_host"`
	NutPort             int    `json:"nut_port"`
	UPSName             string `json:"ups_name"`
	PollInterval        int    `json:"poll_interval"`
	HistoryInterval     int    `json:"history_interval"`
	RetentionDays       int    `json:"retention_days"`
	LowBatteryThreshold int    `json:"low_battery_threshold"`
	WebhookURL          string `json:"webhook_url"`
	WebhookTimeout      int    `json:"webhook_timeout"`
}

func defaultConfig() Config {
	return Config{NutHost: "127.0.0.1", NutPort: 3493, PollInterval: 10, HistoryInterval: 60, RetentionDays: 30, LowBatteryThreshold: 25, WebhookTimeout: 5}
}

type ConfigStore struct {
	mu   sync.RWMutex
	path string
	c    Config
}

func NewConfigStore(path string) *ConfigStore {
	store := &ConfigStore{path: path, c: defaultConfig()}
	_ = store.Load()
	return store
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
	return config, nil
}

func (s *ConfigStore) Load() error {
	_ = os.MkdirAll(filepath.Dir(s.path), 0750)
	contents, err := os.ReadFile(s.path)
	if err == nil {
		var config Config
		if json.Unmarshal(contents, &config) == nil {
			mergeConfigDefaults(&config)
			if valid, validationErr := validateConfig(config); validationErr == nil {
				s.c = valid
			}
		}
	}
	return s.Save(s.c)
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
	_ = os.MkdirAll(filepath.Dir(s.path), 0750)
	contents, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	temporaryPath := s.path + ".tmp"
	if err := os.WriteFile(temporaryPath, append(contents, '\n'), 0640); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, s.path); err != nil {
		return err
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
}
