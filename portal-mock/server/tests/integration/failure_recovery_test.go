package integration

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	portalv1 "github.com/isucon/isucon2026/gen/go/portal/v1"
	portalmockv1 "github.com/isucon/isucon2026/gen/go/portal_mock/v1"
	"github.com/isucon/isucon2026/portal-mock/server/internal/config"
	"github.com/isucon/isucon2026/portal-mock/server/internal/domain"
	"github.com/isucon/isucon2026/portal-mock/server/tests/testkit"
)

func TestInvalidReportResetAndBenchmarkCancellationRecovery(t *testing.T) {
	server := testkit.NewServer(t, func(cfg *config.Config) {
		cfg.RouterMode = config.RouterModeExternalRouter
		cfg.StandInProfile = ""
	})
	invalid := testkit.ConfigProto(server.App.Store.Snapshot().Desired.Config)
	invalid.DefaultCountry = "XX"
	_, err := server.Router.ReportApplied(context.Background(), connect.NewRequest(&portalv1.ReportAppliedRequest{
		Team: &portalv1.TeamKey{TeamNumber: 1},
		Result: &portalv1.ReportAppliedRequest_Success{Success: &portalv1.ApplySuccess{
			SnapshotId: "invalid", Applied: invalid,
		}},
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("invalid report: %v", err)
	}
	reset, err := server.Mock.Reset(context.Background(), connect.NewRequest(&portalmockv1.ResetRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if reset.Msg.State.Portal.Apply.State != portalmockv1.ApplyState_APPLY_STATE_UNSYNCED {
		t.Fatalf("reset did not clear invalid diagnostic: %+v", reset.Msg.State.Portal.Apply)
	}
	desired := server.App.Store.Snapshot().Desired
	_, err = server.Router.ReportApplied(context.Background(), connect.NewRequest(&portalv1.ReportAppliedRequest{
		Team: &portalv1.TeamKey{TeamNumber: 1},
		Result: &portalv1.ReportAppliedRequest_Success{Success: &portalv1.ApplySuccess{
			SnapshotId: desired.SnapshotID, Applied: testkit.ConfigProto(desired.Config),
		}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if server.App.Store.Snapshot().Apply.State != domain.ApplyConverged {
		t.Fatal("valid report did not recover")
	}
	_, err = server.Mock.StartBenchmark(context.Background(), connect.NewRequest(&portalmockv1.StartBenchmarkRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	server.Mock.Reset(context.Background(), connect.NewRequest(&portalmockv1.ResetRequest{}))
	server.Clock.Advance(10 * time.Second)
	time.Sleep(time.Millisecond)
	state := server.App.Store.Snapshot()
	if state.Benchmark.Status != domain.BenchmarkIdle || len(state.History) != 0 {
		t.Fatalf("canceled benchmark mutated reset state: %+v", state)
	}
}
