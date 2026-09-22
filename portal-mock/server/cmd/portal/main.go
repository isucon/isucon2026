package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/isucon/isucon2026/portal-mock/server/internal/config"
	"github.com/isucon/isucon2026/portal-mock/server/internal/rpc"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("invalid portal configuration", "error", err)
		os.Exit(2)
	}
	app, err := rpc.NewApplication(cfg)
	if err != nil {
		logger.Error("failed to initialize portal", "error", err)
		os.Exit(1)
	}
	defer app.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := rpc.Serve(ctx, app, logger); err != nil {
		logger.Error("portal server stopped with an error", "error", err)
		os.Exit(1)
	}
}
