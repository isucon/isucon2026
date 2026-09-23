// Package concurrency_test は並行時の不変条件を検証する。
// backend/tests/concurrency と同じく portal-concurrency ジョブで動く。
package concurrency_test

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	pbresources "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/resources"
	pbbench "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/services/bench"
	pbcontestant "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/services/contestant"
	"github.com/isucon/isucon2026/portal/tests/testkit"
)

// DM-4: 同時 enqueue はチームあたり 1 件しか通らない
// （Rails のアプリ層 COUNT では競合で抜けていた箇所を SELECT ... FOR UPDATE で塞いだ）。
func TestConcurrentEnqueueLeavesOneJob(t *testing.T) {
	p := testkit.Start(t)
	now := time.Now()
	teamID, leaderID := p.CreateTeam(t, "enqueue-race")
	instanceID := p.CreateInstance(t, teamID, 1)

	headers := testkit.TimingHeaders(now)
	headers["X-Isux-Session-Contestant-Id"] = fmt.Sprint(leaderID)

	const workers = 8
	statuses := make(chan int, workers)
	errs := make(chan error, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			res, err := p.TryPB(http.MethodPost, "/api/contestant/benchmark_jobs",
				&pbcontestant.EnqueueBenchmarkJobRequest{TargetId: instanceID}, headers)
			if err != nil {
				errs <- err
				return
			}
			res.Body.Close()
			statuses <- res.StatusCode
		}()
	}
	wait.Wait()
	close(statuses)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	success := 0
	for status := range statuses {
		switch status {
		case http.StatusOK:
			success++
		case 422:
		default:
			t.Errorf("unexpected status %d", status)
		}
	}
	if success != 1 {
		t.Errorf("successful enqueues = %d, want exactly 1", success)
	}
	var jobs int
	if err := p.DB.QueryRow(`SELECT COUNT(*) FROM benchmark_jobs WHERE team_id = $1`, teamID).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 1 {
		t.Errorf("jobs in DB = %d, want 1", jobs)
	}
}

// NF-3: supervisor フリートの同時ポーリング下で、pending ジョブは重複なく 1 件ずつ払い出される。
func TestBenchReceiveHandsOutEachJobExactlyOnce(t *testing.T) {
	p := testkit.Start(t)
	now := time.Now()
	timing := testkit.TimingHeaders(now)

	const jobCount = 5
	pending := map[int64]bool{}
	for i := range jobCount {
		teamID, _ := p.CreateTeam(t, fmt.Sprintf("receive-race-%d", i))
		instanceID := p.CreateInstance(t, teamID, 1)
		var id int64
		if err := p.DB.QueryRow(`
			INSERT INTO benchmark_jobs (team_id, status, instance_name, handle, target_id, post_validation, created_at, updated_at)
			VALUES ($1, 0, 'bench-az', 'handle-seed', $2, false, now(), now()) RETURNING id`,
			teamID, instanceID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		pending[id] = true
	}

	const pollers = 12
	results := make(chan int64, pollers) // 0 = 空応答
	errs := make(chan error, pollers)
	var wait sync.WaitGroup
	for range pollers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			res, err := p.TryPB(http.MethodPost, "/api/bench/receive",
				&pbbench.ReceiveBenchmarkJobRequest{Token: "token", InstanceName: "bench-az"}, timing)
			if err != nil {
				errs <- err
				return
			}
			body := &pbbench.ReceiveBenchmarkJobResponse{}
			if err := testkit.TryDecodePB(res, body); err != nil {
				errs <- err
				return
			}
			if body.JobHandle == nil {
				results <- 0
				return
			}
			results <- body.JobHandle.JobId
		}()
	}
	wait.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	handed := map[int64]int{}
	for id := range results {
		if id == 0 {
			continue
		}
		handed[id]++
	}
	if len(handed) != jobCount {
		t.Errorf("handed out %d distinct jobs, want %d", len(handed), jobCount)
	}
	for id, count := range handed {
		if !pending[id] {
			t.Errorf("unknown job %d handed out", id)
		}
		if count != 1 {
			t.Errorf("job %d handed out %d times, want exactly 1", id, count)
		}
	}
	var running int
	if err := p.DB.QueryRow(`SELECT COUNT(*) FROM benchmark_jobs WHERE status = 1`).Scan(&running); err != nil {
		t.Fatal(err)
	}
	if running != jobCount {
		t.Errorf("running jobs = %d, want %d", running, jobCount)
	}
}

// DM-3: 確定報告の競合では 1 本だけが確定し、finished 後の変更は拒否される。
func TestConcurrentCompletionClosesJobExactlyOnce(t *testing.T) {
	p := testkit.Start(t)
	now := time.Now()
	timing := testkit.TimingHeaders(now)
	teamID, leaderID := p.CreateTeam(t, "complete-race")
	instanceID := p.CreateInstance(t, teamID, 1)

	headers := testkit.TimingHeaders(now)
	headers["X-Isux-Session-Contestant-Id"] = fmt.Sprint(leaderID)
	res := p.DoPB(t, http.MethodPost, "/api/contestant/benchmark_jobs",
		&pbcontestant.EnqueueBenchmarkJobRequest{TargetId: instanceID}, headers)
	res.Body.Close()

	res = p.DoPB(t, http.MethodPost, "/api/bench/receive",
		&pbbench.ReceiveBenchmarkJobRequest{Token: "token", InstanceName: "apne1-az1"}, timing)
	received := &pbbench.ReceiveBenchmarkJobResponse{}
	testkit.DecodePB(t, res, received)
	if received.JobHandle == nil {
		t.Fatal("no job handed out")
	}

	const workers = 4
	statuses := make(chan int, workers)
	errs := make(chan error, workers)
	var wait sync.WaitGroup
	for i := range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			res, err := p.TryPB(http.MethodPost, "/api/bench/benchmark_results/completion",
				&pbbench.CompleteBenchmarkJobRequest{
					JobId:  received.JobHandle.JobId,
					Handle: received.JobHandle.Handle,
					Result: &pbresources.BenchmarkResult{
						Finished:       true,
						Passed:         true,
						Score:          int64(100 + i),
						ScoreBreakdown: &pbresources.BenchmarkResult_ScoreBreakdown{Raw: 100},
						MarkedAt:       timestamppb.New(now.Add(time.Duration(i) * time.Second)),
						Execution:      &pbresources.BenchmarkResult_Execution{ExitStatus: 0},
					},
				}, timing)
			if err != nil {
				errs <- err
				return
			}
			res.Body.Close()
			statuses <- res.StatusCode
		}()
	}
	wait.Wait()
	close(statuses)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	success := 0
	for status := range statuses {
		if status == http.StatusOK {
			success++
		}
	}
	if success != 1 {
		t.Errorf("successful completions = %d, want exactly 1", success)
	}
	var jobStatus int32
	var resultCount int
	if err := p.DB.QueryRow(`SELECT status FROM benchmark_jobs WHERE id = $1`, received.JobHandle.JobId).Scan(&jobStatus); err != nil {
		t.Fatal(err)
	}
	if jobStatus != int32(pbresources.BenchmarkJob_FINISHED) {
		t.Errorf("job status = %d, want FINISHED", jobStatus)
	}
	if err := p.DB.QueryRow(`SELECT COUNT(*) FROM benchmark_results WHERE benchmark_job_id = $1`, received.JobHandle.JobId).Scan(&resultCount); err != nil {
		t.Fatal(err)
	}
	if resultCount != 1 {
		t.Errorf("results = %d, want 1", resultCount)
	}
}
