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

func main() {
	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		dataDir = "/var/apps/" + appName + "/var"
	}
	configDir := os.Getenv("CONFIG_DIR")
	if configDir == "" {
		configDir = "/var/apps/" + appName + "/etc"
	}
	socketPath := os.Getenv("SOCKET_PATH")
	if socketPath == "" {
		socketPath = "/var/apps/" + appName + "/target/ups-monitor.sock"
	}

	_ = os.MkdirAll(dataDir, 0750)
	_ = os.MkdirAll(configDir, 0750)
	_ = os.MkdirAll(filepath.Dir(socketPath), 0755)

	cfg := NewConfigStore(filepath.Join(configDir, "config.json"))
	store := NewStore(dataDir)
	monitor := NewMonitor(cfg, store)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go monitor.Run(ctx)

	_ = os.Remove(socketPath)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		log.Fatal(err)
	}
	_ = os.Chmod(socketPath, 0660)

	app := &App{cfg: cfg, store: store, mon: monitor, allowUnauth: os.Getenv("UPS_MONITOR_ALLOW_UNAUTH_ADMIN") == "1"}
	server := &http.Server{Handler: app, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	_ = os.Remove(socketPath)
}
