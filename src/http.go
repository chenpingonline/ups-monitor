package main

import (
	"bytes"
	"crypto/subtle"
	"embed"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

const gatewayPrefix = "/app/fnos-ups-monitor"

//go:embed static/index.html static/ups-device.png static/ups-device-dark.png static/tabler-icons.svg
var assets embed.FS

type App struct {
	cfg         *ConfigStore
	store       *Store
	mon         *Monitor
	allowUnauth bool
}

func (a *App) apiAuthorized(request *http.Request) bool {
	if a.isAdmin(request) {
		return true
	}
	token := a.cfg.Get().APIToken
	if token == "" {
		return true
	}
	provided := strings.TrimSpace(strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer "))
	return len(provided) == len(token) && subtle.ConstantTimeCompare([]byte(provided), []byte(token)) == 1
}

func (a *App) isAdmin(request *http.Request) bool {
	if a.allowUnauth {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(request.Header.Get("X-Trim-Isadmin"))) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

func routePath(path string) string {
	if path == gatewayPrefix {
		return "/"
	}
	if strings.HasPrefix(path, gatewayPrefix+"/") {
		return strings.TrimPrefix(path, gatewayPrefix)
	}
	return path
}

func jsonOut(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func (a *App) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method == http.MethodGet && request.URL.Path == gatewayPrefix {
		target := gatewayPrefix + "/"
		if request.URL.RawQuery != "" {
			target += "?" + request.URL.RawQuery
		}
		http.Redirect(writer, request, target, http.StatusTemporaryRedirect)
		return
	}
	path := routePath(request.URL.Path)
	if request.Method == http.MethodGet {
		switch path {
		case "/api/status":
			status := a.mon.Get()
			jsonOut(writer, http.StatusOK, map[string]any{
				"connected": status.Connected, "ts": status.TS, "error": status.Error,
				"ups_name": status.UPSName, "ups_list": status.UPSList, "status": status.Status,
				"status_flags": status.StatusFlags, "status_text": status.StatusText,
				"charge": status.Charge, "charge_low": status.ChargeLow, "load": status.Load,
				"runtime": status.Runtime, "runtime_low": status.RuntimeLow,
				"input_voltage": status.InputVoltage, "input_voltage_nominal": status.InputVoltageNominal, "output_voltage": status.OutputVoltage,
				"battery_voltage": status.BatteryVoltage, "input_frequency": status.InputFrequency,
				"real_power": status.RealPower, "real_power_estimated": status.RealPowerEstimated, "temperature": status.Temperature,
				"input_transfer_low": status.InputTransferLow, "input_transfer_high": status.InputTransferHigh,
				"input_sensitivity": status.InputSensitivity, "beeper_status": status.BeeperStatus, "profile": status.Profile,
				"ups_model": status.UPSModel, "ups_mfr": status.UPSMfr, "ups_serial": status.UPSSerial,
				"ups_firmware": status.UPSFirmware, "driver_version": status.DriverVersion, "driver_data_version": status.DriverDataVersion,
				"battery_type": status.BatteryType, "raw": status.Raw,
				"version": version,
				"viewer":  map[string]any{"is_admin": a.isAdmin(request), "username": request.Header.Get("X-Trim-Username")},
			})
			return
		case "/api/ups":
			jsonOut(writer, http.StatusOK, map[string]any{"items": a.mon.GetAll()})
			return
		case "/api/battery-health":
			items := []map[string]any{}
			for _, status := range a.mon.GetAll() {
				score := 100
				reasons := []string{}
				history, historyErr := a.store.HistoryFor(24*90, status.TargetID)
				trend := analyzeRuntimeTrend(history, status)
				if historyErr != nil {
					trend = RuntimeTrend{State: "unavailable", Message: "历史数据读取失败，暂时无法分析续航趋势"}
				}
				if contains(status.StatusFlags, "RB") {
					score -= 60
					reasons = append(reasons, "UPS 报告需要更换电池")
				}
				if status.Connected && contains(status.StatusFlags, "OL") && status.Charge != nil && *status.Charge < 80 {
					score -= 20
					reasons = append(reasons, "市电供电时电量低于 80%")
				}
				if status.BatteryVoltage == nil {
					score -= 5
					reasons = append(reasons, "设备未报告电池电压")
				}
				if result := strings.ToLower(status.Raw["ups.test.result"]); result != "" && !strings.Contains(result, "done and passed") && !strings.Contains(result, "ok") {
					score -= 30
					reasons = append(reasons, "最近一次 UPS 自检未通过："+status.Raw["ups.test.result"])
				}
				if trend.State == "declining" {
					score -= 20
					reasons = append(reasons, "相近负载下的预计续航较早期下降超过 20%")
				} else if trend.State == "watch" {
					score -= 10
					reasons = append(reasons, "相近负载下的预计续航呈下降趋势")
				}
				if score < 0 {
					score = 0
				}
				items = append(items, map[string]any{"target_id": status.TargetID, "target_name": status.TargetName, "score": score, "reasons": reasons, "status": status.Status, "runtime_trend": trend})
			}
			jsonOut(writer, http.StatusOK, map[string]any{"items": items, "method": "基于 NUT 状态的启发式评分，不替代厂商电池检测"})
			return
		case "/api/ups/details":
			targetID := request.URL.Query().Get("target_id")
			for _, status := range a.mon.GetAll() {
				if targetID == "" || status.TargetID == targetID {
					jsonOut(writer, http.StatusOK, map[string]any{"target_id": status.TargetID, "target_name": status.TargetName, "status": status, "profile": status.Profile, "raw": status.Raw})
					return
				}
			}
			jsonOut(writer, http.StatusNotFound, map[string]string{"error": "UPS 目标不存在"})
			return
		case "/api/ups/capabilities":
			target, upsName, ok := a.targetAndUPS(request.URL.Query().Get("target_id"))
			if !ok {
				jsonOut(writer, http.StatusNotFound, map[string]string{"error": "NUT 目标或 UPS 名称不存在"})
				return
			}
			client := NutClient{Host: target.Host, Port: target.Port, Timeout: 4 * time.Second, Username: target.Username, Password: target.Password}
			commands, commandsErr := client.Commands(upsName)
			writableVars, writableErr := client.WritableVars(upsName)
			if commandsErr != nil && writableErr != nil {
				jsonOut(writer, http.StatusBadGateway, map[string]string{"error": "读取 UPS 能力失败：" + commandsErr.Error()})
				return
			}
			errorsByType := map[string]string{}
			if commandsErr != nil {
				errorsByType["commands"] = commandsErr.Error()
			}
			if writableErr != nil {
				errorsByType["writable_vars"] = writableErr.Error()
			}
			safeCommands := []string{}
			for _, command := range []string{"test.battery.start.quick", "test.battery.start.deep", "test.battery.stop"} {
				if contains(commands, command) {
					safeCommands = append(safeCommands, command)
				}
			}
			jsonOut(writer, http.StatusOK, map[string]any{"target_id": target.ID, "ups_name": upsName, "commands": commands, "writable_vars": writableVars, "safe_commands": safeCommands, "username_configured": target.Username != "", "errors": errorsByType})
			return
		case "/api/health", "/api/readiness":
			health := a.mon.Health()
			code := http.StatusOK
			if path == "/api/readiness" && !health.OK {
				code = http.StatusServiceUnavailable
			}
			jsonOut(writer, code, health)
			return
		case "/api/history":
			hours, _ := strconv.ParseFloat(request.URL.Query().Get("hours"), 64)
			if hours < 1 {
				hours = 24
			}
			if hours > 24*365 {
				hours = 24 * 365
			}
			items, err := a.store.HistoryFor(hours, request.URL.Query().Get("target_id"))
			if err != nil {
				jsonOut(writer, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			maxPoints, _ := strconv.Atoi(request.URL.Query().Get("max_points"))
			if maxPoints <= 0 {
				maxPoints = 2000
			}
			if maxPoints > 10000 {
				maxPoints = 10000
			}
			originalCount := len(items)
			items = downsampleHistory(items, maxPoints)
			jsonOut(writer, http.StatusOK, map[string]any{"items": items, "original_count": originalCount, "sampled": len(items) < originalCount})
			return
		case "/api/history.csv":
			hours, _ := strconv.ParseFloat(request.URL.Query().Get("hours"), 64)
			if hours < 1 {
				hours = 24
			}
			if hours > 24*365 {
				hours = 24 * 365
			}
			items, err := a.store.HistoryFor(hours, request.URL.Query().Get("target_id"))
			if err != nil {
				jsonOut(writer, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			writer.Header().Set("Content-Type", "text/csv; charset=utf-8")
			writer.Header().Set("Content-Disposition", `attachment; filename="ups-history.csv"`)
			csvWriter := csv.NewWriter(writer)
			_ = csvWriter.Write([]string{"timestamp", "target_id", "ups_name", "status", "charge", "load", "runtime", "input_voltage", "output_voltage", "battery_voltage", "frequency", "real_power", "temperature"})
			for _, item := range items {
				_ = csvWriter.Write(historyCSVRow(item))
			}
			csvWriter.Flush()
			return
		case "/api/reports/summary":
			days, _ := strconv.Atoi(request.URL.Query().Get("days"))
			if days < 1 {
				days = 30
			}
			if days > 365 {
				days = 365
			}
			targetID := request.URL.Query().Get("target_id")
			report, err := a.reportSummary(days, targetID)
			if err != nil {
				jsonOut(writer, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			jsonOut(writer, http.StatusOK, report)
			return
		case "/api/events":
			limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
			if limit < 1 {
				limit = 100
			}
			if limit > 500 {
				limit = 500
			}
			items, err := a.store.EventsFor(limit, request.URL.Query().Get("target_id"))
			if err != nil {
				jsonOut(writer, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			jsonOut(writer, http.StatusOK, map[string]any{"items": items})
			return
		case "/api/config":
			if !a.isAdmin(request) {
				jsonOut(writer, http.StatusForbidden, map[string]string{"error": "仅管理员可以查看设置"})
				return
			}
			jsonOut(writer, http.StatusOK, a.cfg.Get())
			return
		case "/api/shutdown/plan":
			if !a.isAdmin(request) {
				jsonOut(writer, http.StatusForbidden, map[string]string{"error": "仅管理员可以查看关机计划"})
				return
			}
			jsonOut(writer, http.StatusOK, a.mon.ShutdownPlan())
			return
		case "/metrics":
			if !a.apiAuthorized(request) {
				jsonOut(writer, http.StatusUnauthorized, map[string]string{"error": "API Token 无效"})
				return
			}
			writer.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
			writer.Header().Set("Cache-Control", "no-store")
			writeMetrics(writer, a.mon.GetAll())
			return
		case "/", "/index.html":
			contents, err := assets.ReadFile("static/index.html")
			if err != nil {
				jsonOut(writer, http.StatusInternalServerError, map[string]string{"error": "static file missing"})
				return
			}
			writer.Header().Set("Content-Type", "text/html; charset=utf-8")
			writer.Header().Set("Cache-Control", "no-cache")
			_, _ = writer.Write(contents)
			return
		case "/ups-device.png":
			contents, err := assets.ReadFile("static/ups-device.png")
			if err != nil {
				jsonOut(writer, http.StatusInternalServerError, map[string]string{"error": "static file missing"})
				return
			}
			writer.Header().Set("Content-Type", "image/png")
			writer.Header().Set("Cache-Control", "public, max-age=86400")
			_, _ = writer.Write(contents)
			return
		case "/ups-device-dark.png":
			contents, err := assets.ReadFile("static/ups-device-dark.png")
			if err != nil {
				jsonOut(writer, http.StatusInternalServerError, map[string]string{"error": "static file missing"})
				return
			}
			writer.Header().Set("Content-Type", "image/png")
			writer.Header().Set("Cache-Control", "public, max-age=86400")
			_, _ = writer.Write(contents)
			return
		case "/tabler-icons.svg":
			contents, err := assets.ReadFile("static/tabler-icons.svg")
			if err != nil {
				jsonOut(writer, http.StatusInternalServerError, map[string]string{"error": "static file missing"})
				return
			}
			writer.Header().Set("Content-Type", "image/svg+xml")
			writer.Header().Set("Cache-Control", "public, max-age=86400")
			_, _ = writer.Write(contents)
			return
		}
	}
	if request.Method == http.MethodPost {
		if !a.isAdmin(request) {
			jsonOut(writer, http.StatusForbidden, map[string]string{"error": "仅 fnOS 管理员可以修改设置"})
			return
		}
		switch path {
		case "/api/config":
			config := a.cfg.Get()
			var submitted Config
			fields, err := decodeJSONFields(request, &submitted)
			if err != nil {
				jsonOut(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			mergeConfig(&config, submitted)
			if fields["notification"] {
				config.Notification = submitted.Notification
			}
			if fields["mqtt"] {
				config.MQTT = submitted.MQTT
			}
			if fields["api_token"] {
				config.APIToken = submitted.APIToken
			}
			if fields["shutdown"] {
				config.Shutdown = submitted.Shutdown
			}
			if err := a.cfg.Save(config); err != nil {
				jsonOut(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			jsonOut(writer, http.StatusOK, map[string]any{"ok": true, "config": a.cfg.Get()})
			return
		case "/api/test":
			config := a.cfg.Get()
			var raw Config
			if err := decodeJSON(request, &raw); err != nil {
				jsonOut(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			mergeConfig(&config, raw)
			valid, err := validateConfig(config)
			if err != nil {
				jsonOut(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			upsList, err := (NutClient{Host: valid.NutHost, Port: valid.NutPort, Timeout: 4 * time.Second}).ListUPS()
			if err != nil {
				jsonOut(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
				return
			}
			jsonOut(writer, http.StatusOK, map[string]any{"ok": true, "ups": upsList})
			return
		case "/api/webhook/test":
			config := a.cfg.Get()
			if config.WebhookURL == "" {
				jsonOut(writer, http.StatusBadRequest, map[string]string{"error": "请先配置 Webhook URL"})
				return
			}
			if err := sendWebhook(config, Event{TS: time.Now().Unix(), Severity: "info", Type: "test", Message: "fnOS UPS Monitor 测试通知"}); err != nil {
				jsonOut(writer, http.StatusBadGateway, map[string]string{"error": err.Error()})
				return
			}
			jsonOut(writer, http.StatusOK, map[string]bool{"ok": true})
			return
		case "/api/ups/self-test":
			var input struct {
				TargetID     string `json:"target_id"`
				Command      string `json:"command"`
				Confirmation string `json:"confirmation"`
			}
			if err := decodeJSON(request, &input); err != nil {
				jsonOut(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			if input.TargetID == "" || input.Confirmation != input.TargetID {
				jsonOut(writer, http.StatusBadRequest, map[string]string{"error": "确认内容必须与目标 ID 完全一致"})
				return
			}
			var target *NUTTarget
			for _, item := range a.cfg.Get().EffectiveTargets() {
				if item.ID == input.TargetID {
					copy := item
					target = &copy
					break
				}
			}
			if target == nil {
				jsonOut(writer, http.StatusNotFound, map[string]string{"error": "NUT 目标不存在"})
				return
			}
			upsName := target.UPSName
			if upsName == "" {
				for _, status := range a.mon.GetAll() {
					if status.TargetID == target.ID {
						upsName = status.UPSName
						break
					}
				}
			}
			if upsName == "" {
				jsonOut(writer, http.StatusConflict, map[string]string{"error": "尚未发现目标 UPS 名称"})
				return
			}
			client := NutClient{Host: target.Host, Port: target.Port, Timeout: 4 * time.Second, Username: target.Username, Password: target.Password}
			commands, err := client.Commands(upsName)
			if err != nil {
				jsonOut(writer, http.StatusBadGateway, map[string]string{"error": "无法确认 UPS 支持的自检能力：" + err.Error()})
				return
			}
			if !contains(commands, input.Command) {
				jsonOut(writer, http.StatusConflict, map[string]string{"error": "当前 UPS 未报告支持命令：" + input.Command})
				return
			}
			if err := client.InstantCommand(upsName, input.Command); err != nil {
				jsonOut(writer, http.StatusBadGateway, map[string]string{"error": err.Error()})
				return
			}
			_, _ = a.store.AddTargetEvent(target.ID, "info", "self_test", "已执行 UPS 自检命令："+input.Command)
			jsonOut(writer, http.StatusOK, map[string]bool{"ok": true})
			return
		case "/api/shutdown/cancel":
			var input struct {
				TargetID string `json:"target_id"`
			}
			if err := decodeJSON(request, &input); err != nil {
				jsonOut(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			jsonOut(writer, http.StatusOK, map[string]bool{"cancelled": a.mon.CancelShutdown(input.TargetID)})
			return
		}
	}
	jsonOut(writer, http.StatusNotFound, map[string]string{"error": "not found"})
}

func (a *App) targetAndUPS(targetID string) (NUTTarget, string, bool) {
	targets := a.cfg.Get().EffectiveTargets()
	if targetID == "" && len(targets) > 0 {
		targetID = targets[0].ID
	}
	for _, target := range targets {
		if target.ID != targetID {
			continue
		}
		upsName := target.UPSName
		if upsName == "" {
			for _, status := range a.mon.GetAll() {
				if status.TargetID == target.ID {
					upsName = status.UPSName
					break
				}
			}
		}
		return target, upsName, upsName != ""
	}
	return NUTTarget{}, "", false
}

type RuntimeTrend struct {
	State           string   `json:"state"`
	Message         string   `json:"message"`
	ChangePercent   *float64 `json:"change_percent,omitempty"`
	BaselineSeconds *float64 `json:"baseline_seconds,omitempty"`
	RecentSeconds   *float64 `json:"recent_seconds,omitempty"`
	SampleCount     int      `json:"sample_count"`
	SpanDays        float64  `json:"span_days"`
}

func analyzeRuntimeTrend(history []HistoryItem, current Status) RuntimeTrend {
	result := RuntimeTrend{State: "collecting", Message: "正在积累相近负载且电量充足时的续航样本"}
	if current.Load == nil {
		result.Message = "设备未报告负载，无法比较相同负载下的续航"
		return result
	}
	samples := make([]HistoryItem, 0, len(history))
	for _, item := range history {
		if item.Load == nil || item.Runtime == nil || item.Charge == nil || *item.Runtime <= 0 || *item.Charge < 90 {
			continue
		}
		if math.Abs(*item.Load-*current.Load) > 5 {
			continue
		}
		samples = append(samples, item)
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i].TS < samples[j].TS })
	result.SampleCount = len(samples)
	if len(samples) > 1 {
		result.SpanDays = float64(samples[len(samples)-1].TS-samples[0].TS) / 86400
	}
	if len(samples) < 20 || result.SpanDays < 7 {
		result.Message = fmt.Sprintf("已积累 %d 个可比较样本，需要至少 20 个且覆盖 7 天", len(samples))
		return result
	}
	window := max(5, len(samples)/5)
	baseline := medianRuntime(samples[:window])
	recent := medianRuntime(samples[len(samples)-window:])
	if baseline <= 0 {
		return result
	}
	change := (recent - baseline) / baseline * 100
	result.BaselineSeconds = &baseline
	result.RecentSeconds = &recent
	result.ChangePercent = &change
	switch {
	case change <= -20:
		result.State = "declining"
		result.Message = fmt.Sprintf("相近负载下预计续航下降 %.1f%%，建议安排电池检查", -change)
	case change <= -10:
		result.State = "watch"
		result.Message = fmt.Sprintf("相近负载下预计续航下降 %.1f%%，建议继续观察", -change)
	case change >= 10:
		result.State = "improving"
		result.Message = fmt.Sprintf("相近负载下预计续航提升 %.1f%%", change)
	default:
		result.State = "stable"
		result.Message = fmt.Sprintf("相近负载下预计续航变化 %.1f%%，整体稳定", change)
	}
	return result
}

func medianRuntime(items []HistoryItem) float64 {
	values := make([]float64, 0, len(items))
	for _, item := range items {
		if item.Runtime != nil {
			values = append(values, *item.Runtime)
		}
	}
	sort.Float64s(values)
	if len(values) == 0 {
		return 0
	}
	middle := len(values) / 2
	if len(values)%2 == 0 {
		return (values[middle-1] + values[middle]) / 2
	}
	return values[middle]
}

func downsampleHistory(items []HistoryItem, maxPoints int) []HistoryItem {
	if maxPoints < 2 || len(items) <= maxPoints {
		return items
	}
	result := make([]HistoryItem, 0, maxPoints)
	for index := 0; index < maxPoints; index++ {
		position := index * (len(items) - 1) / (maxPoints - 1)
		result = append(result, items[position])
	}
	return result
}

func floatText(value *float64) string {
	if value == nil {
		return ""
	}
	return strconv.FormatFloat(*value, 'f', -1, 64)
}

func historyCSVRow(item HistoryItem) []string {
	return []string{
		time.Unix(item.TS, 0).Format(time.RFC3339), item.TargetID, item.UPSName, item.Status,
		floatText(item.Charge), floatText(item.Load), floatText(item.Runtime), floatText(item.InputVoltage),
		floatText(item.OutputVoltage), floatText(item.BatteryVoltage), floatText(item.InputFrequency),
		floatText(item.RealPower), floatText(item.Temperature),
	}
}

func (a *App) reportSummary(days int, targetID string) (map[string]any, error) {
	history, err := a.store.HistoryFor(float64(days*24), targetID)
	if err != nil {
		return nil, err
	}
	events, err := a.store.EventsFor(100000, targetID)
	if err != nil {
		return nil, err
	}
	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour).Unix()
	sort.Slice(events, func(i, j int) bool { return events[i].TS < events[j].TS })
	onBattery := map[string]int64{}
	outageCount, totalOutage, longestOutage := 0, int64(0), int64(0)
	for _, event := range events {
		if event.TS < cutoff {
			continue
		}
		switch event.Type {
		case "on_battery":
			if onBattery[event.TargetID] == 0 {
				onBattery[event.TargetID] = event.TS
				outageCount++
			}
		case "online":
			if start := onBattery[event.TargetID]; start > 0 {
				duration := event.TS - start
				totalOutage += duration
				if duration > longestOutage {
					longestOutage = duration
				}
				delete(onBattery, event.TargetID)
			}
		}
	}
	for _, start := range onBattery {
		duration := time.Now().Unix() - start
		totalOutage += duration
		if duration > longestOutage {
			longestOutage = duration
		}
	}
	energyWh := 0.0
	for index := 1; index < len(history); index++ {
		previous, current := history[index-1], history[index]
		if previous.TargetID != current.TargetID || previous.RealPower == nil || current.RealPower == nil {
			continue
		}
		delta := current.TS - previous.TS
		if delta <= 0 || delta > 3600 {
			continue
		}
		energyWh += ((*previous.RealPower + *current.RealPower) / 2) * float64(delta) / 3600
	}
	averageDuration := int64(0)
	if outageCount > 0 {
		averageDuration = totalOutage / int64(outageCount)
	}
	return map[string]any{
		"days": days, "target_id": targetID, "outage_count": outageCount,
		"total_outage_seconds": totalOutage, "longest_outage_seconds": longestOutage,
		"average_outage_seconds": averageDuration, "estimated_energy_kwh": energyWh / 1000,
		"history_samples": len(history), "energy_note": "功率缺失或采样间隔超过一小时的区间不计入估算",
	}, nil
}

func metricLabel(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\"", "\\\"")
	return strings.ReplaceAll(value, "\n", "\\n")
}

func writeMetrics(writer io.Writer, statuses []Status) {
	_, _ = fmt.Fprintln(writer, "# HELP fnos_ups_connected Whether the UPS target is connected.")
	_, _ = fmt.Fprintln(writer, "# TYPE fnos_ups_connected gauge")
	for _, status := range statuses {
		labels := fmt.Sprintf("target_id=\"%s\",target_name=\"%s\",ups_name=\"%s\"", metricLabel(status.TargetID), metricLabel(status.TargetName), metricLabel(status.UPSName))
		connected := 0
		if status.Connected {
			connected = 1
		}
		_, _ = fmt.Fprintf(writer, "fnos_ups_connected{%s} %d\n", labels, connected)
		metrics := []struct {
			name  string
			value *float64
		}{{"battery_charge_percent", status.Charge}, {"load_percent", status.Load}, {"runtime_seconds", status.Runtime}, {"input_voltage", status.InputVoltage}, {"output_voltage", status.OutputVoltage}, {"battery_voltage", status.BatteryVoltage}, {"input_frequency_hz", status.InputFrequency}, {"real_power_watts", status.RealPower}, {"temperature_celsius", status.Temperature}}
		for _, metric := range metrics {
			if metric.value != nil {
				_, _ = fmt.Fprintf(writer, "fnos_ups_%s{%s} %g\n", metric.name, labels, *metric.value)
			}
		}
	}
}

func decodeJSON(request *http.Request, value any) error {
	_, err := decodeJSONFields(request, value)
	return err
}

func decodeJSONFields(request *http.Request, value any) (map[string]bool, error) {
	defer request.Body.Close()
	limited := io.LimitReader(request.Body, 65537)
	contents, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if len(contents) > 65536 {
		return nil, errors.New("请求体过大")
	}
	if len(bytes.TrimSpace(contents)) == 0 {
		return map[string]bool{}, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return nil, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(contents, &raw); err != nil {
		return nil, err
	}
	fields := make(map[string]bool, len(raw))
	for key := range raw {
		fields[key] = true
	}
	return fields, nil
}
