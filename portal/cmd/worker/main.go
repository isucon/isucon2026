// cmd/worker は PostgreSQL の jobs テーブルを消費するワーカー（worker タスク相当）。ジョブ 20 本を処理する。
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/isucon/isucon2026/portal/internal/app"
	"github.com/isucon/isucon2026/portal/internal/job"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	a, err := app.New(ctx)
	if err != nil {
		return err
	}
	defer a.DB.Close()

	queue := &job.DBQueue{DB: a.DB}
	slog.Info("worker started", "poll_interval", job.DefaultPollInterval)
	queue.Run(ctx, a.Jobs, job.DefaultPollInterval)
	slog.Info("worker shutting down")
	return nil
}
