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
	mismatch.DefaultCountry = "DE"
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

func TestEmptySnapshotIDIsInvalidReport(t *testing.T) {
	server := testkit.NewServer(t, func(cfg *config.Config) {
		cfg.RouterMode = config.RouterModeExternalRouter
		cfg.StandInProfile = ""
	})
	ctx := context.Background()
	desired := server.App.Store.Snapshot().Desired

	// Establish an applied value so the rejected report can also prove that it
	// does not overwrite the last valid Router state.
	appliedConfig := desired.Config
	appliedConfig.DefaultCountry = "DE"
	_, err := server.Router.ReportApplied(ctx, connect.NewRequest(&portalv1.ReportAppliedRequest{
		Team: &portalv1.TeamKey{TeamNumber: 1},
		Result: &portalv1.ReportAppliedRequest_Success{Success: &portalv1.ApplySuccess{
			SnapshotId: "known-applied", Applied: testkit.ConfigProto(appliedConfig),
		}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	before := server.App.Store.Snapshot()

	_, err = server.Router.ReportApplied(ctx, connect.NewRequest(&portalv1.ReportAppliedRequest{
		Team: &portalv1.TeamKey{TeamNumber: 1},
		Result: &portalv1.ReportAppliedRequest_Success{Success: &portalv1.ApplySuccess{
			SnapshotId: " \t ", Applied: testkit.ConfigProto(desired.Config),
		}},
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("unexpected error: %v", err)
	}
	after := server.App.Store.Snapshot()
	if after.Apply.State != domain.ApplyInvalidReport || after.Apply.Reason == "" {
		t.Fatalf("missing invalid report diagnostic: %+v", after.Apply)
	}
	if after.Applied == nil || before.Applied == nil ||
		after.Applied.SnapshotID != before.Applied.SnapshotID ||
		after.Applied.Config != before.Applied.Config {
		t.Fatalf("invalid report changed applied: before=%+v after=%+v", before.Applied, after.Applied)
	}
}

func TestMissingAppliedConfigPreservesStructuralValidationReason(t *testing.T) {
	server := testkit.NewServer(t, func(cfg *config.Config) {
		cfg.RouterMode = config.RouterModeExternalRouter
		cfg.StandInProfile = ""
	})
	ctx := context.Background()
	desired := server.App.Store.Snapshot().Desired
	_, err := server.Router.ReportApplied(ctx, connect.NewRequest(&portalv1.ReportAppliedRequest{
		Team: &portalv1.TeamKey{TeamNumber: 1},
		Result: &portalv1.ReportAppliedRequest_Success{Success: &portalv1.ApplySuccess{
			SnapshotId: desired.SnapshotID,
			Applied:    testkit.ConfigProto(desired.Config),
		}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	before := server.App.Store.Snapshot()

	_, err = server.Router.ReportApplied(ctx, connect.NewRequest(&portalv1.ReportAppliedRequest{
		Team: &portalv1.TeamKey{TeamNumber: 1},
		Result: &portalv1.ReportAppliedRequest_Success{Success: &portalv1.ApplySuccess{
			SnapshotId: desired.SnapshotID,
		}},
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("unexpected error: %v", err)
	}
	after := server.App.Store.Snapshot()
	if after.Apply.State != domain.ApplyInvalidReport ||
		after.Apply.Reason != "config and server_regions are required" {
		t.Fatalf("structural validation reason was not preserved: %+v", after.Apply)
	}
	if after.Applied == nil || before.Applied == nil ||
		after.Applied.SnapshotID != before.Applied.SnapshotID ||
		after.Applied.Config != before.Applied.Config {
		t.Fatalf("invalid report changed applied: before=%+v after=%+v", before.Applied, after.Applied)
	}
}
