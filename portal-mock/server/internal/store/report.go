package store

import (
	"fmt"
	"strings"

	"github.com/isucon/isucon2026/portal-mock/server/internal/domain"
)

func (s *Store) ReportSuccess(snapshotID string, cfg domain.Config) error {
	if err := cfg.Validate(); err != nil {
		s.mu.Lock()
		s.state.Apply.State = domain.ApplyInvalidReport
		s.state.Apply.Reason = err.Error()
		s.state.Apply.DesiredSnapshotID = s.state.Desired.SnapshotID
		if s.state.Applied != nil {
			s.state.Apply.AppliedSnapshotID = s.state.Applied.SnapshotID
		}
		s.evaluateDelayedLocked()
		state := domain.CloneState(s.state)
		s.mu.Unlock()
		s.stateHub.publish(state)
		return fmt.Errorf("%w: applied config: %v", ErrInvalidArgument, err)
	}
	if strings.TrimSpace(snapshotID) == "" {
		return fmt.Errorf("%w: snapshot ID must not be empty", ErrInvalidArgument)
	}
	calculated, err := domain.Calculate(s.team, s.hostname, cfg)
	if err != nil {
		return fmt.Errorf("%w: applied config: %v", ErrInvalidArgument, err)
	}

	s.mu.Lock()
	now := s.clock.Now()
	s.state.Applied = &domain.AppliedConfig{
		SnapshotID: snapshotID,
		Config:     cfg,
		Calculated: calculated,
		ReportedAt: now,
	}
	s.state.Converged = cfg == s.state.Desired.Config
	s.state.Apply = domain.ApplyDiagnostic{
		DesiredSnapshotID: s.state.Desired.SnapshotID,
		AppliedSnapshotID: snapshotID,
	}
	if s.state.Converged {
		s.state.Apply.State = domain.ApplyConverged
	} else {
		pendingSince := s.state.Desired.IssuedAt
		s.state.Apply.PendingSince = &pendingSince
		s.state.Apply.State = domain.ApplyPending
		s.evaluateDelayedLocked()
	}
	state := domain.CloneState(s.state)
	s.mu.Unlock()
	s.stateHub.publish(state)
	return nil
}

func (s *Store) ReportFailure(snapshotID, reason string) error {
	if strings.TrimSpace(snapshotID) == "" {
		return fmt.Errorf("%w: snapshot ID must not be empty", ErrInvalidArgument)
	}
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("%w: failure reason must not be empty", ErrInvalidArgument)
	}
	s.mu.Lock()
	if snapshotID != s.state.Desired.SnapshotID {
		s.mu.Unlock()
		return nil
	}
	s.state.Converged = s.state.Applied != nil && s.state.Applied.Config == s.state.Desired.Config
	s.state.Apply.State = domain.ApplyFailedRetryable
	s.state.Apply.Reason = reason
	s.state.Apply.DesiredSnapshotID = s.state.Desired.SnapshotID
	if s.state.Applied != nil {
		s.state.Apply.AppliedSnapshotID = s.state.Applied.SnapshotID
	}
	pendingSince := s.state.Desired.IssuedAt
	s.state.Apply.PendingSince = &pendingSince
	s.evaluateDelayedLocked()
	state := domain.CloneState(s.state)
	s.mu.Unlock()
	s.stateHub.publish(state)
	return nil
}
