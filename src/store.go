package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type HistoryItem struct {
	TargetID       string   `json:"target_id,omitempty"`
	TargetName     string   `json:"target_name,omitempty"`
	UPSName        string   `json:"ups_name,omitempty"`
	TS             int64    `json:"ts"`
	Status         string   `json:"status"`
	Charge         *float64 `json:"charge"`
	Load           *float64 `json:"load"`
	Runtime        *float64 `json:"runtime"`
	InputVoltage   *float64 `json:"input_voltage"`
	OutputVoltage  *float64 `json:"output_voltage"`
	BatteryVoltage *float64 `json:"battery_voltage"`
	InputFrequency *float64 `json:"input_frequency"`
	RealPower      *float64 `json:"real_power"`
	Temperature    *float64 `json:"temperature"`
}

type Event struct {
	ID       string `json:"id,omitempty"`
	TargetID string `json:"target_id,omitempty"`
	TS       int64  `json:"ts"`
	Severity string `json:"severity"`
	Type     string `json:"type"`
	Message  string `json:"message"`
}

type NotificationJob struct {
	ID           string              `json:"id"`
	Event        Event               `json:"event"`
	WebhookURL   string              `json:"webhook_url"`
	Timeout      int                 `json:"timeout"`
	MaxRetries   int                 `json:"max_retries"`
	RetrySeconds int                 `json:"retry_seconds"`
	Attempts     int                 `json:"attempts"`
	NextAttempt  int64               `json:"next_attempt"`
	LastError    string              `json:"last_error,omitempty"`
	Channel      NotificationChannel `json:"channel"`
}

type Store struct {
	mu          sync.Mutex
	historyPath string
	eventsPath  string
	queueDir    string
}

func NewStore(dataDir string) *Store {
	return &Store{historyPath: filepath.Join(dataDir, "history.jsonl"), eventsPath: filepath.Join(dataDir, "events.jsonl"), queueDir: filepath.Join(dataDir, "notification-queue")}
}

func OpenStore(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0750); err != nil {
		return nil, fmt.Errorf("创建数据目录: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "notification-queue"), 0750); err != nil {
		return nil, fmt.Errorf("创建通知队列目录: %w", err)
	}
	return NewStore(dataDir), nil
}

func writeJSONAtomic(path string, value any) error {
	contents, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	temporaryPath := path + ".tmp"
	if err := os.WriteFile(temporaryPath, append(contents, '\n'), 0640); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func (s *Store) EnqueueNotification(event Event, config Config) error {
	if event.ID == "" {
		return errors.New("通知事件缺少 ID")
	}
	job := NotificationJob{
		ID: event.ID, Event: event, WebhookURL: config.WebhookURL, Timeout: config.WebhookTimeout,
		MaxRetries: config.Notification.MaxRetries, RetrySeconds: config.Notification.RetrySeconds,
		NextAttempt: time.Now().Unix(), Channel: NotificationChannel{ID: "legacy-webhook", Type: "webhook", URL: config.WebhookURL, Enabled: true},
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := writeJSONAtomic(filepath.Join(s.queueDir, job.ID+".json"), job); err != nil {
		return fmt.Errorf("持久化通知任务: %w", err)
	}
	return nil
}

func (s *Store) EnqueueChannel(event Event, config Config, channel NotificationChannel) error {
	if event.ID == "" {
		return errors.New("通知事件缺少 ID")
	}
	job := NotificationJob{
		ID: event.ID + "-" + channel.ID, Event: event, WebhookURL: channel.URL, Timeout: config.WebhookTimeout,
		MaxRetries: config.Notification.MaxRetries, RetrySeconds: config.Notification.RetrySeconds,
		NextAttempt: time.Now().Unix(), Channel: channel,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := writeJSONAtomic(filepath.Join(s.queueDir, job.ID+".json"), job); err != nil {
		return fmt.Errorf("持久化通知任务: %w", err)
	}
	return nil
}

func (s *Store) PendingNotifications(now int64, limit int) ([]NotificationJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.queueDir)
	if err != nil {
		return nil, err
	}
	jobs := []NotificationJob{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		contents, err := os.ReadFile(filepath.Join(s.queueDir, entry.Name()))
		if err != nil {
			return nil, err
		}
		var job NotificationJob
		if err := json.Unmarshal(contents, &job); err != nil {
			return nil, fmt.Errorf("通知任务 %s 损坏: %w", entry.Name(), err)
		}
		if job.NextAttempt <= now {
			jobs = append(jobs, job)
		}
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].NextAttempt < jobs[j].NextAttempt })
	if limit > 0 && len(jobs) > limit {
		jobs = jobs[:limit]
	}
	return jobs, nil
}

