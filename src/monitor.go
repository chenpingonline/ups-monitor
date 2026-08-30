package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Monitor struct {
	cfg          *ConfigStore
	store        *Store
	mu           sync.RWMutex
	latest       Status
	lastStatus   string
	wasConnected *bool
	lastHistory  int64
	lastCleanup  int64
}

func NewMonitor(config *ConfigStore, store *Store) *Monitor {
	return &Monitor{cfg: config, store: store, latest: Status{TS: time.Now().Unix(), Error: "等待首次采集", UPSList: []UPSInfo{}}}
}

func (m *Monitor) Get() Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.latest
}

func (m *Monitor) set(status Status) {
	m.mu.Lock()
	m.latest = status
	m.mu.Unlock()
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (m *Monitor) emit(severity, eventType, message string) {
	event := m.store.AddEvent(severity, eventType, message)
	config := m.cfg.Get()
	if config.WebhookURL != "" {
		go sendWebhook(config, event)
	}
}

func (m *Monitor) transitions(current, previous Status) {
	previousFlags := strings.Fields(m.lastStatus)
	flags := current.StatusFlags
	if contains(flags, "OB") && !contains(previousFlags, "OB") {
		m.emit("warning", "on_battery", "检测到市电中断，UPS 正在电池供电")
	}
	if contains(flags, "OL") && !contains(previousFlags, "OL") && m.lastStatus != "" {
		m.emit("info", "online", "市电已恢复，UPS 回到在线供电")
	}
	if contains(flags, "LB") && !contains(previousFlags, "LB") {
		m.emit("critical", "low_battery", "UPS 报告低电量状态")
	}
	if contains(flags, "OVER") && !contains(previousFlags, "OVER") {
		m.emit("critical", "overload", "UPS 报告过载状态")
	}
	if contains(flags, "RB") && !contains(previousFlags, "RB") {
		m.emit("warning", "replace_battery", "UPS 建议更换电池")
	}
	threshold := float64(m.cfg.Get().LowBatteryThreshold)
	if current.Charge != nil && *current.Charge <= threshold && (previous.Charge == nil || *previous.Charge > threshold) {
		m.emit("warning", "charge_threshold", fmt.Sprintf("UPS 电量已降至 %.0f%%（阈值 %.0f%%）", *current.Charge, threshold))
	}
	m.lastStatus = current.Status
}

func (m *Monitor) pollOnce() error {
	config := m.cfg.Get()
	client := NutClient{Host: config.NutHost, Port: config.NutPort, Timeout: 4 * time.Second}
	upsList, err := client.ListUPS()
	if err != nil {
		return err
	}
	if len(upsList) == 0 {
		return errors.New("NUT 服务未返回任何 UPS")
	}
	upsName := config.UPSName
	if upsName == "" {
		upsName = upsList[0].Name
	}
	found := false
	for _, ups := range upsList {
		if ups.Name == upsName {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("配置的 UPS '%s' 不存在", upsName)
	}
	values, err := client.Vars(upsName)
	if err != nil {
		return err
	}
	if len(values) == 0 {
		return errors.New("未读取到 UPS 变量")
	}
	current := normalize(upsName, upsList, values)
	previous := m.Get()
	m.transitions(current, previous)
	m.set(current)
	if m.wasConnected != nil && !*m.wasConnected {
		m.emit("info", "recovered", "UPS 通信已恢复")
	}
	connected := true
	m.wasConnected = &connected
	now := current.TS
	if now-m.lastHistory >= int64(config.HistoryInterval) {
		m.store.AddHistory(current)
		m.lastHistory = now
	}
	if now-m.lastCleanup > 21600 {
		m.store.Cleanup(config.RetentionDays)
		m.lastCleanup = now
	}
	return nil
}

func (m *Monitor) Run(ctx context.Context) {
	for {
		if err := m.pollOnce(); err != nil {
			previous := m.Get()
			m.set(Status{Connected: false, TS: time.Now().Unix(), Error: err.Error(), UPSList: previous.UPSList})
			if m.wasConnected == nil || *m.wasConnected {
				m.emit("critical", "connection_lost", "无法读取 UPS："+err.Error())
			}
			connected := false
			m.wasConnected = &connected
		}
		timer := time.NewTimer(time.Duration(m.cfg.Get().PollInterval) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func sendWebhook(config Config, event Event) {
	body, _ := json.Marshal(map[string]any{"source": "fnOS UPS Monitor", "event": event})
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(config.WebhookTimeout)*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, config.WebhookURL, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "fnos-ups-monitor/0.1.4")
	response, err := http.DefaultClient.Do(request)
	if err == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1024))
		_ = response.Body.Close()
	}
}
