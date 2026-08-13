package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/isucon/isucon2026/backend/internal/clock"
)

type VirtualOffsetSource interface {
	LoadVirtualOffset(context.Context) (time.Duration, bool, error)
}

// ClockSync keeps the worker's virtual clock aligned with the offset the API
// process persists on initialize and virtual-time advance.
type ClockSync struct {
	Source   VirtualOffsetSource
	Clock    *clock.Virtual
	Interval time.Duration
	Logger   *slog.Logger
}

func (s ClockSync) Poll(ctx context.Context) error {
	offset, found, err := s.Source.LoadVirtualOffset(ctx)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	if offset != s.Clock.Offset() {
		s.Clock.SetOffset(offset)
		s.Logger.Info("virtual clock synchronized", "operation", "sync_virtual_clock", "result", "success", "correlationId", "00000000-0000-0000-0000-000000000000", "offset", offset.String())
	}
	return nil
}

func (s ClockSync) Run(ctx context.Context) error {
	interval := s.Interval
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := s.Poll(ctx); err != nil && ctx.Err() == nil {
			s.Logger.Error("virtual clock sync failed", "operation", "sync_virtual_clock", "result", "retryable_error", "correlationId", "00000000-0000-0000-0000-000000000000")
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
