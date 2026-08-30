package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func sendHTTPNotification(timeout int, method, endpoint, contentType string, body []byte, headers map[string]string) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("User-Agent", "fnos-ups-monitor/"+version)
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("通知服务返回 HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	return nil
}

func sendNotification(job NotificationJob) error {
	channel := job.Channel
	if channel.Type == "" {
		channel = NotificationChannel{Type: "webhook", URL: job.WebhookURL}
	}
	message := job.Event.Message
	switch channel.Type {
	case "webhook":
		config := defaultConfig()
		config.WebhookURL = channel.URL
		config.WebhookTimeout = job.Timeout
		return sendWebhook(config, job.Event)
	case "ntfy":
		return sendHTTPNotification(job.Timeout, http.MethodPost, channel.URL, "text/plain; charset=utf-8", []byte(message), map[string]string{"Title": "fnOS UPS Monitor", "Priority": severityPriority(job.Event.Severity)})
	case "gotify":
		parsed, err := url.Parse(channel.URL)
		if err != nil {
			return err
		}
		query := parsed.Query()
		query.Set("token", channel.Token)
		parsed.RawQuery = query.Encode()
		body, _ := json.Marshal(map[string]any{"title": "fnOS UPS Monitor", "message": message, "priority": severityNumber(job.Event.Severity)})
		return sendHTTPNotification(job.Timeout, http.MethodPost, parsed.String(), "application/json", body, nil)
	case "telegram":
		body, _ := json.Marshal(map[string]string{"chat_id": channel.ChatID, "text": "fnOS UPS Monitor\n" + message})
		return sendHTTPNotification(job.Timeout, http.MethodPost, "https://api.telegram.org/bot"+channel.Token+"/sendMessage", "application/json", body, nil)
	case "wecom":
		body, _ := json.Marshal(map[string]any{"msgtype": "text", "text": map[string]string{"content": "fnOS UPS Monitor\n" + message}})
		return sendHTTPNotification(job.Timeout, http.MethodPost, channel.URL, "application/json", body, nil)
	case "dingtalk":
		body, _ := json.Marshal(map[string]any{"msgtype": "text", "text": map[string]string{"content": "fnOS UPS Monitor\n" + message}})
		return sendHTTPNotification(job.Timeout, http.MethodPost, channel.URL, "application/json", body, nil)
	default:
		return fmt.Errorf("不支持的通知渠道类型 %q", channel.Type)
	}
}

func severityNumber(severity string) int {
	if severity == "critical" {
		return 10
	}
	if severity == "warning" {
		return 7
	}
	return 4
}
func severityPriority(severity string) string {
	if severity == "critical" {
		return "urgent"
	}
	if severity == "warning" {
		return "high"
	}
	return "default"
}
