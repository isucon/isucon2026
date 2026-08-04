package store

import (
	"context"
	"fmt"

	"github.com/isucon/isucon2026/portal-mock/server/internal/domain"
)

type BenchmarkTicket struct {
	Generation uint64
	Context    context.Context
	Run        domain.BenchmarkRun
}

func (s *Store) BeginBenchmark() (BenchmarkTicket, error) {
	s.mu.Lock()
	if s.state.Benchmark.Status != domain.BenchmarkIdle {
		s.mu.Unlock()
		return BenchmarkTicket{}, fmt.Errorf("%w: benchmark is already in progress", ErrFailedPrecondition)
	}
	if !s.state.Converged || s.state.Applied == nil || s.state.Apply.State != domain.ApplyConverged {
		s.mu.Unlock()
		return BenchmarkTicket{}, fmt.Errorf("%w: desired and applied configurations are not converged", ErrFailedPrecondition)
	}
	s.nextBenchmarkSequence++
	now := s.clock.Now()
	run := domain.BenchmarkRun{
		ID:               s.id.New(),
		Sequence:         s.nextBenchmarkSequence,
		StartedAt:        now,
		MainServer:       s.state.Applied.Config.MainServer,
		MainServerRegion: s.state.Applied.Config.RegionFor(s.state.Applied.Config.MainServer),
		TargetHostname:   s.state.Applied.Calculated.TargetHostname,
	}
	s.state.Benchmark = domain.BenchmarkRuntime{
		Status:      domain.BenchmarkPending,
		Progress:    0,
		ActiveRunID: run.ID,
		StartedAt:   &now,
		LockReason:  "ベンチマーク実行中のため設定を変更できません",
	}
	state := domain.CloneState(s.state)
	ticket := BenchmarkTicket{Generation: s.generation, Context: s.genCtx, Run: run}
	s.mu.Unlock()
	s.stateHub.publish(state)
	return ticket, nil
}

func (s *Store) MarkBenchmarkRunning(generation uint64, runID string) bool {
	s.mu.Lock()
	if !s.activeBenchmarkLocked(generation, runID) {
		s.mu.Unlock()
		return false
	}
	s.state.Benchmark.Status = domain.BenchmarkRunning
	s.state.Benchmark.Progress = 1
	state := domain.CloneState(s.state)
	s.mu.Unlock()
	s.stateHub.publish(state)
	return true
}

func (s *Store) SetBenchmarkProgress(generation uint64, runID string, progress uint32) bool {
	if progress > 100 {
		progress = 100
	}
	s.mu.Lock()
	if !s.activeBenchmarkLocked(generation, runID) || s.state.Benchmark.Status != domain.BenchmarkRunning {
		s.mu.Unlock()
		return false
	}
	if progress <= s.state.Benchmark.Progress {
		s.mu.Unlock()
		return true
	}
	s.state.Benchmark.Progress = progress
	state := domain.CloneState(s.state)
	s.mu.Unlock()
	s.stateHub.publish(state)
	return true
}

func (s *Store) CompleteBenchmark(generation uint64, run domain.BenchmarkRun) bool {
	s.mu.Lock()
	if !s.activeBenchmarkLocked(generation, run.ID) {
		s.mu.Unlock()
		return false
	}
	run.CompletedAt = s.clock.Now()
	s.state.History = append([]domain.BenchmarkRun{run}, s.state.History...)
	s.state.Benchmark = domain.BenchmarkRuntime{Status: domain.BenchmarkIdle}
	s.state.Scores.HasScores = true
	s.state.Scores.LatestScore = run.Score
	if s.state.Scores.RunsCount == 0 || run.Score > s.state.Scores.BestScore {
		s.state.Scores.BestScore = run.Score
	}
	s.state.Scores.RunsCount = uint32(len(s.state.History))
	state := domain.CloneState(s.state)
	s.mu.Unlock()
	s.stateHub.publish(state)
	return true
}

func (s *Store) activeBenchmarkLocked(generation uint64, runID string) bool {
	return s.generation == generation &&
		s.state.Benchmark.Status != domain.BenchmarkIdle &&
		s.state.Benchmark.ActiveRunID == runID
}
