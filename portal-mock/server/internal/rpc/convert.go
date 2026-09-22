package rpc

import (
	portalv1 "github.com/isucon/isucon2026/gen/go/portal/v1"
	portalmockv1 "github.com/isucon/isucon2026/gen/go/portal_mock/v1"
	"github.com/isucon/isucon2026/portal-mock/server/internal/domain"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func configFromProto(config *portalv1.Config) (domain.Config, error) {
	if config == nil || config.ServerRegions == nil {
		return domain.Config{}, &validationError{"config and server_regions are required"}
	}
	result := domain.Config{
		Server1Region:  config.ServerRegions.Server1,
		Server2Region:  config.ServerRegions.Server2,
		Server3Region:  config.ServerRegions.Server3,
		DefaultCountry: config.DefaultCountry,
	}
	if err := result.Validate(); err != nil {
		return domain.Config{}, err
	}
	return result, nil
}

func configToProto(config domain.Config) *portalv1.Config {
	return &portalv1.Config{
		ServerRegions: &portalv1.ServerRegions{
			Server1: config.Server1Region,
			Server2: config.Server2Region,
			Server3: config.Server3Region,
		},
		DefaultCountry: config.DefaultCountry,
	}
}

func routerSnapshotToProto(snapshot domain.ConfigSnapshot) *portalv1.ConfigSnapshot {
	return &portalv1.ConfigSnapshot{
		SnapshotId: snapshot.SnapshotID,
		Config:     configToProto(snapshot.Config),
		IssuedAt:   timestamppb.New(snapshot.IssuedAt),
	}
}

func calculatedToProto(calculated domain.CalculatedConfig) *portalmockv1.CalculatedConfig {
	return &portalmockv1.CalculatedConfig{
		Hostnames: &portalmockv1.Hostnames{
			App: calculated.AppHostname,
			Servers: &portalmockv1.ServerHostnames{
				Server1: calculated.Server1Hostname,
				Server2: calculated.Server2Hostname,
				Server3: calculated.Server3Hostname,
			},
			Target: calculated.TargetHostname,
		},
		DefaultCountryRegion: calculated.DefaultCountryRegion,
	}
}

func mockSnapshotToProto(snapshot domain.ConfigSnapshot) *portalmockv1.ConfigSnapshot {
	return &portalmockv1.ConfigSnapshot{
		SnapshotId: snapshot.SnapshotID,
		Config:     configToProto(snapshot.Config),
		Calculated: calculatedToProto(snapshot.Calculated),
		IssuedAt:   timestamppb.New(snapshot.IssuedAt),
	}
}

func appliedToProto(applied *domain.AppliedConfig) *portalmockv1.AppliedConfig {
	if applied == nil {
		return nil
	}
	return &portalmockv1.AppliedConfig{
		SnapshotId: applied.SnapshotID,
		Config:     configToProto(applied.Config),
		Calculated: calculatedToProto(applied.Calculated),
		ReportedAt: timestamppb.New(applied.ReportedAt),
	}
}

func diagnosticToProto(diagnostic domain.ApplyDiagnostic) *portalmockv1.ApplyDiagnostic {
	result := &portalmockv1.ApplyDiagnostic{
		State:             applyStateToProto(diagnostic.State),
		DesiredSnapshotId: diagnostic.DesiredSnapshotID,
		AppliedSnapshotId: diagnostic.AppliedSnapshotID,
		Reason:            diagnostic.Reason,
		Delayed:           diagnostic.Delayed,
	}
	if diagnostic.PendingSince != nil {
		result.PendingSince = timestamppb.New(*diagnostic.PendingSince)
	}
	return result
}

func stateToProto(state domain.State) *portalmockv1.MockPortalState {
	countries := domain.SupportedCountries()
	countryMessages := make([]*portalmockv1.SupportedCountry, 0, len(countries))
	for _, country := range countries {
		countryMessages = append(countryMessages, countryToProto(country))
	}
	history := make([]*portalmockv1.BenchmarkRun, 0, len(state.History))
	for _, run := range state.History {
		history = append(history, benchmarkRunToProto(run))
	}
	return &portalmockv1.MockPortalState{
		Runtime: &portalmockv1.MockRuntime{
			RouterMode:       routerModeToProto(state.Runtime.RouterMode),
			StandInProfile:   standInProfileToProto(state.Runtime.StandInProfile),
			ProcessStartedAt: timestamppb.New(state.Runtime.ProcessStartedAt),
		},
		Portal: &portalmockv1.PortalState{
			Team:               &portalv1.TeamKey{TeamNumber: state.Team.Number},
			Desired:            mockSnapshotToProto(state.Desired),
			Applied:            appliedToProto(state.Applied),
			Apply:              diagnosticToProto(state.Apply),
			Converged:          state.Converged,
			Locked:             state.Benchmark.Status != domain.BenchmarkIdle,
			LockReason:         state.Benchmark.LockReason,
			SupportedCountries: countryMessages,
		},
		Benchmark: benchmarkRuntimeToProto(state.Benchmark),
		Scores: &portalmockv1.ScoreSummary{
			HasScores:   state.Scores.HasScores,
			BestScore:   state.Scores.BestScore,
			LatestScore: state.Scores.LatestScore,
			RunsCount:   state.Scores.RunsCount,
		},
		History: history,
	}
}

func countryToProto(country domain.SupportedCountry) *portalmockv1.SupportedCountry {
	return &portalmockv1.SupportedCountry{
		Code: country.Code, NameJa: country.NameJA, Region: country.Region,
	}
}

func benchmarkRuntimeToProto(runtime domain.BenchmarkRuntime) *portalmockv1.BenchmarkRuntime {
	result := &portalmockv1.BenchmarkRuntime{
		Status:      benchmarkStatusToProto(runtime.Status),
		Progress:    runtime.Progress,
		ActiveRunId: runtime.ActiveRunID,
		LockReason:  runtime.LockReason,
	}
	if runtime.StartedAt != nil {
		result.StartedAt = timestamppb.New(*runtime.StartedAt)
	}
	return result
}

func benchmarkRunToProto(run domain.BenchmarkRun) *portalmockv1.BenchmarkRun {
	return &portalmockv1.BenchmarkRun{
		Id:             run.ID,
		Sequence:       run.Sequence,
		StartedAt:      timestamppb.New(run.StartedAt),
		CompletedAt:    timestamppb.New(run.CompletedAt),
		TargetHostname: run.TargetHostname,
		Score:          run.Score,
		Result:         benchmarkResultToProto(run.Result),
		Logs:           append([]string(nil), run.Logs...),
	}
}

func applyStateToProto(state domain.ApplyState) portalmockv1.ApplyState {
	switch state {
	case domain.ApplyConverged:
		return portalmockv1.ApplyState_APPLY_STATE_CONVERGED
	case domain.ApplyPending:
		return portalmockv1.ApplyState_APPLY_STATE_PENDING
	case domain.ApplyDelayed:
		return portalmockv1.ApplyState_APPLY_STATE_DELAYED
	case domain.ApplyFailedRetryable:
		return portalmockv1.ApplyState_APPLY_STATE_FAILED_RETRYABLE
	case domain.ApplyUnsynced:
		return portalmockv1.ApplyState_APPLY_STATE_UNSYNCED
	case domain.ApplyInvalidReport:
		return portalmockv1.ApplyState_APPLY_STATE_INVALID_REPORT
	default:
		return portalmockv1.ApplyState_APPLY_STATE_UNSPECIFIED
	}
}

func routerModeToProto(mode domain.RouterMode) portalmockv1.RouterMode {
	switch mode {
	case domain.RouterModeStandIn:
		return portalmockv1.RouterMode_ROUTER_MODE_STAND_IN
	case domain.RouterModeExternalRouter:
		return portalmockv1.RouterMode_ROUTER_MODE_EXTERNAL_ROUTER
	default:
		return portalmockv1.RouterMode_ROUTER_MODE_UNSPECIFIED
	}
}

func standInProfileToProto(profile domain.StandInProfile) portalmockv1.StandInProfile {
	switch profile {
	case domain.StandInProfileSuccess:
		return portalmockv1.StandInProfile_STAND_IN_PROFILE_SUCCESS
	case domain.StandInProfileFailOnceThenSuccess:
		return portalmockv1.StandInProfile_STAND_IN_PROFILE_FAIL_ONCE_THEN_SUCCESS
	case domain.StandInProfileNoReport:
		return portalmockv1.StandInProfile_STAND_IN_PROFILE_NO_REPORT
	default:
		return portalmockv1.StandInProfile_STAND_IN_PROFILE_UNSPECIFIED
	}
}

func benchmarkStatusToProto(status domain.BenchmarkStatus) portalmockv1.BenchmarkStatus {
	switch status {
	case domain.BenchmarkIdle:
		return portalmockv1.BenchmarkStatus_BENCHMARK_STATUS_IDLE
	case domain.BenchmarkPending:
		return portalmockv1.BenchmarkStatus_BENCHMARK_STATUS_PENDING
	case domain.BenchmarkRunning:
		return portalmockv1.BenchmarkStatus_BENCHMARK_STATUS_RUNNING
	default:
		return portalmockv1.BenchmarkStatus_BENCHMARK_STATUS_UNSPECIFIED
	}
}

func benchmarkResultToProto(result domain.BenchmarkResult) portalmockv1.BenchmarkResult {
	switch result {
	case domain.BenchmarkPass:
		return portalmockv1.BenchmarkResult_BENCHMARK_RESULT_PASS
	case domain.BenchmarkFail:
		return portalmockv1.BenchmarkResult_BENCHMARK_RESULT_FAIL
	default:
		return portalmockv1.BenchmarkResult_BENCHMARK_RESULT_UNSPECIFIED
	}
}

type validationError struct{ message string }

func (e *validationError) Error() string { return e.message }
