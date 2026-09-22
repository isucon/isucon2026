package status

import (
	"sync"
	"time"
)

type Snapshot struct {
	Status          string    `json:"status"`
	Ready           bool      `json:"ready"`
	PortalConnected bool      `json:"portal_connected"`
	EnvoyConnected  bool      `json:"envoy_connected"`
	EnvoySynced     bool      `json:"envoy_synced"`
	DesiredSnapshot string    `json:"desired_snapshot_id,omitempty"`
	ActiveSnapshot  string    `json:"active_snapshot_id,omitempty"`
	RDSVersion      string    `json:"rds_version,omitempty"`
	Phase           string    `json:"phase"`
	LastError       string    `json:"last_error,omitempty"`
	Retry           uint64    `json:"retry"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type Store struct {
	mu    sync.RWMutex
	state Snapshot
}

func New() *Store {
	return &Store{state: Snapshot{Status: "healthy", Phase: "initial", UpdatedAt: time.Now().UTC()}}
}

func (s *Store) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

func (s *Store) SetPortalConnected(connected bool) {
	s.update(func(state *Snapshot) { state.PortalConnected = connected })
}

func (s *Store) SetEnvoyConnected(connected bool) {
	s.update(func(state *Snapshot) {
		state.EnvoyConnected = connected
		if !connected {
			state.EnvoySynced = false
		}
	})
}

func (s *Store) SetEnvoySynced(synced bool) {
	s.update(func(state *Snapshot) { state.EnvoySynced = synced })
}

func (s *Store) StartApply(snapshotID, phase string, retry uint64) {
	s.update(func(state *Snapshot) {
		state.DesiredSnapshot = snapshotID
		state.Phase = phase
		state.LastError = ""
		state.Retry = retry
	})
}

func (s *Store) SetPhase(phase string) {
	s.update(func(state *Snapshot) { state.Phase = phase })
}

func (s *Store) RecordActive(snapshotID, version string) {
	s.update(func(state *Snapshot) {
		state.DesiredSnapshot = snapshotID
		state.ActiveSnapshot = snapshotID
		state.RDSVersion = version
		state.Phase = "active"
		state.LastError = ""
		state.Retry = 0
	})
}

func (s *Store) RecordFailure(snapshotID, phase, reason string, retry uint64) {
	if len(reason) > 512 {
		reason = reason[:512]
	}
	s.update(func(state *Snapshot) {
		state.DesiredSnapshot = snapshotID
		state.Phase = phase
		state.LastError = reason
		state.Retry = retry
	})
}

func (s *Store) RecordReportError(reason string, retry uint64) {
	if len(reason) > 512 {
		reason = reason[:512]
	}
	s.update(func(state *Snapshot) {
		state.LastError = reason
		state.Retry = retry
	})
}

func (s *Store) ClearReportError() {
	s.update(func(state *Snapshot) {
		if state.ActiveSnapshot != "" && state.Phase == "active" {
			state.LastError = ""
			state.Retry = 0
		}
	})
}

func (s *Store) update(change func(*Snapshot)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	change(&s.state)
	s.state.Ready = s.state.ActiveSnapshot != "" && s.state.EnvoyConnected && s.state.EnvoySynced
	s.state.UpdatedAt = time.Now().UTC()
}
