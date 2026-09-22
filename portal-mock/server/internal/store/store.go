package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/isucon/isucon2026/portal-mock/server/internal/clock"
	"github.com/isucon/isucon2026/portal-mock/server/internal/domain"
	"github.com/isucon/isucon2026/portal-mock/server/internal/id"
)

var (
	ErrInvalidArgument    = errors.New("invalid argument")
	ErrFailedPrecondition = errors.New("failed precondition")
)

type Options struct {
	Team             domain.TeamKey
	Hostname         domain.HostnameConfig
	RouterMode       domain.RouterMode
	StandInProfile   domain.StandInProfile
	ProcessStartedAt time.Time
	ApplyTimeout     time.Duration
	Clock            clock.Clock
	ID               id.Generator
}

type Store struct {
	mu sync.RWMutex

	team         domain.TeamKey
	hostname     domain.HostnameConfig
	mode         domain.RouterMode
	profile      domain.StandInProfile
	processStart time.Time
	applyTimeout time.Duration
	clock        clock.Clock
	id           id.Generator

	state                 domain.State
	generation            uint64
	genCtx                context.Context
	genCancel             context.CancelFunc
	nextBenchmarkSequence uint64

	desiredHub *hub[domain.ConfigSnapshot]
	stateHub   *hub[domain.State]
}

func New(options Options) (*Store, error) {
	if options.Clock == nil {
		options.Clock = clock.Real{}
	}
	if options.ID == nil {
		options.ID = id.UUID{}
	}
	if options.ApplyTimeout <= 0 {
		return nil, fmt.Errorf("%w: apply timeout must be positive", ErrInvalidArgument)
	}
	if options.ProcessStartedAt.IsZero() {
		options.ProcessStartedAt = options.Clock.Now()
	}
	if err := options.Team.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	if err := options.Hostname.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	initial, err := domain.InitialState(
		options.Team,
		options.Hostname,
		options.RouterMode,
		options.StandInProfile,
		options.ProcessStartedAt,
		options.Clock.Now(),
		options.ID.New(),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	genCtx, genCancel := context.WithCancel(context.Background())
	store := &Store{
		team:         options.Team,
		hostname:     options.Hostname,
		mode:         options.RouterMode,
		profile:      options.StandInProfile,
		processStart: options.ProcessStartedAt,
		applyTimeout: options.ApplyTimeout,
		clock:        options.Clock,
		id:           options.ID,
		state:        initial,
		generation:   1,
		genCtx:       genCtx,
		genCancel:    genCancel,
	}
	store.desiredHub = newHub(initial.Desired)
	store.stateHub = newHub(domain.CloneState(initial))
	if !initial.Converged {
		store.scheduleDelayed(initial.Desired.SnapshotID, store.generation, genCtx)
	}
	return store, nil
}

func (s *Store) Close() {
	s.mu.Lock()
	s.genCancel()
	s.mu.Unlock()
}

func (s *Store) Team() domain.TeamKey { return s.team }

func (s *Store) Snapshot() domain.State {
	s.mu.Lock()
	changed := s.evaluateDelayedLocked()
	state := domain.CloneState(s.state)
	s.mu.Unlock()
	if changed {
		s.stateHub.publish(state)
	}
	return state
}

func (s *Store) Desired() domain.ConfigSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.Desired
}

func (s *Store) SubscribeDesired(ctx context.Context) Subscription[domain.ConfigSnapshot] {
	return s.desiredHub.subscribe(ctx)
}

func (s *Store) SubscribeState(ctx context.Context) Subscription[domain.State] {
	return s.stateHub.subscribe(ctx)
}

type Patch struct {
	Server1Region  *domain.Region
	Server2Region  *domain.Region
	Server3Region  *domain.Region
	MainServer     *domain.ServerID
	DefaultCountry *string
}

func (p Patch) Empty() bool {
	return p.Server1Region == nil &&
		p.Server2Region == nil &&
		p.Server3Region == nil &&
		p.MainServer == nil &&
		p.DefaultCountry == nil
}

