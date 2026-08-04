package integration

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	portalv1 "github.com/isucon/isucon2026/gen/go/portal/v1"
	portalmockv1 "github.com/isucon/isucon2026/gen/go/portal_mock/v1"
	"github.com/isucon/isucon2026/portal-mock/server/internal/config"
	"github.com/isucon/isucon2026/portal-mock/server/internal/domain"
	"github.com/isucon/isucon2026/portal-mock/server/tests/testkit"
)

func TestExternalRouterUnsyncedConvergenceAndStaleReport(t *testing.T) {
	server := testkit.NewServer(t, func(cfg *config.Config) {
		cfg.RouterMode = config.RouterModeExternalRouter
		cfg.StandInProfile = ""
	})
	initial := server.App.Store.Snapshot()
	if initial.Applied != nil || initial.Apply.State != domain.ApplyUnsynced {
		t.Fatalf("unexpected external startup: %+v", initial)
	}
	oldSnapshot := initial.Desired.SnapshotID
	reset, err := server.Mock.Reset(context.Background(), connect.NewRequest(&portalmockv1.ResetRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if reset.Msg.State.Portal.Applied != nil ||
		reset.Msg.State.Portal.Apply.State != portalmockv1.ApplyState_APPLY_STATE_UNSYNCED {
		t.Fatalf("unexpected external reset: %+v", reset.Msg.State)
	}
	_, err = server.Router.ReportApplied(context.Background(), connect.NewRequest(&portalv1.ReportAppliedRequest{
		Team: &portalv1.TeamKey{TeamNumber: 1},
		Result: &portalv1.ReportAppliedRequest_Success{Success: &portalv1.ApplySuccess{
			SnapshotId: oldSnapshot,
			Applied:    testkit.ConfigProto(initial.Desired.Config),
		}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	state := server.App.Store.Snapshot()
	if !state.Converged || state.Applied.SnapshotID != oldSnapshot ||
		state.Apply.State != domain.ApplyConverged {
		t.Fatalf("stale correlated report should converge by config: %+v", state)
	}
}
