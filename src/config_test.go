package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateConfigDefaultsAndClamps(t *testing.T) {
	config, err := validateConfig(Config{NutHost: " 127.0.0.1 ", NutPort: 70000, PollInterval: 1, RetentionDays: 500})
	if err != nil {
		t.Fatalf("validateConfig() error = %v", err)
	}
	if config.NutHost != "127.0.0.1" {
		t.Fatalf("NutHost = %q", config.NutHost)
	}
	if config.NutPort != 65535 {
		t.Fatalf("NutPort = %d, want 65535", config.NutPort)
	}
	if config.PollInterval != 5 {
		t.Fatalf("PollInterval = %d, want 5", config.PollInterval)
	}
	if config.HistoryInterval != 60 {
		t.Fatalf("HistoryInterval = %d, want 60", config.HistoryInterval)
	}
	if config.RetentionDays != 365 {
		t.Fatalf("RetentionDays = %d, want 365", config.RetentionDays)
	}
	if config.LowBatteryThreshold != 25 {
		t.Fatalf("LowBatteryThreshold = %d, want 25", config.LowBatteryThreshold)
	}
}

func TestValidateConfigRejectsUnsafeValues(t *testing.T) {
	tests := []Config{
		{NutHost: "host\nother"},
		{NutHost: "localhost", UPSName: "ups name"},
		{NutHost: "localhost", WebhookURL: "file:///tmp/hook"},
	}
	for _, input := range tests {
		if _, err := validateConfig(input); err == nil {
			t.Fatalf("validateConfig(%+v) unexpectedly succeeded", input)
		}
	}
}

func TestConfigStoreLoadsLegacyFileWithDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"nut_host":"192.0.2.1","ups_name":"main"}`), 0640); err != nil {
		t.Fatal(err)
	}
	store := NewConfigStore(path)
	config := store.Get()
	if config.NutHost != "192.0.2.1" || config.UPSName != "main" {
		t.Fatalf("loaded config = %+v", config)
	}
	if config.NutPort != 3493 || config.PollInterval != 10 || config.WebhookTimeout != 5 {
		t.Fatalf("defaults not merged: %+v", config)
	}
}
