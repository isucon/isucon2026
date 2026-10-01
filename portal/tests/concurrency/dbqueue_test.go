package concurrency_test

import (
	"context"
	"sync"
	"testing"

	"github.com/isucon/isucon2026/portal/internal/job"
	"github.com/isucon/isucon2026/portal/tests/testkit"
)

// 複数ワーカーが同時に claim しても、各ジョブは exactly-once で払い出される
// （FOR UPDATE SKIP LOCKED）。
func TestDBQueueConcurrentClaimIsExclusive(t *testing.T) {
	conn := testkit.Database(t)
	ctx := context.Background()
	q := &job.DBQueue{DB: conn}

	const jobCount = 40
	for range jobCount {
		if err := q.Enqueue(ctx, "slack_webhook", nil); err != nil {
			t.Fatal(err)
		}
	}

	const workers = 8
	claimed := make(chan int64, jobCount+workers)
	errs := make(chan error, jobCount+workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for {
				j, err := q.Claim(ctx)
				if err != nil {
					errs <- err
					return
				}
				if j == nil {
					return
				}
				claimed <- j.ID
			}
		}()
	}
	wait.Wait()
	close(claimed)
	close(errs)

	for err := range errs {
		t.Errorf("claim error: %v", err)
	}
	seen := make(map[int64]int, jobCount)
	for id := range claimed {
		seen[id]++
	}
	if len(seen) != jobCount {
		t.Fatalf("claimed %d distinct jobs, want %d", len(seen), jobCount)
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("job %d claimed %d times", id, n)
		}
	}
}
