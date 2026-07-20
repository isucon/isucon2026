package worker

import (
	"context"
	"github.com/isucon/isucon2026/backend/internal/domain"
	"log/slog"
	"time"
)

type DueWindowStarter interface {
	StartOneDue(context.Context) (domain.Region, bool, error)
}
type WindowScheduler struct {
	Repository DueWindowStarter
	Interval   time.Duration
	BatchSize  int
	Logger     *slog.Logger
}

func (w WindowScheduler) Poll(ctx context.Context) error {
	for index := 0; index < w.BatchSize; index++ {
		region, started, err := w.Repository.StartOneDue(ctx)
		if err != nil {
			return err
		}
		if !started {
			return nil
		}
		w.Logger.Info("posting window started", "operation", "start_posting_window", "result", "success", "correlationId", "00000000-0000-0000-0000-000000000000", "region", region)
	}
	return nil
}
func (w WindowScheduler) Run(ctx context.Context) error {
	interval := w.Interval
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := w.Poll(ctx); err != nil && ctx.Err() == nil {
			w.Logger.Error("window scheduler failed", "operation", "start_posting_window", "result", "retryable_error", "correlationId", "00000000-0000-0000-0000-000000000000")
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
