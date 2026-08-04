package standin

import (
	"context"
	"testing"
	"time"

	"github.com/isucon/isucon2026/portal-mock/server/internal/clock"
	"github.com/isucon/isucon2026/portal-mock/server/internal/domain"
	"github.com/isucon/isucon2026/portal-mock/server/internal/id"
	"github.com/isucon/isucon2026/portal-mock/server/internal/store"
)

func TestResetCancelsOldStandInWork(t *testing.T) {
	fakeClock := clock.NewFake(time.Unix(1_700_000_000, 0))
	stateStore, err := store.New(store.Options{
		Team:       domain.TeamKey{Number: 1},
		Hostname:   domain.HostnameConfig{RandomHash: "hash", DomainSuffix: "example.test"},
		RouterMode: domain.RouterModeStandIn, StandInProfile: domain.StandInProfileSuccess,
		ProcessStartedAt: fakeClock.Now(), ApplyTimeout: time.Second,
		Clock: fakeClock, ID: id.NewFake("id"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stateStore.Close()
	worker, err := New(stateStore, fakeClock, domain.StandInProfileSuccess, 100*time.Millisecond, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	worker.Start(ctx)
	defer func() {
		cancel()
		worker.Close()
	}()
	main := domain.Server2
	_, oldDesired, _, err := stateStore.UpdateDesired(store.Patch{MainServer: &main})
	if err != nil {
		t.Fatal(err)
	}
	reset := stateStore.Reset()
	fakeClock.Advance(time.Second)
	time.Sleep(time.Millisecond)
	state := stateStore.Snapshot()
	if state.Desired.SnapshotID != reset.Desired.SnapshotID ||
		state.Applied == nil ||
		state.Applied.SnapshotID == oldDesired.SnapshotID ||
		!state.Converged {
		t.Fatalf("stale stand-in work escaped reset: %+v", state)
	}
}
