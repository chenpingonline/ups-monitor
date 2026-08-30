package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const gatewayPrefix = "/app/fnos-ups-monitor"

//go:embed static/index.html
var assets embed.FS

type App struct {
	cfg         *ConfigStore
	store       *Store
	mon         *Monitor
	allowUnauth bool
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
	path := routePath(request.URL.Path)
	if request.Method == http.MethodGet {
		switch path {
		case "/api/status":
			status := a.mon.Get()
			jsonOut(writer, http.StatusOK, map[string]any{
				"connected": status.Connected, "ts": status.TS, "error": status.Error,
				"ups_name": status.UPSName, "ups_list": status.UPSList, "status": status.Status,
				"status_flags": status.StatusFlags, "status_text": status.StatusText,
				"charge": status.Charge, "load": status.Load, "runtime": status.Runtime,
				"input_voltage": status.InputVoltage, "output_voltage": status.OutputVoltage,
				"battery_voltage": status.BatteryVoltage, "input_frequency": status.InputFrequency,
				"ups_model": status.UPSModel, "ups_mfr": status.UPSMfr, "ups_serial": status.UPSSerial,
				"battery_type": status.BatteryType, "raw": status.Raw,
				"viewer": map[string]any{"is_admin": a.isAdmin(request), "username": request.Header.Get("X-Trim-Username")},
			})
			return
		case "/api/history":
			hours, _ := strconv.ParseFloat(request.URL.Query().Get("hours"), 64)
			if hours < 1 {
				hours = 24
			}
			if hours > 168 {
				hours = 168
			}
			jsonOut(writer, http.StatusOK, map[string]any{"items": a.store.History(hours)})
			return
		case "/api/events":
			limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
			if limit < 1 {
				limit = 100
			}
			if limit > 500 {
				limit = 500
			}
			jsonOut(writer, http.StatusOK, map[string]any{"items": a.store.Events(limit)})
			return
		case "/api/config":
			if !a.isAdmin(request) {
				jsonOut(writer, http.StatusForbidden, map[string]string{"error": "仅管理员可以查看设置"})
				return
			}
			jsonOut(writer, http.StatusOK, a.cfg.Get())
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
		}
	}
	if request.Method == http.MethodPost {
		if !a.isAdmin(request) {
			jsonOut(writer, http.StatusForbidden, map[string]string{"error": "仅 fnOS 管理员可以修改设置"})
			return
		}
		switch path {
		case "/api/config":
			var config Config
			if err := decodeJSON(request, &config); err != nil {
				jsonOut(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
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
			go sendWebhook(config, Event{TS: time.Now().Unix(), Severity: "info", Type: "test", Message: "fnOS UPS Monitor 测试通知"})
			jsonOut(writer, http.StatusOK, map[string]bool{"ok": true})
			return
		}
	}
	jsonOut(writer, http.StatusNotFound, map[string]string{"error": "not found"})
}

func decodeJSON(request *http.Request, value any) error {
	defer request.Body.Close()
	limited := io.LimitReader(request.Body, 65537)
	contents, err := io.ReadAll(limited)
	if err != nil {
		return err
	}
	if len(contents) > 65536 {
		return errors.New("请求体过大")
	}
	if len(bytes.TrimSpace(contents)) == 0 {
		return nil
	}
	return json.Unmarshal(contents, value)
}
