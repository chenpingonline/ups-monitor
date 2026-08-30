package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type alertState struct {
	Since        int64
	Active       bool
	LastNotified int64
}

type targetRuntime struct {
	LastStatus        string
	WasConnected      *bool
	LastHistory       int64
	LastCleanup       int64
	OnBatterySince    int64
	ShutdownRequested atomic.Bool
	Rules             map[string]*alertState
	TransientSince    map[string]int64
	LastSelfTest      int64
}

type Monitor struct {
	cfg                      *ConfigStore
	store                    *Store
	mu                       sync.RWMutex
	runtimeMu                sync.Mutex
	latest                   map[string]Status
	targetOrder              []string
	runtime                  map[string]*targetRuntime
	startedAt                int64
	lastPollAt               int64
	lastSuccessAt            int64
	lastHistoryWriteAt       int64
	lastError                string
	lastStorageError         string
	lastWebhookError         string
	lastMQTTError            string
	shutdownExecutionAllowed bool
	notifyWake               chan struct{}
}

type Health struct {
	OK                 bool   `json:"ok"`
	Version            string `json:"version"`
	StartedAt          int64  `json:"started_at"`
	LastPollAt         int64  `json:"last_poll_at"`
	LastSuccessAt      int64  `json:"last_success_at"`
	LastHistoryWriteAt int64  `json:"last_history_write_at"`
	TargetCount        int    `json:"target_count"`
	ConnectedCount     int    `json:"connected_count"`
	LastError          string `json:"last_error,omitempty"`
	LastStorageError   string `json:"last_storage_error,omitempty"`
	LastWebhookError   string `json:"last_webhook_error,omitempty"`
	LastMQTTError      string `json:"last_mqtt_error,omitempty"`
}

type ShutdownPlan struct {
	Enabled              bool     `json:"enabled"`
	DryRun               bool     `json:"dry_run"`
	ExecutionGateEnabled bool     `json:"execution_gate_enabled"`
	Steps                []string `json:"steps"`
	Triggers             []string `json:"triggers"`
}

func NewMonitor(config *ConfigStore, store *Store) *Monitor {
	now := time.Now().Unix()
	return &Monitor{
		cfg: config, store: store, latest: map[string]Status{}, runtime: map[string]*targetRuntime{},
		startedAt: now, shutdownExecutionAllowed: os.Getenv("UPS_MONITOR_ALLOW_SYSTEM_SHUTDOWN") == "1", notifyWake: make(chan struct{}, 1),
	}
}

func (m *Monitor) Health() Health {
	m.mu.RLock()
	defer m.mu.RUnlock()
	connected := 0
	for _, status := range m.latest {
		if status.Connected {
			connected++
		}
	}
	return Health{
		OK: len(m.latest) > 0 && connected == len(m.latest), Version: version, StartedAt: m.startedAt,
		LastPollAt: m.lastPollAt, LastSuccessAt: m.lastSuccessAt, LastHistoryWriteAt: m.lastHistoryWriteAt,
		TargetCount: len(m.latest), ConnectedCount: connected, LastError: m.lastError,
		LastStorageError: m.lastStorageError, LastWebhookError: m.lastWebhookError,
		LastMQTTError: m.lastMQTTError,
	}
}

func (m *Monitor) Get() Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, id := range m.targetOrder {
		if status, ok := m.latest[id]; ok {
			return status
		}
	}
	return Status{TS: time.Now().Unix(), Error: "等待首次采集", UPSList: []UPSInfo{}}
}

func (m *Monitor) GetAll() []Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]Status, 0, len(m.latest))
	seen := map[string]bool{}
	for _, id := range m.targetOrder {
		if status, ok := m.latest[id]; ok {
			result = append(result, status)
			seen[id] = true
		}
	}
	remaining := make([]string, 0)
	for id := range m.latest {
		if !seen[id] {
			remaining = append(remaining, id)
		}
	}
	sort.Strings(remaining)
	for _, id := range remaining {
		result = append(result, m.latest[id])
	}
	return result
}

