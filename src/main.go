package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

const appName = "fnos-ups-monitor"

var version = "dev"

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func main() {
	dataDir := envOrDefault("DATA_DIR", "/var/apps/"+appName+"/var")
	configDir := envOrDefault("CONFIG_DIR", "/var/apps/"+appName+"/etc")
	socketPath := envOrDefault("SOCKET_PATH", "/var/apps/"+appName+"/target/ups-monitor.sock")
	if err := os.MkdirAll(filepath.Dir(socketPath), 0755); err != nil {
		log.Fatal(err)
	}
	configStore, err := OpenConfigStore(filepath.Join(configDir, "config.json"))
	if err != nil {
		log.Fatal(err)
	}
	dataStore, err := OpenStore(dataDir)
	if err != nil {
		log.Fatal(err)
	}
	monitor := NewMonitor(configStore, dataStore)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go monitor.Run(ctx)

	_ = os.Remove(socketPath)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.Chmod(socketPath, 0660); err != nil {
		_ = listener.Close()
		log.Fatal(err)
	}
	app := &App{cfg: configStore, store: dataStore, mon: monitor, allowUnauth: os.Getenv("UPS_MONITOR_ALLOW_UNAUTH_ADMIN") == "1"}
	server := &http.Server{Handler: app, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = server.Shutdown(shutdownContext)
	}()
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	_ = os.Remove(socketPath)
}
