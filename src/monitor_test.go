package main

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestMonitorTransitionsEmitOnceAndRecover(t *testing.T) {
	directory := t.TempDir()
	config, err := OpenConfigStore(filepath.Join(directory, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(directory)
	monitor := NewMonitor(config, store)
	state := monitor.targetState("default")
	previousCharge := 50.0
	currentCharge := 20.0
	monitor.transitions("default", state,
		Status{Status: "OB LB", StatusFlags: []string{"OB", "LB"}, Charge: &currentCharge},
		Status{Status: "OL", StatusFlags: []string{"OL"}, Charge: &previousCharge},
	)
	events, err := store.Events(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("first transitions emitted %d events, want 3: %#v", len(events), events)
	}
	monitor.transitions("default", state,
		Status{Status: "OB LB", StatusFlags: []string{"OB", "LB"}, Charge: &currentCharge},
		Status{Status: "OB LB", StatusFlags: []string{"OB", "LB"}, Charge: &currentCharge},
	)
	afterDuplicate, err := store.Events(10)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(afterDuplicate); got != 3 {
		t.Fatalf("duplicate transition emitted event, total = %d", got)
	}
	monitor.transitions("default", state,
		Status{Status: "OL", StatusFlags: []string{"OL"}, Charge: &currentCharge},
		Status{Status: "OB LB", StatusFlags: []string{"OB", "LB"}, Charge: &currentCharge},
	)
	events, err = store.Events(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 || events[0].Type != "online" {
		t.Fatalf("recovery events = %#v", events)
	}
}

func TestSendWebhookReturnsHTTPFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	config := defaultConfig()
	config.WebhookURL = server.URL
	if err := sendWebhook(config, Event{Type: "test"}); err == nil {
		t.Fatal("sendWebhook() unexpectedly accepted a non-2xx response")
	}
}

func TestRuleAlertDurationRecoveryAndShutdownDryRun(t *testing.T) {
	directory := t.TempDir()
	config, err := OpenConfigStore(filepath.Join(directory, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	monitor := NewMonitor(config, store)
	state := monitor.targetState("ups-a")
	load := 95.0
	status := Status{TargetID: "ups-a", TargetName: "机柜 A", StatusFlags: []string{"OB"}, Load: &load}
	rule := AlertRule{ID: "high-load", Metric: "load", Operator: "gte", Threshold: 90, DurationSeconds: 10, RecoveryDelta: 5, Severity: "warning", Enabled: true}
	monitor.evaluateRules("ups-a", state, status, 100, []AlertRule{rule})
	monitor.evaluateRules("ups-a", state, status, 110, []AlertRule{rule})
	load = 80
	monitor.evaluateRules("ups-a", state, status, 111, []AlertRule{rule})
	monitor.evaluateShutdown("ups-a", state, Status{TargetID: "ups-a", TargetName: "机柜 A", StatusFlags: []string{"OB"}, Charge: floatPointer("5")}, 120, ShutdownPolicy{Enabled: true, DryRun: true, ChargeBelow: 10, RuntimeBelow: 300, OnBatterySeconds: 300})
	events, err := store.EventsFor(10, "ups-a")
	if err != nil {
		t.Fatal(err)
	}
	types := map[string]bool{}
	for _, event := range events {
		types[event.Type] = true
	}
	for _, expected := range []string{"rule_high-load", "rule_recovered_high-load", "shutdown_dry_run"} {
		if !types[expected] {
			t.Fatalf("missing %s in %#v", expected, events)
		}
	}
}

func startFakeNUT(t *testing.T, charge string) (string, int) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for count := 0; count < 2; count++ {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			line, _ := bufio.NewReader(connection).ReadString('\n')
			if line == "LIST UPS\n" {
				_, _ = fmt.Fprint(connection, "BEGIN LIST UPS\nUPS main \"Main UPS\"\nEND LIST UPS\n")
			}
			if line == "LIST VAR main\n" {
				_, _ = fmt.Fprintf(connection, "BEGIN LIST VAR main\nVAR main ups.status \"OL\"\nVAR main battery.charge \"%s\"\nEND LIST VAR main\n", charge)
			}
			_ = connection.Close()
		}
	}()
	address := listener.Addr().(*net.TCPAddr)
	return "127.0.0.1", address.Port
}

func TestMonitorPollsMultipleNUTTargets(t *testing.T) {
	directory := t.TempDir()
	configStore, err := OpenConfigStore(filepath.Join(directory, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	hostA, portA := startFakeNUT(t, "90")
	hostB, portB := startFakeNUT(t, "70")
	config := configStore.Get()
	config.Targets = []NUTTarget{{ID: "ups-a", Name: "A", Host: hostA, Port: portA, Enabled: true}, {ID: "ups-b", Name: "B", Host: hostB, Port: portB, Enabled: true}}
	if err := configStore.Save(config); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	monitor := NewMonitor(configStore, store)
	if err := monitor.pollOnce(); err != nil {
		t.Fatal(err)
	}
	statuses := monitor.GetAll()
	if len(statuses) != 2 || !statuses[0].Connected || !statuses[1].Connected {
		t.Fatalf("statuses = %#v", statuses)
	}
}

func TestCancelShutdownClearsPendingRequest(t *testing.T) {
	directory := t.TempDir()
	config, err := OpenConfigStore(filepath.Join(directory, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	monitor := NewMonitor(config, store)
	monitor.targetState("ups-a").ShutdownRequested.Store(true)
	if !monitor.CancelShutdown("ups-a") || monitor.targetState("ups-a").ShutdownRequested.Load() {
		t.Fatal("shutdown was not cancelled")
	}
}

func TestPersistentNotificationRetriesThenCompletes(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		attempts++
		if attempts == 1 {
			writer.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	directory := t.TempDir()
	configStore, err := OpenConfigStore(filepath.Join(directory, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	config := configStore.Get()
	config.WebhookURL = server.URL
	config.Notification.MaxRetries = 2
	config.Notification.RetrySeconds = 1
	if err := configStore.Save(config); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	event, err := store.AddTargetEvent("ups-a", "warning", "test", "retry")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnqueueNotification(event, config); err != nil {
		t.Fatal(err)
	}
	monitor := NewMonitor(configStore, store)
	monitor.processNotifications()
	jobs, err := store.PendingNotifications(time.Now().Add(time.Hour).Unix(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Attempts != 1 {
		t.Fatalf("jobs after failure = %#v", jobs)
	}
	jobs[0].NextAttempt = time.Now().Unix()
	if err := store.UpdateNotification(jobs[0]); err != nil {
		t.Fatal(err)
	}
	monitor.processNotifications()
	jobs, err = store.PendingNotifications(time.Now().Add(time.Hour).Unix(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 0 || attempts != 2 {
		t.Fatalf("jobs=%#v attempts=%d", jobs, attempts)
	}
}
