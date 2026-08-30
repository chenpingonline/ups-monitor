package main

import (
	"path/filepath"
	"testing"
)

func TestMonitorTransitionsEmitOnceAndRecover(t *testing.T) {
	directory := t.TempDir()
	config := NewConfigStore(filepath.Join(directory, "config.json"))
	store := NewStore(directory)
	monitor := NewMonitor(config, store)
	previousCharge := 50.0
	currentCharge := 20.0
	monitor.transitions(
		Status{Status: "OB LB", StatusFlags: []string{"OB", "LB"}, Charge: &currentCharge},
		Status{Status: "OL", StatusFlags: []string{"OL"}, Charge: &previousCharge},
	)
	events := store.Events(10)
	if len(events) != 3 {
		t.Fatalf("first transitions emitted %d events, want 3: %#v", len(events), events)
	}
	monitor.transitions(
		Status{Status: "OB LB", StatusFlags: []string{"OB", "LB"}, Charge: &currentCharge},
		Status{Status: "OB LB", StatusFlags: []string{"OB", "LB"}, Charge: &currentCharge},
	)
	if got := len(store.Events(10)); got != 3 {
		t.Fatalf("duplicate transition emitted event, total = %d", got)
	}
	monitor.transitions(
		Status{Status: "OL", StatusFlags: []string{"OL"}, Charge: &currentCharge},
		Status{Status: "OB LB", StatusFlags: []string{"OB", "LB"}, Charge: &currentCharge},
	)
	events = store.Events(10)
	if len(events) != 4 || events[0].Type != "online" {
		t.Fatalf("recovery events = %#v", events)
	}
}
