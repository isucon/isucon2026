package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/isucon/isucon2026/portal-mock/server/internal/clock"
	"github.com/isucon/isucon2026/portal-mock/server/internal/domain"
	"github.com/isucon/isucon2026/portal-mock/server/internal/id"
)

func testStore(t *testing.T, mode domain.RouterMode) (*Store, *clock.Fake) {
	t.Helper()
	fakeClock := clock.NewFake(time.Unix(1_700_000_000, 0))
	profile := domain.StandInProfileNone
	if mode == domain.RouterModeStandIn {
		profile = domain.StandInProfileSuccess
	}
	stateStore, err := New(Options{
		Team:       domain.TeamKey{Number: 1},
		Hostname:   domain.HostnameConfig{RandomHash: "hash", DomainSuffix: "example.test"},
		RouterMode: mode, StandInProfile: profile,
		ProcessStartedAt: fakeClock.Now(), ApplyTimeout: time.Second,
		Clock: fakeClock, ID: id.NewFake("id"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stateStore.Close)
	return stateStore, fakeClock
}

func TestUpdateNoopAndReports(t *testing.T) {
	stateStore, _ := testStore(t, domain.RouterModeExternalRouter)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	desiredEvents := stateStore.SubscribeDesired(ctx)
	stateEvents := stateStore.SubscribeState(ctx)
	<-desiredEvents.C
	<-stateEvents.C
	main := domain.Server1
	changed, desired, _, err := stateStore.UpdateDesired(Patch{MainServer: &main})
	if err != nil || changed {
		t.Fatalf("no-op: changed=%v err=%v", changed, err)
	}
	select {
	case <-desiredEvents.C:
		t.Fatal("no-op emitted desired event")
	default:
	}
	if err := stateStore.ReportSuccess(desired.SnapshotID, desired.Config); err != nil {
		t.Fatal(err)
	}
	if !stateStore.Snapshot().Converged {
		t.Fatal("success did not converge")
	}
}

func TestInvalidReportPreservesAppliedAndRecovers(t *testing.T) {
	stateStore, _ := testStore(t, domain.RouterModeStandIn)
	before := stateStore.Snapshot()
	invalid := before.Desired.Config
	invalid.DefaultCountry = "XX"
	if err := stateStore.ReportSuccess("unknown", invalid); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("got %v", err)
	}
	afterInvalid := stateStore.Snapshot()
	if afterInvalid.Applied == nil || afterInvalid.Applied.Config != before.Applied.Config ||
		afterInvalid.Apply.State != domain.ApplyInvalidReport {
		t.Fatalf("invalid report changed applied: %+v", afterInvalid)
	}
	mismatch := before.Desired.Config
	mismatch.MainServer = domain.Server2
	if err := stateStore.ReportSuccess("unknown-valid", mismatch); err != nil {
		t.Fatal(err)
	}
	recovered := stateStore.Snapshot()
	if recovered.Apply.State == domain.ApplyInvalidReport || recovered.Apply.Reason != "" || recovered.Converged {
		t.Fatalf("invalid-report diagnostic was not cleared: %+v", recovered.Apply)
	}
}

func TestResetCreatesNewSnapshotAndExternalUnsynced(t *testing.T) {
	stateStore, _ := testStore(t, domain.RouterModeExternalRouter)
	old := stateStore.Snapshot()
	reset := stateStore.Reset()
	if old.Desired.SnapshotID == reset.Desired.SnapshotID {
		t.Fatal("snapshot ID reused")
	}
	if reset.Applied != nil || reset.Apply.State != domain.ApplyUnsynced || reset.Desired.Calculated.AppHostname != old.Desired.Calculated.AppHostname {
		t.Fatalf("unexpected reset state: %+v", reset)
	}
}

// A success report with a valid Config but a missing (empty) snapshot ID is a
// required-field violation. Per the spec it must be treated as an invalid
// success report: applied stays unchanged, the diagnostic becomes
// INVALID_REPORT with the validity reason, and INVALID_ARGUMENT is returned.
func TestEmptySnapshotIDValidConfigSetsInvalidReport(t *testing.T) {
	stateStore, _ := testStore(t, domain.RouterModeStandIn)
	before := stateStore.Snapshot()
	valid := before.Desired.Config
	valid.MainServer = domain.Server2

	err := stateStore.ReportSuccess("", valid)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument, got %v", err)
	}
	after := stateStore.Snapshot()
	if after.Apply.State != domain.ApplyInvalidReport {
		t.Fatalf("expected INVALID_REPORT diagnostic, got %q", after.Apply.State)
	}
	if after.Apply.Reason == "" {
		t.Fatal("expected a validity reason on the diagnostic")
	}
	if after.Applied == nil || after.Applied.Config != before.Applied.Config {
		t.Fatalf("applied config must be preserved: %+v", after.Applied)
	}
	// A subsequent valid report must clear the INVALID_REPORT diagnostic,
	// proving the state is not a terminal error.
	recovered := before.Desired.Config
	recovered.MainServer = domain.Server3
	if err := stateStore.ReportSuccess("late-valid", recovered); err != nil {
		t.Fatalf("valid recovery report failed: %v", err)
	}
	state := stateStore.Snapshot()
	if state.Apply.State == domain.ApplyInvalidReport || state.Apply.Reason != "" {
		t.Fatalf("diagnostic did not recover: %+v", state.Apply)
	}
}
