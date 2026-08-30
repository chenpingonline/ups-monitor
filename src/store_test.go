package main

import (
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

	history := store.History(24)
	if len(history) != 1 || history[0].TS != now {
		t.Fatalf("History() = %#v", history)
	}
	events := store.Events(2)
	if len(events) != 2 || events[0].Type != "second" || events[1].Type != "first" {
		t.Fatalf("Events() = %#v", events)
	}

	store.Cleanup(1)
	allEvents := readJSONL[Event](store.eventsPath)
	if len(allEvents) != 2 {
		t.Fatalf("events after Cleanup() = %#v", allEvents)
	}
}