func (m *Monitor) set(status Status) {
	id := status.TargetID
	if id == "" {
		id = "default"
		status.TargetID = id
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.latest[id] = status
	if !contains(m.targetOrder, id) {
		m.targetOrder = append(m.targetOrder, id)
	}
}

func (m *Monitor) targetState(id string) *targetRuntime {
	m.runtimeMu.Lock()
	defer m.runtimeMu.Unlock()
	state := m.runtime[id]
	if state == nil {
		state = &targetRuntime{Rules: map[string]*alertState{}, TransientSince: map[string]int64{}}
		m.runtime[id] = state
	}
	return state
}

func filterKnownUPSQuirks(status Status, state *targetRuntime, now int64) Status {
	if status.Profile.Brand != "施耐德 APC" || !strings.Contains(strings.ToUpper(status.Profile.Model), "BK650M2-CH") {
		return status
	}
	if state.TransientSince == nil {
		state.TransientSince = map[string]int64{}
	}
	flags := append([]string(nil), status.StatusFlags...)
	if contains(flags, "OL") && !contains(flags, "OB") {
		filtered := make([]string, 0, len(flags))
		for _, flag := range flags {
			if flag == "DISCHRG" {
				continue
			}
			if flag == "LB" || flag == "RB" {
				since := state.TransientSince[flag]
				if since == 0 {
					state.TransientSince[flag] = now
					since = now
				}
				if now-since < 15 {
					continue
				}
			}
			filtered = append(filtered, flag)
		}
		flags = filtered
	}
	for _, flag := range []string{"LB", "RB"} {
		if !contains(status.StatusFlags, flag) {
			delete(state.TransientSince, flag)
		}
	}
	status.StatusFlags = flags
	status.Status = strings.Join(flags, " ")
	status.StatusText = statusText(flags)
	return status
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (m *Monitor) setStorageError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err == nil {
		m.lastStorageError = ""
	} else {
		m.lastStorageError = err.Error()
	}
}

func (m *Monitor) setWebhookError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err == nil {
		m.lastWebhookError = ""
	} else {
		m.lastWebhookError = err.Error()
	}
}

func (m *Monitor) emit(targetID, severity, eventType, message string) {
	event, err := m.store.AddTargetEvent(targetID, severity, eventType, message)
	if err != nil {
		m.setStorageError(err)
		log.Printf("storage error: %v", err)
		return
	}
	config := m.cfg.Get()
	queued := false
	if config.WebhookURL != "" {
		if err := m.store.EnqueueNotification(event, config); err != nil {
			m.setStorageError(err)
			log.Printf("notification queue error: %v", err)
		} else {
			queued = true
		}
	}
	for _, channel := range config.Notification.Channels {
		if !channel.Enabled {
			continue
		}
		if err := m.store.EnqueueChannel(event, config, channel); err != nil {
			m.setStorageError(err)
			log.Printf("notification queue error: %v", err)
		} else {
			queued = true
		}
	}
	if queued {
		select {
		case m.notifyWake <- struct{}{}:
		default:
		}
	}
}

func (m *Monitor) processNotifications() {
	jobs, err := m.store.PendingNotifications(time.Now().Unix(), 16)
	if err != nil {
		m.setStorageError(err)
		return
	}
	for _, job := range jobs {
		deliveryErr := sendNotification(job)
		if deliveryErr == nil {
			if err := m.store.CompleteNotification(job.ID); err != nil {
				m.setStorageError(err)
			}
			m.setWebhookError(nil)
			continue
		}
		job.Attempts++
		job.LastError = deliveryErr.Error()
		m.setWebhookError(deliveryErr)
		if job.Attempts > job.MaxRetries {
			_, _ = m.store.AddTargetEvent(job.Event.TargetID, "critical", "notification_failed", "Webhook 通知重试耗尽："+deliveryErr.Error())
			_ = m.store.CompleteNotification(job.ID)
			continue
		}
		delay := job.RetrySeconds * (1 << min(job.Attempts-1, 8))
		if delay > 3600 {
			delay = 3600
		}
		job.NextAttempt = time.Now().Add(time.Duration(delay) * time.Second).Unix()
		if err := m.store.UpdateNotification(job); err != nil {
			m.setStorageError(err)
		}
	}
}

func (m *Monitor) notificationWorker(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		m.processNotifications()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-m.notifyWake:
		}
	}
}

