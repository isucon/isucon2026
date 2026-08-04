package concurrency

import (
	"sync"
	"testing"
	"time"

	"github.com/isucon/isucon2026/portal-mock/server/internal/domain"
	"github.com/isucon/isucon2026/portal-mock/server/internal/store"
	"github.com/isucon/isucon2026/portal-mock/server/tests/testkit"
)

func TestConcurrentUpdatesAndReportsPreserveValidState(t *testing.T) {
	server := testkit.NewServer(t, nil)
	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(2)
		go func(index int) {
			defer wg.Done()
			serverID := domain.ServerID(index%3 + 1)
			_, _, _, _ = server.App.Store.UpdateDesired(store.Patch{MainServer: &serverID})
		}(i)
		go func(index int) {
			defer wg.Done()
			cfg := domain.InitialConfig()
			cfg.MainServer = domain.ServerID(index%3 + 1)
			_ = server.App.Store.ReportSuccess("report", cfg)
		}(i)
	}
	wg.Wait()
	testkit.Eventually(t, time.Second, func() bool {
		state := server.App.Store.Snapshot()
		return state.Desired.Config.Validate() == nil &&
			state.Applied != nil &&
			state.Applied.Config.Validate() == nil
	})
}