func (s *Store) UpdateDesired(patch Patch) (bool, domain.ConfigSnapshot, domain.ApplyDiagnostic, error) {
	if patch.Server1Region != nil || patch.Server2Region != nil || patch.Server3Region != nil {
		return false, domain.ConfigSnapshot{}, domain.ApplyDiagnostic{}, fmt.Errorf("%w: server regions are fixed and cannot be configured", ErrInvalidArgument)
	}
	if patch.Empty() {
		return false, domain.ConfigSnapshot{}, domain.ApplyDiagnostic{}, fmt.Errorf("%w: patch must contain at least one field", ErrInvalidArgument)
	}

	s.mu.Lock()
	if s.state.Benchmark.Status != domain.BenchmarkIdle {
		s.mu.Unlock()
		return false, domain.ConfigSnapshot{}, domain.ApplyDiagnostic{}, fmt.Errorf("%w: benchmark is in progress", ErrFailedPrecondition)
	}
	cfg := s.state.Desired.Config
	if patch.MainServer != nil {
		if !domain.ValidServer(*patch.MainServer) {
			s.mu.Unlock()
			return false, domain.ConfigSnapshot{}, domain.ApplyDiagnostic{}, fmt.Errorf("%w: main server is invalid", ErrInvalidArgument)
		}
		cfg.MainServer = *patch.MainServer
	}
	if patch.DefaultCountry != nil {
		if err := domain.ValidateCountryCode(*patch.DefaultCountry); err != nil {
			s.mu.Unlock()
			return false, domain.ConfigSnapshot{}, domain.ApplyDiagnostic{}, fmt.Errorf("%w: default country: %v", ErrInvalidArgument, err)
		}
		cfg.DefaultCountry = *patch.DefaultCountry
	}
	if err := cfg.Validate(); err != nil {
		s.mu.Unlock()
		return false, domain.ConfigSnapshot{}, domain.ApplyDiagnostic{}, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	if cfg == s.state.Desired.Config {
		desired, apply := s.state.Desired, s.state.Apply
		s.mu.Unlock()
		return false, desired, apply, nil
	}
	calculated, err := domain.Calculate(s.team, s.hostname, cfg)
	if err != nil {
		s.mu.Unlock()
		return false, domain.ConfigSnapshot{}, domain.ApplyDiagnostic{}, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	now := s.clock.Now()
	desired := domain.ConfigSnapshot{
		SnapshotID: s.id.New(),
		Config:     cfg,
		Calculated: calculated,
		IssuedAt:   now,
	}
	s.state.Desired = desired
	s.state.Converged = false
	pendingSince := now
	s.state.Apply = domain.ApplyDiagnostic{
		State:             domain.ApplyPending,
		DesiredSnapshotID: desired.SnapshotID,
		PendingSince:      &pendingSince,
	}
	if s.state.Applied != nil {
		s.state.Apply.AppliedSnapshotID = s.state.Applied.SnapshotID
	}
	state := domain.CloneState(s.state)
	generation, genCtx := s.generation, s.genCtx
	s.mu.Unlock()

	s.desiredHub.publish(desired)
	s.stateHub.publish(state)
	s.scheduleDelayed(desired.SnapshotID, generation, genCtx)
	return true, desired, state.Apply, nil
}

func (s *Store) Reset() domain.State {
	s.mu.Lock()
	s.genCancel()
	s.generation++
	s.genCtx, s.genCancel = context.WithCancel(context.Background())
	initial, err := domain.InitialState(
		s.team,
		s.hostname,
		s.mode,
		s.profile,
		s.processStart,
		s.clock.Now(),
		s.id.New(),
	)
	if err != nil {
		panic(err)
	}
	s.state = initial
	s.nextBenchmarkSequence = 0
	state := domain.CloneState(initial)
	generation, genCtx := s.generation, s.genCtx
	s.mu.Unlock()
	s.desiredHub.publish(initial.Desired)
	s.stateHub.publish(state)
	if !initial.Converged {
		s.scheduleDelayed(initial.Desired.SnapshotID, generation, genCtx)
	}
	return state
}

func (s *Store) GenerationContext() (uint64, context.Context) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.generation, s.genCtx
}

func (s *Store) scheduleDelayed(snapshotID string, generation uint64, genCtx context.Context) {
	timer := s.clock.NewTimer(s.applyTimeout)
	go func() {
		defer timer.Stop()
		select {
		case <-genCtx.Done():
			return
		case <-timer.C():
		}
		s.mu.Lock()
		if s.generation != generation || s.state.Desired.SnapshotID != snapshotID || s.state.Converged {
			s.mu.Unlock()
			return
		}
		changed := s.evaluateDelayedLocked()
		state := domain.CloneState(s.state)
		s.mu.Unlock()
		if changed {
			s.stateHub.publish(state)
		}
	}()
}
