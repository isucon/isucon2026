package worker

import (
	"context"
	"github.com/google/uuid"
	"log/slog"
	"time"
)

type DueAccountPurger interface {
	PurgeDue(context.Context, int) error
}
type AccountPurgeWorker struct {
	Service   DueAccountPurger
	Interval  time.Duration
	BatchSize int
	Logger    *slog.Logger
}

func (w AccountPurgeWorker) Run(ctx context.Context) error {
	interval := w.Interval
	if interval <= 0 || interval > 30*time.Second {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := w.Service.PurgeDue(ctx, w.BatchSize); err != nil && ctx.Err() == nil {
			w.Logger.Error("account purge failed", "operation", "account_purge", "result", "retryable_error", "correlationId", uuid.Nil.String())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
