package standin

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/isucon/isucon2026/portal-mock/server/internal/clock"
	"github.com/isucon/isucon2026/portal-mock/server/internal/domain"
	"github.com/isucon/isucon2026/portal-mock/server/internal/store"
)

type StandIn struct {
	store     *store.Store
	clock     clock.Clock
	profile   domain.StandInProfile
	delay     time.Duration
	retryWait time.Duration

	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

func New(
	stateStore *store.Store,
	clk clock.Clock,
	profile domain.StandInProfile,
	delay time.Duration,
	retryWait time.Duration,
) (*StandIn, error) {
	if stateStore == nil || clk == nil {
		return nil, errors.New("stand-in requires a store and clock")
	}
	switch profile {
	case domain.StandInProfileSuccess, domain.StandInProfileFailOnceThenSuccess, domain.StandInProfileNoReport:
	default:
		return nil, errors.New("invalid stand-in profile")
	}
	if delay <= 0 || retryWait <= 0 {
		return nil, errors.New("stand-in durations must be positive")
	}
	return &StandIn{
		store: stateStore, clock: clk, profile: profile,
		delay: delay, retryWait: retryWait, done: make(chan struct{}),
	}, nil
}

func (s *StandIn) Start(parent context.Context) {
	s.once.Do(func() {
		ctx, cancel := context.WithCancel(parent)
		s.cancel = cancel
		go s.loop(ctx)
	})
}

func (s *StandIn) Close() {
	if s.cancel != nil {
		s.cancel()
		<-s.done
	}
}

func (s *StandIn) loop(ctx context.Context) {
	defer close(s.done)
	subscription := s.store.SubscribeDesired(ctx)
	defer subscription.Cancel()
	var cancelWork context.CancelFunc
	defer func() {
		if cancelWork != nil {
			cancelWork()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case desired := <-subscription.C:
			if cancelWork != nil {
				cancelWork()
			}
			state := s.store.Snapshot()
			if state.Converged && state.Applied != nil &&
				state.Applied.SnapshotID == desired.SnapshotID &&
				state.Applied.Config == desired.Config {
				cancelWork = nil
				continue
			}
			_, generationCtx := s.store.GenerationContext()
			workCtx, cancel := context.WithCancel(generationCtx)
			cancelWork = cancel
			go s.apply(workCtx, desired)
		}
	}
}

func (s *StandIn) apply(ctx context.Context, desired domain.ConfigSnapshot) {
	if s.profile == domain.StandInProfileNoReport {
		return
	}
	if !s.wait(ctx, s.delay) {
		return
	}
	if s.profile == domain.StandInProfileFailOnceThenSuccess {
		if err := s.store.ReportFailure(desired.SnapshotID, "stand-in simulated retryable failure"); err != nil {
			return
		}
		if !s.wait(ctx, s.retryWait) {
			return
		}
	}
	_ = s.store.ReportSuccess(desired.SnapshotID, desired.Config)
}

func (s *StandIn) wait(ctx context.Context, duration time.Duration) bool {
	timer := s.clock.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C():
		return true
	}
}
