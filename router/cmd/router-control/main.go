package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/isucon/isucon2026/router/internal/control"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("Router control stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := control.LoadConfig(os.Getenv)
	if err != nil {
		return err
	}
	service, err := control.New(cfg, logger)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	// Streaming connections are closed when the process receives a termination signal.
	server := &http.Server{Addr: cfg.ListenAddress, Handler: service.Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	errorsCh := make(chan error, 1)
	go func() {
		logger.Info("Router control listening", "address", cfg.ListenAddress, "team", cfg.TeamNumber)
		errorsCh <- server.ListenAndServe()
	}()
	select {
	case err := <-errorsCh:
		return err
	case <-ctx.Done():
		// Streaming connections last until the process stops. Close them to let Router reconnect.
		return server.Close()
	}
}
