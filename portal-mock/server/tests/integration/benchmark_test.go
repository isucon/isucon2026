package integration

import (
	"context"
	"reflect"
	"testing"
	"time"

	"connectrpc.com/connect"
	portalv1 "github.com/isucon/isucon2026/gen/go/portal/v1"
	portalmockv1 "github.com/isucon/isucon2026/gen/go/portal_mock/v1"
	"github.com/isucon/isucon2026/portal-mock/server/tests/testkit"
)

func TestBenchmarkLifecycleLockHistoryAndDeterminism(t *testing.T) {
	server := testkit.NewServer(t, nil)
	ctx := context.Background()
	started, err := server.Mock.StartBenchmark(ctx, connect.NewRequest(&portalmockv1.StartBenchmarkRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if started.Msg.Benchmark.Status != portalmockv1.BenchmarkStatus_BENCHMARK_STATUS_PENDING {
		t.Fatalf("unexpected start: %+v", started.Msg.Benchmark)
	}
	main := portalv1.ServerId_SERVER_ID_SERVER2
	_, err = server.Mock.UpdateDesired(ctx, connect.NewRequest(&portalmockv1.UpdateDesiredRequest{
		Patch: &portalmockv1.DesiredPatch{MainServer: &main},
	}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("update during benchmark: %v", err)
	}
	_, err = server.Mock.StartBenchmark(ctx, connect.NewRequest(&portalmockv1.StartBenchmarkRequest{}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("duplicate start: %v", err)
	}
	advanceBenchmark(t, server)
	first := server.App.Store.Snapshot()
	if first.Benchmark.Status != "idle" || len(first.History) != 1 ||
		first.Scores.RunsCount != 1 || !first.Scores.HasScores {
		t.Fatalf("benchmark did not complete: %+v", first)
	}
	firstRun := first.History[0]
	server.Mock.Reset(ctx, connect.NewRequest(&portalmockv1.ResetRequest{}))
	server.Mock.StartBenchmark(ctx, connect.NewRequest(&portalmockv1.StartBenchmarkRequest{}))
	advanceBenchmark(t, server)
	replayed := server.App.Store.Snapshot().History[0]
	firstRun.ID, replayed.ID = "", ""
	firstRun.StartedAt, replayed.StartedAt = time.Time{}, time.Time{}
	firstRun.CompletedAt, replayed.CompletedAt = time.Time{}, time.Time{}
	if !reflect.DeepEqual(firstRun, replayed) {
		t.Fatalf("reset did not replay deterministic outcome:\n%+v\n%+v", firstRun, replayed)
	}
}

func TestPendingConfigurationBlocksBenchmark(t *testing.T) {
	server := testkit.NewServer(t, nil)
	main := portalv1.ServerId_SERVER_ID_SERVER2
	_, err := server.Mock.UpdateDesired(context.Background(), connect.NewRequest(&portalmockv1.UpdateDesiredRequest{
		Patch: &portalmockv1.DesiredPatch{MainServer: &main},
	}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = server.Mock.StartBenchmark(context.Background(), connect.NewRequest(&portalmockv1.StartBenchmarkRequest{}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("pending benchmark start: %v", err)
	}
}

func TestResetWhileRunningCancelsCompletion(t *testing.T) {
	server := testkit.NewServer(t, nil)
	_, err := server.Mock.StartBenchmark(context.Background(), connect.NewRequest(&portalmockv1.StartBenchmarkRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	server.Clock.Advance(100 * time.Millisecond)
	testkit.Eventually(t, time.Second, func() bool {
		return server.App.Store.Snapshot().Benchmark.Status == "running"
	})
	_, err = server.Mock.Reset(context.Background(), connect.NewRequest(&portalmockv1.ResetRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	server.Clock.Advance(10 * time.Second)
	time.Sleep(time.Millisecond)
	state := server.App.Store.Snapshot()
	if state.Benchmark.Status != "idle" || len(state.History) != 0 || state.Scores.HasScores {
		t.Fatalf("running work escaped reset: %+v", state)
	}
}

func advanceBenchmark(t *testing.T, server *testkit.Server) {
	t.Helper()
	server.Clock.Advance(100 * time.Millisecond)
	testkit.Eventually(t, time.Second, func() bool {
		return server.App.Store.Snapshot().Benchmark.Status == "running"
	})
	for progress := uint32(20); progress <= 100; progress += 20 {
		server.Clock.Advance(100 * time.Millisecond)
		want := progress
		testkit.Eventually(t, time.Second, func() bool {
			state := server.App.Store.Snapshot()
			return state.Benchmark.Status == "idle" || state.Benchmark.Progress >= want
		})
	}
	testkit.Eventually(t, time.Second, func() bool {
		return server.App.Store.Snapshot().Benchmark.Status == "idle"
	})
}