func (m *Monitor) transitions(targetID string, state *targetRuntime, current, previous Status) {
	previousFlags := strings.Fields(state.LastStatus)
	flags := current.StatusFlags
	if contains(flags, "OB") && !contains(previousFlags, "OB") {
		m.emit(targetID, "warning", "on_battery", current.TargetName+"：检测到市电中断，UPS 正在电池供电")
	}
	if contains(flags, "OL") && !contains(previousFlags, "OL") && state.LastStatus != "" {
		m.emit(targetID, "info", "online", current.TargetName+"：市电已恢复，UPS 回到在线供电")
	}
	if contains(flags, "LB") && !contains(previousFlags, "LB") {
		m.emit(targetID, "critical", "low_battery", current.TargetName+"：UPS 报告低电量状态")
	}
	if contains(flags, "OVER") && !contains(previousFlags, "OVER") {
		m.emit(targetID, "critical", "overload", current.TargetName+"：UPS 报告过载状态")
	}
	if contains(flags, "RB") && !contains(previousFlags, "RB") {
		m.emit(targetID, "warning", "replace_battery", current.TargetName+"：UPS 建议更换电池")
	}
	threshold := float64(m.cfg.Get().LowBatteryThreshold)
	if current.Charge != nil && *current.Charge <= threshold && (previous.Charge == nil || *previous.Charge > threshold) {
		m.emit(targetID, "warning", "charge_threshold", fmt.Sprintf("%s：UPS 电量已降至 %.0f%%（阈值 %.0f%%）", current.TargetName, *current.Charge, threshold))
	}
	state.LastStatus = current.Status
}

func metricValue(status Status, metric string) *float64 {
	switch metric {
	case "charge":
		return status.Charge
	case "runtime":
		return status.Runtime
	case "load":
		return status.Load
	case "input_voltage":
		return status.InputVoltage
	case "output_voltage":
		return status.OutputVoltage
	case "battery_voltage":
		return status.BatteryVoltage
	case "input_frequency":
		return status.InputFrequency
	case "real_power":
		return status.RealPower
	case "temperature":
		return status.Temperature
	default:
		return nil
	}
}

func compare(value float64, operator string, threshold float64) bool {
	switch operator {
	case "lt":
		return value < threshold
	case "lte":
		return value <= threshold
	case "gt":
		return value > threshold
	case "gte":
		return value >= threshold
	}
	return false
}

func recovered(value float64, rule AlertRule) bool {
	switch rule.Operator {
	case "lt", "lte":
		return value >= rule.Threshold+rule.RecoveryDelta
	case "gt", "gte":
		return value <= rule.Threshold-rule.RecoveryDelta
	}
	return true
}

func (m *Monitor) evaluateRules(targetID string, state *targetRuntime, status Status, now int64, rules []AlertRule) {
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		value := metricValue(status, rule.Metric)
		if value == nil {
			continue
		}
		ruleState := state.Rules[rule.ID]
		if ruleState == nil {
			ruleState = &alertState{}
			state.Rules[rule.ID] = ruleState
		}
		condition := compare(*value, rule.Operator, rule.Threshold)
		if condition {
			if ruleState.Since == 0 {
				ruleState.Since = now
			}
			if !ruleState.Active && now-ruleState.Since >= int64(rule.DurationSeconds) {
				ruleState.Active = true
				ruleState.LastNotified = now
				m.emit(targetID, rule.Severity, "rule_"+rule.ID, fmt.Sprintf("%s：%s 当前值 %.2f 触发阈值 %.2f", status.TargetName, rule.Metric, *value, rule.Threshold))
			} else if ruleState.Active && rule.CooldownSeconds > 0 && now-ruleState.LastNotified >= int64(rule.CooldownSeconds) {
				ruleState.LastNotified = now
				m.emit(targetID, rule.Severity, "rule_reminder_"+rule.ID, fmt.Sprintf("%s：%s 告警仍在持续，当前值 %.2f", status.TargetName, rule.Metric, *value))
			}
		} else if ruleState.Active && recovered(*value, rule) {
			ruleState.Active = false
			ruleState.Since = 0
			m.emit(targetID, "info", "rule_recovered_"+rule.ID, fmt.Sprintf("%s：%s 已恢复，当前值 %.2f", status.TargetName, rule.Metric, *value))
		} else if !ruleState.Active {
			ruleState.Since = 0
		}
	}
}

