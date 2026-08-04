package concurrency

import (
	"context"
	"sync"
	"testing"

	"connectrpc.com/connect"
	portalmockv1 "github.com/isucon/isucon2026/gen/go/portal_mock/v1"
	"github.com/isucon/isucon2026/portal-mock/server/tests/testkit"
)

func TestOnlyOneCompetingBenchmarkStartSucceeds(t *testing.T) {
	server := testkit.NewServer(t, nil)
	var wg sync.WaitGroup
	results := make(chan error, 20)
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := server.Mock.StartBenchmark(context.Background(), connect.NewRequest(&portalmockv1.StartBenchmarkRequest{}))
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatalf("unexpected start error: %v", err)
		}
	}
	if success != 1 {
		t.Fatalf("successful starts = %d, want 1", success)
	}
}
