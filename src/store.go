package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type HistoryItem struct {
	TS             int64    `json:"ts"`
	Status         string   `json:"status"`
	Charge         *float64 `json:"charge"`
	Load           *float64 `json:"load"`
	Runtime        *float64 `json:"runtime"`
	InputVoltage   *float64 `json:"input_voltage"`
	OutputVoltage  *float64 `json:"output_voltage"`
	BatteryVoltage *float64 `json:"battery_voltage"`
	InputFrequency *float64 `json:"input_frequency"`
}

type Event struct {
	TS       int64  `json:"ts"`
	Severity string `json:"severity"`
	Type     string `json:"type"`
	Message  string `json:"message"`
}

type Store struct {
	mu          sync.Mutex
	historyPath string
	eventsPath  string
}

func NewStore(dataDir string) *Store {
	_ = os.MkdirAll(dataDir, 0750)
	return &Store{historyPath: filepath.Join(dataDir, "history.jsonl"), eventsPath: filepath.Join(dataDir, "events.jsonl")}
}

func appendJSON(path string, value any) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0640)
	if err != nil {
		return err
	}
	defer file.Close()
	contents, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = file.Write(append(contents, '\n'))
	return err
}

func (s *Store) AddHistory(status Status) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = appendJSON(s.historyPath, HistoryItem{
		TS: status.TS, Status: status.Status, Charge: status.Charge, Load: status.Load,
		Runtime: status.Runtime, InputVoltage: status.InputVoltage, OutputVoltage: status.OutputVoltage,
		BatteryVoltage: status.BatteryVoltage, InputFrequency: status.InputFrequency,
	})
}

func (s *Store) AddEvent(severity, eventType, message string) Event {
	event := Event{TS: time.Now().Unix(), Severity: severity, Type: eventType, Message: message}
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = appendJSON(s.eventsPath, event)
	return event
}

func readJSONL[T any](path string) []T {
	file, err := os.Open(path)
	if err != nil {
		return []T{}
	}
	defer file.Close()
	items := []T{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		var item T
		if json.Unmarshal(scanner.Bytes(), &item) == nil {
			items = append(items, item)
		}
	}
	return items
}

func (s *Store) History(hours float64) []HistoryItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := readJSONL[HistoryItem](s.historyPath)
	since := time.Now().Add(-time.Duration(hours * float64(time.Hour))).Unix()
	filtered := items[:0]
	for _, item := range items {
		if item.TS >= since {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func (s *Store) Events(limit int) []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := readJSONL[Event](s.eventsPath)
	if limit > len(items) {
		limit = len(items)
	}
	result := make([]Event, 0, limit)
	for index := len(items) - 1; index >= 0 && len(result) < limit; index-- {
		result = append(result, items[index])
	}
	return result
}

func rewriteRecent[T any](path string, cutoff int64, timestamp func(T) int64) {
	items := readJSONL[T](path)
	temporaryPath := path + ".tmp"
	file, err := os.OpenFile(temporaryPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0640)
	if err != nil {
		return
	}
	encoder := json.NewEncoder(file)
	for _, item := range items {
		if timestamp(item) >= cutoff {
			_ = encoder.Encode(item)
		}
	}
	_ = file.Close()
	_ = os.Rename(temporaryPath, path)
}

func (s *Store) Cleanup(days int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour).Unix()
	rewriteRecent(s.historyPath, cutoff, func(item HistoryItem) int64 { return item.TS })
	rewriteRecent(s.eventsPath, cutoff, func(event Event) int64 { return event.TS })
}