func (m *Monitor) evaluateShutdown(targetID string, state *targetRuntime, status Status, now int64, policy ShutdownPolicy) {
	if contains(status.StatusFlags, "OB") {
		if state.OnBatterySince == 0 {
			state.OnBatterySince = now
		}
	} else {
		state.OnBatterySince = 0
		state.ShutdownRequested.Store(false)
		return
	}
	if !policy.Enabled || state.ShutdownRequested.Load() {
		return
	}
	reasons := []string{}
	if now-state.OnBatterySince >= int64(policy.OnBatterySeconds) {
		reasons = append(reasons, "电池供电持续时间达到阈值")
	}
	if status.Charge != nil && *status.Charge <= float64(policy.ChargeBelow) {
		reasons = append(reasons, "电量达到关机阈值")
	}
	if status.Runtime != nil && *status.Runtime <= float64(policy.RuntimeBelow) {
		reasons = append(reasons, "预计续航达到关机阈值")
	}
	if len(reasons) == 0 {
		return
	}
	state.ShutdownRequested.Store(true)
	message := status.TargetName + "：关机策略已触发（" + strings.Join(reasons, "、") + "）"
	if policy.DryRun || !m.shutdownExecutionAllowed || policy.Confirmation != "I_UNDERSTAND_POWER_OFF" {
		m.emit(targetID, "critical", "shutdown_dry_run", message+"，当前仅演练，不会关闭系统")
		return
	}
	m.emit(targetID, "critical", "shutdown_scheduled", fmt.Sprintf("%s，%d 秒后再次确认并关闭系统", message, policy.CountdownSeconds))
	go func() {
		timer := time.NewTimer(time.Duration(policy.CountdownSeconds) * time.Second)
		defer timer.Stop()
		<-timer.C
		current := m.statusFor(targetID)
		currentPolicy := m.cfg.Get().Shutdown
		currentState := m.targetState(targetID)
		if !currentState.ShutdownRequested.Load() || !contains(current.StatusFlags, "OB") || !currentPolicy.Enabled || currentPolicy.DryRun {
			m.emit(targetID, "info", "shutdown_cancelled", "关机倒计时已取消：市电恢复、策略关闭或管理员取消")
			return
		}
		m.emit(targetID, "critical", "shutdown_started", "关机倒计时结束，正在关闭系统")
		var command *exec.Cmd
		if policy.Command == "/sbin/shutdown -h now" {
			command = exec.Command("/sbin/shutdown", "-h", "now")
		} else {
			command = exec.Command("/sbin/poweroff")
		}
		if err := command.Run(); err != nil {
			m.emit(targetID, "critical", "shutdown_failed", "系统关机命令执行失败："+err.Error())
		}
	}()
}

func (m *Monitor) pollTarget(config Config, target NUTTarget, now time.Time) error {
	client := NutClient{Host: target.Host, Port: target.Port, Timeout: 4 * time.Second, Username: target.Username, Password: target.Password}
	upsList, err := client.ListUPS()
	if err != nil {
		return err
	}
	upsName := target.UPSName
	if upsName == "" && len(upsList) > 0 {
		upsName = upsList[0].Name
	}
	found := false
	for _, ups := range upsList {
		if ups.Name == upsName {
			found = true
			break
		}
	}
	if len(upsList) == 0 {
		return fmt.Errorf("NUT 服务未返回任何 UPS")
	}
	if !found {
		return fmt.Errorf("配置的 UPS '%s' 不存在", upsName)
	}
	values, err := client.Vars(upsName)
	if err != nil {
		return err
	}
	if len(values) == 0 {
		return fmt.Errorf("未读取到 UPS 变量")
	}
	current := normalize(upsName, upsList, values)
	current.TS = now.Unix()
	current.TargetID = target.ID
	current.TargetName = target.Name
	previous := m.statusFor(target.ID)
	state := m.targetState(target.ID)
	current = filterKnownUPSQuirks(current, state, now.Unix())
	m.transitions(target.ID, state, current, previous)
	m.set(current)
	if state.WasConnected != nil && !*state.WasConnected {
		m.emit(target.ID, "info", "recovered", target.Name+"：UPS 通信已恢复")
	}
	connected := true
	state.WasConnected = &connected
	if now.Unix()-state.LastHistory >= int64(config.HistoryInterval) {
		if err := m.store.AddHistory(current); err != nil {
			m.setStorageError(err)
			log.Printf("storage error: %v", err)
		} else {
			state.LastHistory = now.Unix()
			m.mu.Lock()
			m.lastHistoryWriteAt = now.Unix()
			m.lastStorageError = ""
			m.mu.Unlock()
		}
	}
	m.evaluateRules(target.ID, state, current, now.Unix(), config.AlertRules)
	if config.SelfTest.Enabled {
		if state.LastSelfTest == 0 {
			state.LastSelfTest = now.Unix()
		} else if now.Unix()-state.LastSelfTest >= int64(config.SelfTest.IntervalDays)*86400 {
			state.LastSelfTest = now.Unix()
			commands, err := client.Commands(upsName)
			if err != nil {
				m.emit(target.ID, "warning", "self_test_failed", target.Name+"：计划 UPS 自检能力检测失败："+err.Error())
			} else if !contains(commands, config.SelfTest.Command) {
				m.emit(target.ID, "warning", "self_test_failed", target.Name+"：设备未报告支持计划自检命令："+config.SelfTest.Command)
			} else if err := client.InstantCommand(upsName, config.SelfTest.Command); err != nil {
				m.emit(target.ID, "warning", "self_test_failed", target.Name+"：计划 UPS 自检启动失败："+err.Error())
			} else {
				m.emit(target.ID, "info", "self_test", target.Name+"：已启动计划 UPS 自检："+config.SelfTest.Command)
			}
		}
	}
	m.evaluateShutdown(target.ID, state, current, now.Unix(), config.Shutdown)
	if config.MQTT.Enabled {
		if err := publishMQTT(config.MQTT, current); err != nil {
			m.mu.Lock()
			m.lastMQTTError = err.Error()
			m.mu.Unlock()
		} else {
			m.mu.Lock()
			m.lastMQTTError = ""
			m.mu.Unlock()
		}
	}
	return nil
}

