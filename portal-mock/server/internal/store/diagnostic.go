package store

import "github.com/isucon/isucon2026/portal-mock/server/internal/domain"

func (s *Store) evaluateDelayedLocked() bool {
	diagnostic := &s.state.Apply
	if s.state.Converged || diagnostic.PendingSince == nil {
		return false
	}
	delayed := s.clock.Now().Sub(*diagnostic.PendingSince) >= s.applyTimeout
	if diagnostic.Delayed == delayed {
		if diagnostic.State != domain.ApplyPending || !delayed {
			return false
		}
	}
	diagnostic.Delayed = delayed
	if delayed && (diagnostic.State == domain.ApplyPending || diagnostic.State == domain.ApplyUnsynced) {
		diagnostic.State = domain.ApplyDelayed
		return true
	}
	return delayed
}
