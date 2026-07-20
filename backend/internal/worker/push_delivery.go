package worker

import (
	"context"
	"github.com/isucon/isucon2026/backend/internal/domain"
	isurealpush "github.com/isucon/isucon2026/backend/internal/push"
	"log/slog"
	"time"
)

type PushAttemptStore interface {
	ClaimPush(context.Context, int) ([]domain.PushAttempt, error)
	FinishPush(context.Context, domain.PushAttempt, bool, bool, string) error
}
type PushDelivery struct {
	Repository PushAttemptStore
	Sender     isurealpush.Sender
	Interval   time.Duration
	BatchSize  int
	Logger     *slog.Logger
}

func (w PushDelivery) Poll(ctx context.Context) error {
	items, err := w.Repository.ClaimPush(ctx, w.BatchSize)
	if err != nil {
		return err
	}
	for _, item := range items {
		result := w.Sender.Send(ctx, item)
		if err := w.Repository.FinishPush(ctx, item, result.Transient, result.Permanent, result.ErrorClass); err != nil {
			return err
		}
		state := "delivered"
		if result.Transient {
			state = "retryable"
		}
		if result.Permanent {
			state = "permanent_failure"
		}
		w.Logger.Info("push delivery", "operation", "deliver_push", "result", state, "correlationId", "00000000-0000-0000-0000-000000000000", "notificationId", item.NotificationID.String())
	}
	return nil
}
func (w PushDelivery) Run(ctx context.Context) error {
	interval := w.Interval
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := w.Poll(ctx); err != nil && ctx.Err() == nil {
			w.Logger.Error("push delivery failed", "operation", "deliver_push", "result", "retryable_error", "correlationId", "00000000-0000-0000-0000-000000000000")
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