func (m *Monitor) statusFor(targetID string) Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.latest[targetID]
}

func (m *Monitor) pollOnce() error {
	config := m.cfg.Get()
	targets := config.EffectiveTargets()
	now := time.Now()
	m.mu.Lock()
	m.lastPollAt = now.Unix()
	m.targetOrder = m.targetOrder[:0]
	m.mu.Unlock()
	if len(targets) == 0 {
		return fmt.Errorf("没有启用的 NUT 目标")
	}
	errorsByTarget := []string{}
	for _, target := range targets {
		m.mu.Lock()
		m.targetOrder = append(m.targetOrder, target.ID)
		m.mu.Unlock()
		if err := m.pollTarget(config, target, now); err != nil {
			state := m.targetState(target.ID)
			previous := m.statusFor(target.ID)
			m.set(Status{TargetID: target.ID, TargetName: target.Name, Connected: false, TS: now.Unix(), Error: err.Error(), UPSList: previous.UPSList})
			if state.WasConnected == nil || *state.WasConnected {
				m.emit(target.ID, "critical", "connection_lost", target.Name+"：无法读取 UPS："+err.Error())
			}
			connected := false
			state.WasConnected = &connected
			errorsByTarget = append(errorsByTarget, target.Name+": "+err.Error())
		}
	}
	if now.Unix()-m.targetState("_global").LastCleanup > 21600 {
		if err := m.store.Cleanup(config.RetentionDays); err != nil {
			m.setStorageError(err)
		} else {
			m.targetState("_global").LastCleanup = now.Unix()
		}
	}
	m.mu.Lock()
	if len(errorsByTarget) == 0 {
		m.lastSuccessAt = now.Unix()
		m.lastError = ""
	} else {
		m.lastError = strings.Join(errorsByTarget, "; ")
	}
	m.mu.Unlock()
	if len(errorsByTarget) > 0 {
		return fmt.Errorf("%s", strings.Join(errorsByTarget, "; "))
	}
	return nil
}

func (m *Monitor) ShutdownPlan() ShutdownPlan {
	policy := m.cfg.Get().Shutdown
	return ShutdownPlan{
		Enabled: policy.Enabled, DryRun: policy.DryRun, ExecutionGateEnabled: m.shutdownExecutionAllowed,
		Triggers: []string{fmt.Sprintf("电池供电 ≥ %d 秒", policy.OnBatterySeconds), fmt.Sprintf("电量 ≤ %d%%", policy.ChargeBelow), fmt.Sprintf("续航 ≤ %d 秒", policy.RuntimeBelow)},
		Steps:    []string{"检测持续断电并过滤瞬时波动", "持久化 critical 事件并发送通知", fmt.Sprintf("等待 %d 秒安全倒计时", policy.CountdownSeconds), "再次确认市电尚未恢复且策略仍启用", "演练模式只记录结果；执行模式调用受限系统关机命令"},
	}
}

func (m *Monitor) CancelShutdown(targetID string) bool {
	state := m.targetState(targetID)
	if !state.ShutdownRequested.Load() {
		return false
	}
	state.ShutdownRequested.Store(false)
	m.emit(targetID, "info", "shutdown_cancelled", "管理员已取消待执行的关机计划")
	return true
}

func (m *Monitor) Run(ctx context.Context) {
	go m.notificationWorker(ctx)
	for {
		_ = m.pollOnce()
		timer := time.NewTimer(time.Duration(m.cfg.Get().PollInterval) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func sendWebhook(config Config, event Event) error {
	body, err := json.Marshal(map[string]any{"source": "fnOS UPS Monitor", "event": event})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(config.WebhookTimeout)*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, config.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "fnos-ups-monitor/"+version)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Webhook 返回 HTTP %d", response.StatusCode)
	}
	return nil
}
