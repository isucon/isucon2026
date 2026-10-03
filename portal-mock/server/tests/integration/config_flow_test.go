package integration

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	portalv1 "github.com/isucon/isucon2026/gen/go/portal/v1"
	portalmockv1 "github.com/isucon/isucon2026/gen/go/portal_mock/v1"
	"github.com/isucon/isucon2026/portal-mock/server/internal/config"
	"github.com/isucon/isucon2026/portal-mock/server/tests/testkit"
)

func TestStandInProfilesAndDesiredDiagnostics(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		server := testkit.NewServer(t, nil)
		country := "US"
		response, err := server.Mock.UpdateDesired(context.Background(), connect.NewRequest(&portalmockv1.UpdateDesiredRequest{
			Patch: &portalmockv1.DesiredPatch{DefaultCountry: &country},
		}))
		if err != nil {
			t.Fatal(err)
		}
		if !response.Msg.Changed || response.Msg.Apply.State != portalmockv1.ApplyState_APPLY_STATE_PENDING {
			t.Fatalf("unexpected update: %+v", response.Msg)
		}
		// The store timeout and stand-in timer must both be registered.
		testkit.Eventually(t, time.Second, func() bool {
			return server.Clock.PendingTimers() == 2
		})
		server.Clock.Advance(100 * time.Millisecond)
		testkit.Eventually(t, time.Second, func() bool {
			state := server.App.Store.Snapshot()
			return state.Converged && state.Applied != nil &&
				state.Applied.Config.DefaultCountry == country &&
				state.Applied.Calculated.TargetHostname == "app.team1-testhash.example.test" &&
				state.Applied.Calculated.DefaultCountryRegion == portalv1.Region_REGION_US
		})
	})

	t.Run("fail once then success", func(t *testing.T) {
		server := testkit.NewServer(t, func(cfg *config.Config) {
			cfg.StandInProfile = config.StandInFailOnceThenSuccess
		})
		app2 := "DE"
		_, err := server.Mock.UpdateDesired(context.Background(), connect.NewRequest(&portalmockv1.UpdateDesiredRequest{
			Patch: &portalmockv1.DesiredPatch{DefaultCountry: &app2},
		}))
		if err != nil {
			t.Fatal(err)
		}
		// The store timeout and stand-in timer must both be registered.
		testkit.Eventually(t, time.Second, func() bool {
			return server.Clock.PendingTimers() == 2
		})
		server.Clock.Advance(100 * time.Millisecond)
		testkit.Eventually(t, time.Second, func() bool {
			return server.App.Store.Snapshot().Apply.State == "failed-retryable"
		})
		// The store timeout and stand-in timer must both be registered.
		testkit.Eventually(t, time.Second, func() bool {
			return server.Clock.PendingTimers() == 2
		})
		server.Clock.Advance(100 * time.Millisecond)
		testkit.Eventually(t, time.Second, func() bool {
			return server.App.Store.Snapshot().Converged
		})
	})

	t.Run("no report becomes delayed without reverting desired", func(t *testing.T) {
		server := testkit.NewServer(t, func(cfg *config.Config) {
			cfg.StandInProfile = config.StandInNoReport
		})
		app2 := "DE"
		response, err := server.Mock.UpdateDesired(context.Background(), connect.NewRequest(&portalmockv1.UpdateDesiredRequest{
			Patch: &portalmockv1.DesiredPatch{DefaultCountry: &app2},
		}))
		if err != nil {
			t.Fatal(err)
		}
		server.Clock.Advance(time.Second)
		testkit.Eventually(t, time.Second, func() bool {
			state := server.App.Store.Snapshot()
			return state.Apply.Delayed &&
				state.Desired.SnapshotID == response.Msg.Desired.SnapshotId &&
				state.Applied.Config.DefaultCountry == "JP"
		})
	})
}
