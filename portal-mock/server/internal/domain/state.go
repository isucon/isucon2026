package domain

import "time"

type RouterMode string

const (
	RouterModeStandIn        RouterMode = "stand-in"
	RouterModeExternalRouter RouterMode = "external-router"
)

type StandInProfile string

const (
	StandInProfileNone                StandInProfile = ""
	StandInProfileSuccess             StandInProfile = "success"
	StandInProfileFailOnceThenSuccess StandInProfile = "fail-once-then-success"
	StandInProfileNoReport            StandInProfile = "no-report"
)

type ApplyState string

const (
	ApplyConverged       ApplyState = "converged"
	ApplyPending         ApplyState = "pending"
	ApplyDelayed         ApplyState = "delayed"
	ApplyFailedRetryable ApplyState = "failed-retryable"
	ApplyUnsynced        ApplyState = "unsynced"
	ApplyInvalidReport   ApplyState = "invalid-report"
)

type ConfigSnapshot struct {
	SnapshotID string
	Config     Config
	Calculated CalculatedConfig
	IssuedAt   time.Time
}

type AppliedConfig struct {
	SnapshotID string
	Config     Config
	Calculated CalculatedConfig
	ReportedAt time.Time
}

type ApplyDiagnostic struct {
	State             ApplyState
	DesiredSnapshotID string
	AppliedSnapshotID string
	Reason            string
	PendingSince      *time.Time
	Delayed           bool
}

type Runtime struct {
	RouterMode       RouterMode
	StandInProfile   StandInProfile
	ProcessStartedAt time.Time
}

type BenchmarkStatus string

const (
	BenchmarkIdle    BenchmarkStatus = "idle"
	BenchmarkPending BenchmarkStatus = "pending"
	BenchmarkRunning BenchmarkStatus = "running"
)

type BenchmarkRuntime struct {
	Status      BenchmarkStatus
	Progress    uint32
	ActiveRunID string
	StartedAt   *time.Time
	LockReason  string
}

type BenchmarkResult string

const (
	BenchmarkPass BenchmarkResult = "pass"
	BenchmarkFail BenchmarkResult = "fail"
)

type BenchmarkRun struct {
	ID               string
	Sequence         uint64
	StartedAt        time.Time
	CompletedAt      time.Time
	MainServer       ServerID
	MainServerRegion Region
	TargetHostname   string
	Score            uint64
	Result           BenchmarkResult
	Logs             []string
}

type ScoreSummary struct {
	HasScores   bool
	BestScore   uint64
	LatestScore uint64
	RunsCount   uint32
}

type State struct {
	Runtime   Runtime
	Team      TeamKey
	Desired   ConfigSnapshot
	Applied   *AppliedConfig
	Apply     ApplyDiagnostic
	Converged bool
	Benchmark BenchmarkRuntime
	Scores    ScoreSummary
	History   []BenchmarkRun
}

func InitialState(
	team TeamKey,
	host HostnameConfig,
	mode RouterMode,
	profile StandInProfile,
	processStartedAt time.Time,
	now time.Time,
	snapshotID string,
) (State, error) {
	cfg := InitialConfig()
	calculated, err := Calculate(team, host, cfg)
	if err != nil {
		return State{}, err
	}
	desired := ConfigSnapshot{
		SnapshotID: snapshotID,
		Config:     cfg,
		Calculated: calculated,
		IssuedAt:   now,
	}
	state := State{
		Runtime: Runtime{
			RouterMode:       mode,
			StandInProfile:   profile,
			ProcessStartedAt: processStartedAt,
		},
		Team:    team,
		Desired: desired,
		Benchmark: BenchmarkRuntime{
			Status: BenchmarkIdle,
		},
	}
	switch mode {
	case RouterModeStandIn:
		state.Applied = &AppliedConfig{
			SnapshotID: snapshotID,
			Config:     cfg,
			Calculated: calculated,
			ReportedAt: now,
		}
		state.Converged = true
		state.Apply = ApplyDiagnostic{
			State:             ApplyConverged,
			DesiredSnapshotID: snapshotID,
			AppliedSnapshotID: snapshotID,
		}
	case RouterModeExternalRouter:
		pendingSince := now
		state.Apply = ApplyDiagnostic{
			State:             ApplyUnsynced,
			DesiredSnapshotID: snapshotID,
			PendingSince:      &pendingSince,
		}
	default:
		return State{}, ErrInvalidRouterMode
	}
	return state, nil
}

var ErrInvalidRouterMode = stateError("invalid router mode")

type stateError string

func (e stateError) Error() string { return string(e) }

func CloneState(state State) State {
	cloned := state
	if state.Applied != nil {
		applied := *state.Applied
		cloned.Applied = &applied
	}
	if state.Apply.PendingSince != nil {
		pending := *state.Apply.PendingSince
		cloned.Apply.PendingSince = &pending
	}
	if state.Benchmark.StartedAt != nil {
		started := *state.Benchmark.StartedAt
		cloned.Benchmark.StartedAt = &started
	}
	cloned.History = append([]BenchmarkRun(nil), state.History...)
	for i := range cloned.History {
		cloned.History[i].Logs = append([]string(nil), cloned.History[i].Logs...)
	}
	return cloned
}
