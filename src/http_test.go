package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func newTestApp(t *testing.T) *App {
	t.Helper()
	directory := t.TempDir()
	config := NewConfigStore(filepath.Join(directory, "config.json"))
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