func (s *Store) UpdateNotification(job NotificationJob) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeJSONAtomic(filepath.Join(s.queueDir, job.ID+".json"), job)
}

func (s *Store) CompleteNotification(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := os.Remove(filepath.Join(s.queueDir, id+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
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
	if err != nil {
		return err
	}
	return file.Sync()
}

func (s *Store) AddHistory(status Status) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := appendJSON(s.historyPath, HistoryItem{
		TargetID: status.TargetID, TargetName: status.TargetName, UPSName: status.UPSName,
		TS: status.TS, Status: status.Status, Charge: status.Charge, Load: status.Load,
		Runtime: status.Runtime, InputVoltage: status.InputVoltage, OutputVoltage: status.OutputVoltage,
		BatteryVoltage: status.BatteryVoltage, InputFrequency: status.InputFrequency,
		RealPower: status.RealPower, Temperature: status.Temperature,
	}); err != nil {
		return fmt.Errorf("写入历史记录: %w", err)
	}
	return nil
}

func (s *Store) AddEvent(severity, eventType, message string) (Event, error) {
	return s.AddTargetEvent("", severity, eventType, message)
}

func (s *Store) AddTargetEvent(targetID, severity, eventType, message string) (Event, error) {
	now := time.Now()
	event := Event{ID: fmt.Sprintf("%d", now.UnixNano()), TargetID: targetID, TS: now.Unix(), Severity: severity, Type: eventType, Message: message}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := appendJSON(s.eventsPath, event); err != nil {
		return event, fmt.Errorf("写入事件: %w", err)
	}
	return event, nil
}

func readJSONL[T any](path string) ([]T, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return []T{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	items := []T{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		var item T
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil {
			return nil, fmt.Errorf("%s 第 %d 行损坏: %w", filepath.Base(path), line, err)
		}
		items = append(items, item)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *Store) History(hours float64) ([]HistoryItem, error) {
	return s.HistoryFor(hours, "")
}

func (s *Store) HistoryFor(hours float64, targetID string) ([]HistoryItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := readJSONL[HistoryItem](s.historyPath)
	if err != nil {
		return nil, fmt.Errorf("读取历史记录: %w", err)
	}
	since := time.Now().Add(-time.Duration(hours * float64(time.Hour))).Unix()
	filtered := items[:0]
	for _, item := range items {
		if item.TS >= since && (targetID == "" || item.TargetID == targetID) {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

func (s *Store) Events(limit int) ([]Event, error) {
	return s.EventsFor(limit, "")
}

func (s *Store) EventsFor(limit int, targetID string) ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := readJSONL[Event](s.eventsPath)
	if err != nil {
		return nil, fmt.Errorf("读取事件: %w", err)
	}
	if limit > len(items) {
		limit = len(items)
	}
	result := make([]Event, 0, limit)
	for index := len(items) - 1; index >= 0 && len(result) < limit; index-- {
		// Events written before multi-UPS support have no target_id. They came
		// from the then-only UPS, so keep them visible when a device is selected.
		if targetID == "" || items[index].TargetID == "" || items[index].TargetID == targetID {
			result = append(result, items[index])
		}
	}
	return result, nil
}

func rewriteRecent[T any](path string, cutoff int64, timestamp func(T) int64) error {
	items, err := readJSONL[T](path)
	if err != nil {
		return err
	}
	temporaryPath := path + ".tmp"
	file, err := os.OpenFile(temporaryPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0640)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(file)
	for _, item := range items {
		if timestamp(item) >= cutoff {
			if err := encoder.Encode(item); err != nil {
				_ = file.Close()
				return err
			}
		}
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func (s *Store) Cleanup(days int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour).Unix()
	if err := rewriteRecent(s.historyPath, cutoff, func(item HistoryItem) int64 { return item.TS }); err != nil {
		return fmt.Errorf("清理历史记录: %w", err)
	}
	if err := rewriteRecent(s.eventsPath, cutoff, func(event Event) int64 { return event.TS }); err != nil {
		return fmt.Errorf("清理事件记录: %w", err)
	}
	return nil
}
