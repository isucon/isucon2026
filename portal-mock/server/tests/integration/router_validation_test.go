package integration

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	portalv1 "github.com/isucon/isucon2026/gen/go/portal/v1"
	"github.com/isucon/isucon2026/portal-mock/server/internal/config"
	"github.com/isucon/isucon2026/portal-mock/server/internal/domain"
	"github.com/isucon/isucon2026/portal-mock/server/tests/testkit"
)

func TestInvalidReportThenValidMismatchRecovery(t *testing.T) {
	server := testkit.NewServer(t, func(cfg *config.Config) {
		cfg.RouterMode = config.RouterModeExternalRouter
		cfg.StandInProfile = ""
	})
	desired := server.App.Store.Snapshot().Desired
	invalid := testkit.ConfigProto(desired.Config)
	invalid.DefaultCountry = "XX"
	_, err := server.Router.ReportApplied(context.Background(), connect.NewRequest(&portalv1.ReportAppliedRequest{
		Team: &portalv1.TeamKey{TeamNumber: 1},
		Result: &portalv1.ReportAppliedRequest_Success{Success: &portalv1.ApplySuccess{
			SnapshotId: desired.SnapshotID, Applied: invalid,
		}},
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("unexpected error: %v", err)
	}
	if server.App.Store.Snapshot().Apply.State != domain.ApplyInvalidReport {
		t.Fatal("missing invalid report diagnostic")
	}
	mismatch := desired.Config
	mismatch.MainServer = domain.Server2
	_, err = server.Router.ReportApplied(context.Background(), connect.NewRequest(&portalv1.ReportAppliedRequest{
		Team: &portalv1.TeamKey{TeamNumber: 1},
		Result: &portalv1.ReportAppliedRequest_Success{Success: &portalv1.ApplySuccess{
			SnapshotId: "other", Applied: testkit.ConfigProto(mismatch),
		}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	state := server.App.Store.Snapshot()
	if state.Apply.State != domain.ApplyPending || state.Apply.Reason != "" || state.Converged {
		t.Fatalf("diagnostic did not recover: %+v", state.Apply)
	}
}
