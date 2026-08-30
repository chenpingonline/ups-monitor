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
	for _, text := range []string{"设备信息", "电池信息", "输入电源", "输出与负载", "当前功率", "市电质量", "设备能力检测", "同负载续航趋势", "展开原始 NUT 数据", "近 ${esc(data.days)} 天运行报告", "NUT 拒绝了这项操作：设备要求身份验证"} {
		if !strings.Contains(body, text) {
			t.Fatalf("dashboard missing readable result marker %q", text)
		}
	}
	if strings.Contains(body, "$('dataResult').textContent=JSON.stringify") {
		t.Fatal("device results regressed to raw JSON output")
	}
}
