package main

import (
	"os"
	"testing"
	"time"
)

func TestStoreFiltersOrdersAndCleansJSONL(t *testing.T) {
	store := NewStore(t.TempDir())
	now := time.Now().Unix()
	old := now - 48*60*60
	charge := 80.0
	if err := appendJSON(store.historyPath, HistoryItem{TS: old, Charge: &charge}); err != nil {
		t.Fatal(err)
	}
	if err := appendJSON(store.historyPath, HistoryItem{TS: now, Charge: &charge}); err != nil {
		t.Fatal(err)
	}
	if err := appendJSON(store.eventsPath, Event{TS: old, Type: "old"}); err != nil {
		t.Fatal(err)
	}
	if err := appendJSON(store.eventsPath, Event{TS: now - 1, Type: "first"}); err != nil {
		t.Fatal(err)
	}
	if err := appendJSON(store.eventsPath, Event{TS: now, Type: "second"}); err != nil {
		t.Fatal(err)
	}

	history, err := store.History(24)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].TS != now {
		t.Fatalf("History() = %#v", history)
	}
	events, err := store.Events(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Type != "second" || events[1].Type != "first" {
		t.Fatalf("Events() = %#v", events)
	}

	if err := store.Cleanup(1); err != nil {
		t.Fatal(err)
	}
	allEvents, err := readJSONL[Event](store.eventsPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(allEvents) != 2 {
		t.Fatalf("events after Cleanup() = %#v", allEvents)
	}
}

func TestStoreReportsCorruptJSONL(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := os.WriteFile(store.eventsPath, []byte("{broken\n"), 0640); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Events(10); err == nil {
		t.Fatal("Events() unexpectedly ignored corrupt JSONL")
	}
}

func TestNotificationQueuePersistsUpdatesAndCompletes(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	config := defaultConfig()
	config.WebhookURL = "https://example.test/hook"
	event := Event{ID: "job-1", TargetID: "ups-a", TS: time.Now().Unix(), Type: "test"}
	if err := store.EnqueueNotification(event, config); err != nil {
		t.Fatal(err)
	}
	jobs, err := store.PendingNotifications(time.Now().Unix(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].ID != "job-1" {
		t.Fatalf("pending jobs = %#v", jobs)
	}
	jobs[0].Attempts = 2
	jobs[0].NextAttempt = time.Now().Add(time.Hour).Unix()
	if err := store.UpdateNotification(jobs[0]); err != nil {
		t.Fatal(err)
	}
	jobs, err = store.PendingNotifications(time.Now().Unix(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 0 {
		t.Fatalf("future job was returned: %#v", jobs)
	}
	if err := store.CompleteNotification("job-1"); err != nil {
		t.Fatal(err)
	}
}
