package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSendNotificationFormatsNtfyRequest(t *testing.T) {
	var title, body string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		title = request.Header.Get("Title")
		contents, _ := io.ReadAll(request.Body)
		body = string(contents)
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	job := NotificationJob{Timeout: 2, Event: Event{Severity: "critical", Message: "市电中断"}, Channel: NotificationChannel{Type: "ntfy", URL: server.URL}}
	if err := sendNotification(job); err != nil {
		t.Fatal(err)
	}
	if title != "fnOS UPS Monitor" || !strings.Contains(body, "市电中断") {
		t.Fatalf("title=%q body=%q", title, body)
	}
}
