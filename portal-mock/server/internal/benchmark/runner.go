package benchmark

import (
	"context"
	"errors"
	"time"

	"github.com/isucon/isucon2026/portal-mock/server/internal/clock"
	"github.com/isucon/isucon2026/portal-mock/server/internal/domain"
	"github.com/isucon/isucon2026/portal-mock/server/internal/store"
)

type Runner struct {
	store       *store.Store
	clock       clock.Clock
	generator   Generator
	pendingWait time.Duration
	stepWait    time.Duration
}

func NewRunner(stateStore *store.Store, clk clock.Clock, pendingWait, stepWait time.Duration) (*Runner, error) {
	if stateStore == nil || clk == nil {
		return nil, errors.New("benchmark runner requires a store and clock")
	}
	if pendingWait <= 0 || stepWait <= 0 {
		return nil, errors.New("benchmark durations must be positive")
	}
	return &Runner{
		store:       stateStore,
		clock:       clk,
		pendingWait: pendingWait,
		stepWait:    stepWait,
	}, nil
}

func (r *Runner) Start() (domain.BenchmarkRuntime, error) {
	ticket, err := r.store.BeginBenchmark()
	if err != nil {
		return domain.BenchmarkRuntime{}, err
	}
	// Register the first timer before returning. Besides making the state
	// transition deterministic for callers using a fake clock, this avoids a
	// scheduler race where time can advance before the worker has started.
	pendingTimer := r.clock.NewTimer(r.pendingWait)
	go r.run(ticket, pendingTimer)
	return r.store.Snapshot().Benchmark, nil
}

func (r *Runner) run(ticket store.BenchmarkTicket, pendingTimer clock.Timer) {
	if !waitTimer(ticket.Context, pendingTimer) {
		return
	}
	// Install the first progress timer before publishing RUNNING. Tests and
	// stream consumers can therefore advance time immediately after observing
	// RUNNING without racing the worker scheduler.
	stepTimer := r.clock.NewTimer(r.stepWait)
	if !r.store.MarkBenchmarkRunning(ticket.Generation, ticket.Run.ID) {
		stepTimer.Stop()
		return
	}
	for _, progress := range []uint32{20, 40, 60, 80, 100} {
		if !waitTimer(ticket.Context, stepTimer) {
			return
		}
		var nextTimer clock.Timer
		if progress < 100 {
			// Publish progress only after the timer for the next step exists.
			// This makes manual-clock progression deterministic while preserving
			// the same wall-clock behavior.
			nextTimer = r.clock.NewTimer(r.stepWait)
		}
		if !r.store.SetBenchmarkProgress(ticket.Generation, ticket.Run.ID, progress) {
			if nextTimer != nil {
				nextTimer.Stop()
			}
			return
		}
		stepTimer = nextTimer
	}
	outcome := r.generator.Generate(ticket.Run.Sequence)
	ticket.Run.Result = outcome.Result
	ticket.Run.Score = outcome.Score
	ticket.Run.Logs = outcome.Logs
	r.store.CompleteBenchmark(ticket.Generation, ticket.Run)
}

func waitTimer(ctx context.Context, timer clock.Timer) bool {
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C():
		return true
	}
}
