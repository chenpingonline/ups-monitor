package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestApp(t *testing.T) *App {
	t.Helper()
	directory := t.TempDir()
	config, err := OpenConfigStore(filepath.Join(directory, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(directory)
	monitor := NewMonitor(config, store)
	monitor.set(Status{Connected: true, TS: 123, UPSList: []UPSInfo{}, Status: "OL"})
	return &App{cfg: config, store: store, mon: monitor}
}

func TestStatusSupportsGatewayPrefixAndViewer(t *testing.T) {
	app := newTestApp(t)
	request := httptest.NewRequest(http.MethodGet, gatewayPrefix+"/api/status", nil)
	request.Header.Set("X-Trim-Isadmin", "true")
	request.Header.Set("X-Trim-Username", "admin")
	recorder := httptest.NewRecorder()
	app.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Connected bool `json:"connected"`
		Viewer    struct {
			IsAdmin  bool   `json:"is_admin"`
			Username string `json:"username"`
		} `json:"viewer"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Connected || !response.Viewer.IsAdmin || response.Viewer.Username != "admin" {
		t.Fatalf("response = %+v", response)
	}
}

func TestGatewayRootRedirectsToTrailingSlashForRelativeAssets(t *testing.T) {
	app := newTestApp(t)
	recorder := httptest.NewRecorder()
	app.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, gatewayPrefix+"?source=fnos", nil))
	if recorder.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusTemporaryRedirect)
	}
	if location := recorder.Header().Get("Location"); location != gatewayPrefix+"/?source=fnos" {
		t.Fatalf("Location = %q", location)
	}

	index := httptest.NewRecorder()
	app.ServeHTTP(index, httptest.NewRequest(http.MethodGet, gatewayPrefix+"/", nil))
	if index.Code != http.StatusOK || !strings.Contains(index.Body.String(), `src="ups-device.png?v=5"`) || !strings.Contains(index.Body.String(), `src="ups-device-dark.png?v=2"`) {
		t.Fatalf("gateway dashboard = %d", index.Code)
	}
}

func TestConfigEndpointsEnforceAdminAndPersist(t *testing.T) {
	app := newTestApp(t)
	unauthorized := httptest.NewRecorder()
	app.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	if unauthorized.Code != http.StatusForbidden {
		t.Fatalf("unauthorized status = %d", unauthorized.Code)
	}

	body := `{"nut_host":"192.0.2.10","nut_port":3493,"poll_interval":10,"history_interval":60,"retention_days":30,"low_battery_threshold":25,"webhook_timeout":5}`
	request := httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader(body))
	request.Header.Set("X-Trim-Isadmin", "1")
	recorder := httptest.NewRecorder()
	app.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if got := app.cfg.Get().NutHost; got != "192.0.2.10" {
		t.Fatalf("saved NutHost = %q", got)
	}
}

func TestDecodeJSONRejectsOversizedBody(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader(strings.Repeat("x", 65537)))
	var value map[string]any
	if err := decodeJSON(request, &value); err == nil || err.Error() != "请求体过大" {
		t.Fatalf("decodeJSON() error = %v", err)
	}
}

func TestUnknownRouteReturnsJSON404(t *testing.T) {
	app := newTestApp(t)
	recorder := httptest.NewRecorder()
	app.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/missing", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d", recorder.Code)
	}
	if contentType := recorder.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("Content-Type = %q", contentType)
	}
}

func TestUPSProductImageSupportsRootAndGatewayPrefix(t *testing.T) {
	app := newTestApp(t)
	for _, path := range []string{"/ups-device.png", gatewayPrefix + "/ups-device.png", "/ups-device-dark.png", gatewayPrefix + "/ups-device-dark.png"} {
		recorder := httptest.NewRecorder()
		app.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s status = %d", path, recorder.Code)
		}
		if contentType := recorder.Header().Get("Content-Type"); contentType != "image/png" {
			t.Fatalf("%s Content-Type = %q", path, contentType)
		}
		if body := recorder.Body.Bytes(); len(body) < 8 || string(body[:8]) != "\x89PNG\r\n\x1a\n" {
			t.Fatalf("%s did not return a PNG", path)
		}
	}
}

func TestIconSpriteSupportsRootAndGatewayPrefix(t *testing.T) {
	app := newTestApp(t)
	for _, path := range []string{"/tabler-icons.svg", gatewayPrefix + "/tabler-icons.svg"} {
		recorder := httptest.NewRecorder()
		app.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s status = %d", path, recorder.Code)
		}
		if contentType := recorder.Header().Get("Content-Type"); contentType != "image/svg+xml" {
			t.Fatalf("%s Content-Type = %q", path, contentType)
		}
		if !strings.Contains(recorder.Body.String(), "<symbol id=\"gauge\"") || !strings.Contains(recorder.Body.String(), "<symbol id=\"device-desktop\"") {
			t.Fatalf("%s did not return the icon sprite", path)
		}
	}
}

func TestHealthAndReadinessEndpoints(t *testing.T) {
	app := newTestApp(t)
	for _, path := range []string{"/api/health", "/api/readiness"} {
		recorder := httptest.NewRecorder()
		app.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s status = %d, body = %s", path, recorder.Code, recorder.Body.String())
		}
	}
	app.mon.set(Status{Connected: false, Error: "offline", UPSList: []UPSInfo{}})
	recorder := httptest.NewRecorder()
	app.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/readiness", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness status = %d, want 503", recorder.Code)
	}
}

func TestMetricsRequireConfiguredAPITokenAndExposeMultipleTargets(t *testing.T) {
	app := newTestApp(t)
	config := app.cfg.Get()
	config.APIToken = "secret-token"
	if err := app.cfg.Save(config); err != nil {
		t.Fatal(err)
	}
	app.mon.set(Status{TargetID: "ups-a", TargetName: "机柜 A", UPSName: "main", Connected: true, Charge: floatPointer("88"), UPSList: []UPSInfo{}})
	unauthorized := httptest.NewRecorder()
	app.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("metrics status = %d", unauthorized.Code)
	}
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	request.Header.Set("Authorization", "Bearer secret-token")
	recorder := httptest.NewRecorder()
	app.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `target_id="ups-a"`) || !strings.Contains(recorder.Body.String(), "fnos_ups_battery_charge_percent") {
		t.Fatalf("metrics response = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestHistoryCSVReportAndDownsampling(t *testing.T) {
	app := newTestApp(t)
	now := time.Now().Unix()
	power := 100.0
	for index := 0; index < 4; index++ {
		if err := app.store.AddHistory(Status{TargetID: "ups-a", UPSName: "main", TS: now - int64(3-index)*1200, RealPower: &power}); err != nil {
			t.Fatal(err)
		}
	}
	if err := appendJSON(app.store.eventsPath, Event{TS: now - 600, TargetID: "ups-a", Type: "on_battery"}); err != nil {
		t.Fatal(err)
	}
	if err := appendJSON(app.store.eventsPath, Event{TS: now - 300, TargetID: "ups-a", Type: "online"}); err != nil {
		t.Fatal(err)
	}
	csvRecorder := httptest.NewRecorder()
	app.ServeHTTP(csvRecorder, httptest.NewRequest(http.MethodGet, "/api/history.csv?hours=24&target_id=ups-a", nil))
	if csvRecorder.Code != http.StatusOK || !strings.Contains(csvRecorder.Body.String(), "real_power") {
		t.Fatalf("CSV = %d %s", csvRecorder.Code, csvRecorder.Body.String())
	}
	reportRecorder := httptest.NewRecorder()
	app.ServeHTTP(reportRecorder, httptest.NewRequest(http.MethodGet, "/api/reports/summary?days=1&target_id=ups-a", nil))
	if reportRecorder.Code != http.StatusOK || !strings.Contains(reportRecorder.Body.String(), `"outage_count":1`) {
		t.Fatalf("report = %d %s", reportRecorder.Code, reportRecorder.Body.String())
	}
	items := []HistoryItem{{TS: 1}, {TS: 2}, {TS: 3}, {TS: 4}, {TS: 5}}
	sampled := downsampleHistory(items, 3)
	if len(sampled) != 3 || sampled[0].TS != 1 || sampled[2].TS != 5 {
		t.Fatalf("sampled = %#v", sampled)
	}
}

func TestAnalyzeRuntimeTrendDetectsDeclineAtComparableLoad(t *testing.T) {
	load, charge := 30.0, 100.0
	history := make([]HistoryItem, 0, 25)
	for index := 0; index < 25; index++ {
		runtimeSeconds := 3300.0
		if index < 5 {
			runtimeSeconds = 3600
		} else if index >= 20 {
			runtimeSeconds = 2700
		}
		history = append(history, HistoryItem{TS: int64(index) * 10 * 86400 / 24, Load: &load, Charge: &charge, Runtime: &runtimeSeconds})
	}
	trend := analyzeRuntimeTrend(history, Status{Load: &load})
	if trend.State != "declining" || trend.ChangePercent == nil || *trend.ChangePercent != -25 {
		t.Fatalf("trend = %#v", trend)
	}
}

func TestDashboardRendersReadableDeviceResults(t *testing.T) {
	app := newTestApp(t)
	recorder := httptest.NewRecorder()
	app.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("dashboard = %d", recorder.Code)
	}
	body := recorder.Body.String()
	for _, text := range []string{"设备信息", "电池信息", "输入电源", "输出与负载", "当前功率", "市电质量", "低续航阈值", "蜂鸣器", "NUT 驱动版本", "设备能力检测", "同负载续航趋势", "输入/输出电压", "电池电压", "续航", "功率", "展开原始 NUT 数据", "近 ${esc(data.days)} 天运行报告", "NUT 拒绝了这项操作：设备要求身份验证"} {
		if !strings.Contains(body, text) {
			t.Fatalf("dashboard missing readable result marker %q", text)
		}
	}
	if strings.Contains(body, "$('dataResult').textContent=JSON.stringify") {
		t.Fatal("device results regressed to raw JSON output")
	}
	for _, marker := range []string{"openBatteryHealth()", `id="dataDialogTools"`, `class="resultGrid healthGrid"`} {
		if !strings.Contains(body, marker) {
			t.Fatalf("dashboard missing standalone battery health marker %q", marker)
		}
	}
	if strings.Contains(body, "openSettingsDialog('health')") {
		t.Fatal("battery health regressed into the settings dialog")
	}
	for _, marker := range []string{`openSettingsDialog('connection')`, `openSettingsDialog('advanced')`, `openSettingsDialog('control')`, `id="settingsDialogTitle"`, `id="controlPanel"`, "operationTarget('关机计划')", "点击自检按钮可查看配置指引"} {
		if !strings.Contains(body, marker) {
			t.Fatalf("dashboard missing settings interaction marker %q", marker)
		}
	}
	if strings.Contains(body, `class="settingsTabs"`) {
		t.Fatal("settings dialog regressed to duplicate category navigation")
	}
	for _, marker := range []string{"dialog.modal[open]{display:flex", ".modal.advancedMode .modalBody", ".modal.advancedMode #advancedConfig", "classList.toggle('advancedMode',name==='advanced')"} {
		if !strings.Contains(body, marker) {
			t.Fatalf("dashboard missing single-scroll settings marker %q", marker)
		}
	}
	if strings.Contains(body, "button.disabled=!supported||!isAdmin||!caps.username_configured") {
		t.Fatal("self-test buttons regressed to silently disabled when control credentials are missing")
	}
	for _, marker := range []string{"left+(timestamp-start)/rangeSeconds*pw", "timestamp-segment[segment.length-1].ts>maxGap", "当前时间范围内暂无连续采样数据"} {
		if !strings.Contains(body, marker) {
			t.Fatalf("dashboard missing honest history-gap marker %q", marker)
		}
	}
	if strings.Contains(body, "(item.ts-t0)/(t1-t0)*pw") {
		t.Fatal("chart regressed to stretching available samples across the selected time range")
	}
	for _, marker := range []string{`data-theme-preference="system"`, "--app-canvas:#F3F3F3", "--app-canvas:#0C0C0D", "localStorage.getItem('ups-monitor-theme')||'system'", "prefers-color-scheme: dark", `id="systemBtn"`, "themeOrder=['system','light','dark']"} {
		if !strings.Contains(body, marker) {
			t.Fatalf("dashboard missing system-theme marker %q", marker)
		}
	}
	for _, marker := range []string{"ups-monitor-theme-version", "DesktopConfig-1000", "fnos-theme-mode", "function readFnosTheme()", "userPreference?.theme", "function parentFnosTheme()", "dataset.fnosThemeSource", "function syncSystemTheme()", "systemTheme.addListener(syncSystemTheme)", "addEventListener('storage'", "setInterval(syncSystemTheme,500)", "addEventListener('focus',syncSystemTheme)", "addEventListener('pageshow',syncSystemTheme)", "visibilitychange"} {
		if !strings.Contains(body, marker) {
			t.Fatalf("dashboard missing live system-theme marker %q", marker)
		}
	}
	for _, marker := range []string{`class="productImage productImageLight"`, `class="productImage productImageDark"`, `src="ups-device-dark.png?v=2"`, ".productImageDark{display:none}", `html[data-theme="dark"] .productImageLight{display:none}`, `html[data-theme="dark"] .productImageDark{display:block}`, "background:transparent;border:0"} {
		if !strings.Contains(body, marker) {
			t.Fatalf("dashboard missing dark product-image marker %q", marker)
		}
	}
	for _, marker := range []string{`id="eventStartDate"`, `id="eventEndDate"`, `id="eventPrev"`, `id="eventNext"`, "function filteredEvents()", "function localDateInputValue(date=new Date())", "function initEventDateFilters()", "initEventDateFilters();switchWorkspace", "function changeEventPage(delta)", "eventsPath(500)", ".sort((a,b)=>(Number(b.ts)||0)-(Number(a.ts)||0))", "main.classList.toggle('eventsWorkspaceActive',name==='events')", "refresh().then(()=>{loadEvents();loadChart();loadCapabilities()})"} {
		if !strings.Contains(body, marker) {
			t.Fatalf("dashboard missing paginated events marker %q", marker)
		}
	}
	if strings.Contains(body, "查看全部") || strings.Contains(body, "openAllEvents()") {
		t.Fatal("events workspace regressed to the redundant all-events dialog")
	}
	for _, marker := range []string{"html,body{height:100%;min-height:0;overflow:hidden", ".app{height:100%;min-height:0", "overflow-y:auto;scrollbar-width:thin", ".main::-webkit-scrollbar{width:10px}", ".main::-webkit-scrollbar-track{margin-block:18px;background:transparent}", "background-clip:content-box", ".topbar{position:sticky;top:0"} {
		if !strings.Contains(body, marker) {
			t.Fatalf("dashboard missing inner-window scroll marker %q", marker)
		}
	}
	if strings.Contains(body, "scrollbar-gutter:stable") {
		t.Fatal("main scrollbar regressed to reserving a permanent outer gutter")
	}
	for _, marker := range []string{"--app-gap:8px", "padding:0 var(--app-gap) var(--app-gap)", "border-radius:16px;background:var(--app-window)", `@media(max-width:560px){:root{--app-gap:6px}.main{border-radius:12px}`} {
		if !strings.Contains(body, marker) {
			t.Fatalf("dashboard missing native canvas-gap marker %q", marker)
		}
	}
	for _, marker := range []string{".statusStateRow{display:flex;align-items:center", ".statusStateIcon{width:32px;height:32px", `id="statusStateIcon"`, `class="statusFact"`, `class="statusFactIcon"`, `tabler-icons.svg?v=1#battery`, `tabler-icons.svg?v=1#gauge`, `$('statusStateIcon').classList.toggle('error',!ok)`, "[shortModel,rating].filter(Boolean).join(' · ')"} {
		if !strings.Contains(body, marker) {
			t.Fatalf("dashboard missing reference-aligned status marker %q", marker)
		}
	}
	for _, marker := range []string{".topbar{height:64px", ".pageHeading{font-size:23px", ".upsSelect,.connBadge{height:36px", ".themeToggle{width:42px;height:36px", ".themeBtn .icon{width:19px;height:19px"} {
		if !strings.Contains(body, marker) {
			t.Fatalf("dashboard missing compact toolbar marker %q", marker)
		}
	}
	for _, marker := range []string{`role="tablist" aria-label="主要功能"`, `data-workspace-tab="status"`, `data-workspace-tab="trend"`, `data-workspace-tab="events"`, `data-workspace-tab="device"`, `data-workspace-tab="settings"`, "function switchWorkspace(name,persist=true)", "main.classList.toggle('trendWorkspaceActive',name==='trend')", ".main.trendWorkspaceActive{overflow-y:hidden}", `.dashboard[data-active-workspace="trend"] .chart{height:100%;min-height:0;display:flex;flex-direction:column`, `border:0;border-radius:0;background:transparent`, `data-workspaces="status trend"`, `data-workspaces="status events"`, `class="workspaceHub deviceWorkspace"`, `class="workspaceHub settingsWorkspace"`} {
		if !strings.Contains(body, marker) {
			t.Fatalf("dashboard missing workspace-tab marker %q", marker)
		}
	}
	for _, marker := range []string{"grid-template-rows:232px minmax(0,1fr);row-gap:10px;padding-top:4px;padding-bottom:26px", "grid-template-rows:204px minmax(0,1fr);row-gap:10px;padding-top:3px;padding-bottom:18px", "grid-template-rows:188px minmax(0,1fr);row-gap:8px;padding-top:2px;padding-bottom:12px"} {
		if !strings.Contains(body, marker) {
			t.Fatalf("dashboard missing compact status spacing marker %q", marker)
		}
	}
}
