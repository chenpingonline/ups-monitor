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
	store, err := OpenConfigStore(path)
	if err != nil {
		t.Fatal(err)
	}
	config := store.Get()
	if config.NutHost != "192.0.2.1" || config.UPSName != "main" {
		t.Fatalf("loaded config = %+v", config)
	}
	if config.NutPort != 3493 || config.PollInterval != 10 || config.WebhookTimeout != 5 {
		t.Fatalf("defaults not merged: %+v", config)
	}
}

func TestOpenConfigStoreRejectsAndPreservesInvalidFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	invalid := []byte(`{"nut_host":`)
	if err := os.WriteFile(path, invalid, 0640); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenConfigStore(path); err == nil {
		t.Fatal("OpenConfigStore() unexpectedly accepted invalid JSON")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != string(invalid) {
		t.Fatalf("invalid config was overwritten: %q", contents)
	}
}

func TestEffectiveTargetsMigratesLegacyAndSupportsMultipleTargets(t *testing.T) {
	legacy := defaultConfig()
	legacy.NutHost = "192.0.2.10"
	legacy.UPSName = "main"
	targets := legacy.EffectiveTargets()
	if len(targets) != 1 || targets[0].ID != "default" || targets[0].Host != "192.0.2.10" {
		t.Fatalf("legacy targets = %#v", targets)
	}
	legacy.Targets = []NUTTarget{{ID: "ups-a", Name: "机柜 A", Host: "192.0.2.11", Port: 3493, Enabled: true}, {ID: "disabled", Host: "192.0.2.12"}}
	targets = legacy.EffectiveTargets()
	if len(targets) != 1 || targets[0].ID != "ups-a" {
		t.Fatalf("multi targets = %#v", targets)
	}
}

func TestValidateConfigRejectsDuplicateTargetAndUnsafeShutdownCommand(t *testing.T) {
	config := defaultConfig()
	config.Targets = []NUTTarget{{ID: "same", Host: "localhost", Enabled: true}, {ID: "same", Host: "localhost", Enabled: true}}
	if _, err := validateConfig(config); err == nil {
		t.Fatal("duplicate target unexpectedly accepted")
	}
	config.Targets = nil
	config.Shutdown.Command = "rm -rf /"
	if _, err := validateConfig(config); err == nil {
		t.Fatal("unsafe shutdown command unexpectedly accepted")
	}
}
