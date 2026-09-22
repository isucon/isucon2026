package store

import (
	"context"
	"testing"
	"time"

	"github.com/isucon/isucon2026/portal-mock/server/internal/domain"
)

func TestSubscriptionInitialResetAndCoalescing(t *testing.T) {
	stateStore, _ := testStore(t, domain.RouterModeExternalRouter)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	subscription := stateStore.SubscribeDesired(ctx)
	initial := <-subscription.C
	for _, server := range []domain.ServerID{domain.Server2, domain.Server3, domain.Server1} {
		changed, _, _, err := stateStore.UpdateDesired(Patch{MainServer: &server})
		if err != nil {
			t.Fatal(err)
		}
		if !changed && server != domain.Server1 {
			t.Fatal("expected update")
		}
	}
	latest := <-subscription.C
	if latest.Config.MainServer != domain.Server1 || latest.SnapshotID == initial.SnapshotID {
		t.Fatalf("did not coalesce to latest: %+v", latest)
	}
	reset := stateStore.Reset()
	select {
	case got := <-subscription.C:
		if got.SnapshotID != reset.Desired.SnapshotID {
			t.Fatalf("got stale reset event: %s", got.SnapshotID)
		}
	case <-time.After(time.Second):
		t.Fatal("reset event not delivered")
	}
}
