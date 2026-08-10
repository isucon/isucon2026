package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/isucon/isucon2026/backend/internal/clock"
	"github.com/isucon/isucon2026/backend/internal/config"
	"github.com/isucon/isucon2026/backend/internal/domain"
	isurealimage "github.com/isucon/isucon2026/backend/internal/image"
	"github.com/isucon/isucon2026/backend/internal/observability"
	isurealpush "github.com/isucon/isucon2026/backend/internal/push"
	"github.com/isucon/isucon2026/backend/internal/repository/postgres"
	"github.com/isucon/isucon2026/backend/internal/service"
	"github.com/isucon/isucon2026/backend/internal/worker"
)

func main() {
	logger := observability.New(os.Stdout, slog.LevelInfo)
	cfg, err := config.Load()
	if err != nil {
		logger.Error("invalid configuration", "operation", "worker_startup", "result", "configuration_error", "correlationId", "00000000-0000-0000-0000-000000000000")
		os.Exit(1)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	db, err := postgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("database startup failed", "operation", "worker_startup", "result", "database_error", "correlationId", "00000000-0000-0000-0000-000000000000")
		os.Exit(1)
	}
	defer db.Close()
	source := clock.NewVirtual()
	ids := domain.RandomIDGenerator{}
	sanitizer := isurealimage.NewIsolatedSanitizer(isurealimage.NewSubprocessProcess(cfg.Sanitizer.Path), cfg.Sanitizer.Concurrency, isurealimage.ResourceBudget{MemoryBytes: cfg.Sanitizer.MemoryBytes, CPUSeconds: cfg.Sanitizer.CPUSeconds, Timeout: cfg.Sanitizer.Timeout})
	images := isurealimage.NewStore(cfg.ImageRoot, sanitizer, ids)
	accounts := service.NewAccountService(postgres.NewProfileRepository(db, source), postgres.NewAccountRepository(db, source), images)
	runner := worker.Runner{DB: db, Clock: source, PollInterval: cfg.Worker.PollInterval, BatchSize: cfg.Worker.BatchSize, Handlers: map[string]worker.Handler{}, Logger: logger}
	notifications := postgres.NewNotificationRepository(db, ids, source)
	purger := worker.AccountPurgeWorker{Service: accounts, Interval: 30 * time.Second, BatchSize: cfg.Worker.BatchSize, Logger: logger}
	scheduler := worker.WindowScheduler{Repository: notifications, Interval: cfg.Worker.PollInterval, BatchSize: cfg.Worker.BatchSize, Logger: logger}
	delivery := worker.PushDelivery{Repository: notifications, Sender: isurealpush.WebPush{PublicKey: cfg.VAPID.PublicKey, PrivateKey: cfg.VAPID.PrivateKey, Subject: cfg.VAPID.Subject}, Interval: cfg.Worker.PollInterval, BatchSize: cfg.Worker.BatchSize, Logger: logger}
	results := make(chan error, 4)
	go func() { results <- runner.Run(ctx) }()
	go func() { results <- purger.Run(ctx) }()
	go func() { results <- scheduler.Run(ctx) }()
	go func() { results <- delivery.Run(ctx) }()
	err = <-results
	cancel()
	<-results
	<-results
	<-results
	if err != nil {
		logger.Error("worker stopped", "operation", "worker_shutdown", "result", "unexpected_error", "correlationId", "00000000-0000-0000-0000-000000000000")
		os.Exit(1)
	}
}
