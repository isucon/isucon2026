package contract

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	portalv1 "github.com/isucon/isucon2026/gen/go/portal/v1"
	"github.com/isucon/isucon2026/portal-mock/server/internal/config"
	"github.com/isucon/isucon2026/portal-mock/server/tests/testkit"
)

func TestRouterReportContractAndTeamSemantics(t *testing.T) {
	server := testkit.NewServer(t, func(cfg *config.Config) {
		cfg.RouterMode = config.RouterModeExternalRouter
		cfg.StandInProfile = ""
	})
	ctx := context.Background()
	reportFailure := func(team uint32) error {
		_, err := server.Router.ReportApplied(ctx, connect.NewRequest(&portalv1.ReportAppliedRequest{
			Team: &portalv1.TeamKey{TeamNumber: team},
			Result: &portalv1.ReportAppliedRequest_Failure{Failure: &portalv1.ApplyFailure{
				SnapshotId: "any", Reason: "failed",
			}},
		}))
		return err
	}
	assertCode(t, reportFailure(0), connect.CodeInvalidArgument)
	assertCode(t, reportFailure(2), connect.CodeNotFound)

	_, err := server.Router.ReportApplied(ctx, connect.NewRequest(&portalv1.ReportAppliedRequest{
		Team: &portalv1.TeamKey{TeamNumber: 1},
	}))
	assertCode(t, err, connect.CodeInvalidArgument)

	state := server.App.Store.Snapshot()
	cfg := state.Desired.Config
	cfg.MainServer = portalv1.ServerId_SERVER_ID_SERVER2
	_, err = server.Router.ReportApplied(ctx, connect.NewRequest(&portalv1.ReportAppliedRequest{
		Team: &portalv1.TeamKey{TeamNumber: 1},
		Result: &portalv1.ReportAppliedRequest_Success{Success: &portalv1.ApplySuccess{
			SnapshotId: "unknown", Applied: testkit.ConfigProto(cfg),
		}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	applied := server.App.Store.Snapshot()
	if applied.Converged || applied.Applied == nil || applied.Applied.SnapshotID != "unknown" {
		t.Fatalf("unknown/mismatch success was not accepted: %+v", applied)
	}

	invalid := testkit.ConfigProto(cfg)
	invalid.ServerRegions.Server1 = 99
	_, err = server.Router.ReportApplied(ctx, connect.NewRequest(&portalv1.ReportAppliedRequest{
		Team: &portalv1.TeamKey{TeamNumber: 1},
		Result: &portalv1.ReportAppliedRequest_Success{Success: &portalv1.ApplySuccess{
			SnapshotId: "bad", Applied: invalid,
		}},
	}))
	assertCode(t, err, connect.CodeInvalidArgument)
	if server.App.Store.Snapshot().Apply.State != "invalid-report" {
		t.Fatal("invalid report diagnostic missing")
	}
}
