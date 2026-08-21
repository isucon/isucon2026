package worker_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/isucon/isucon2026/backend/internal/clock"
	"github.com/isucon/isucon2026/backend/internal/worker"
)

type fakeOffsetSource struct {
	offset time.Duration
	found  bool
	err    error
}

func (f fakeOffsetSource) LoadVirtualOffset(context.Context) (time.Duration, bool, error) {
	return f.offset, f.found, f.err
}

func TestClockSyncAppliesStoredOffset(t *testing.T) {
	clk := clock.NewVirtual()
	sync := worker.ClockSync{
		Source: fakeOffsetSource{offset: 48 * time.Hour, found: true},
		Clock:  clk,
		Logger: slog.New(slog.DiscardHandler),
	}

	if err := sync.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := clk.Offset(); got != 48*time.Hour {
		t.Fatalf("Offset() = %v, want %v", got, 48*time.Hour)
	}
}

func TestClockSyncKeepsOffsetWhenUnset(t *testing.T) {
	clk := clock.NewVirtual()
	clk.SetOffset(time.Hour)
	sync := worker.ClockSync{
		Source: fakeOffsetSource{},
		Clock:  clk,
		Logger: slog.New(slog.DiscardHandler),
	}

	if err := sync.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := clk.Offset(); got != time.Hour {
		t.Fatalf("Offset() = %v, want %v", got, time.Hour)
	}
}

func TestClockSyncReturnsSourceError(t *testing.T) {
	clk := clock.NewVirtual()
	wantErr := errors.New("database down")
	sync := worker.ClockSync{
		Source: fakeOffsetSource{err: wantErr},
		Clock:  clk,
		Logger: slog.New(slog.DiscardHandler),
	}

	if err := sync.Poll(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("Poll() error = %v, want %v", err, wantErr)
	}
	if got := clk.Offset(); got != 0 {
		t.Fatalf("Offset() = %v, want 0", got)
	}
}
