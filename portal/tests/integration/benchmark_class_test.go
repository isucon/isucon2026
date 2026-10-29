package integration_test

import (
	"net/http"
	"sync"
	"testing"

	pbbench "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/services/bench"
	"github.com/isucon/isucon2026/portal/tests/testkit"
)

func TestBenchmarkClassRouting(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		score                  int
		finished, passed, post bool
		threshold              int64
		class                  string
		want                   bool
	}{
		{"above", 101, true, true, false, 100, "large", true},
		{"equal", 100, true, true, false, 100, "large", false},
		{"below", 99, true, true, false, 100, "standard", true},
		{"failed", 101, true, false, false, 100, "large", false},
		{"unfinished", 101, false, true, false, 100, "large", false},
		{"post validation", 101, true, true, true, 100, "large", false},
		{"legacy worker", 101, true, true, false, 100, "", false},
		{"standard excludes large", 101, true, true, false, 100, "standard", false},
		{"disabled standard", 101, true, true, false, 0, "", true},
		{"disabled large", 101, true, true, false, 0, "large", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := testkit.Start(t)
			p.Cfg.BenchLargeScoreThreshold = tc.threshold
			team, _ := p.CreateTeam(t, "routing")
			var history, pending int64
			err := p.DB.QueryRow(`INSERT INTO benchmark_jobs (team_id,status,post_validation,created_at,updated_at) VALUES ($1,4,$2,now(),now()) RETURNING id`, team, tc.post).Scan(&history)
			if err != nil {
				t.Fatal(err)
			}
			_, err = p.DB.Exec(`INSERT INTO benchmark_results (team_id,benchmark_job_id,score,finished,passed,marked_at,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,now(),now(),now())`, team, history, tc.score, tc.finished, tc.passed)
			if err != nil {
				t.Fatal(err)
			}
			// A later low result must not demote a previously qualified team.
			var later int64
			err = p.DB.QueryRow(`INSERT INTO benchmark_jobs (team_id,status,created_at,updated_at) VALUES ($1,4,now(),now()) RETURNING id`, team).Scan(&later)
			if err != nil {
				t.Fatal(err)
			}
			_, err = p.DB.Exec(`INSERT INTO benchmark_results (team_id,benchmark_job_id,score,finished,passed,marked_at,created_at,updated_at) VALUES ($1,$2,1,true,true,now(),now(),now())`, team, later)
			if err != nil {
				t.Fatal(err)
			}
			err = p.DB.QueryRow(`INSERT INTO benchmark_jobs (team_id,status,created_at,updated_at) VALUES ($1,0,now(),now()) RETURNING id`, team).Scan(&pending)
			if err != nil {
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			ids := make(chan int64, 8)
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					res := p.DoPB(t, http.MethodPost, "/api/bench/receive", &pbbench.ReceiveBenchmarkJobRequest{Token: "token"}, map[string]string{"X-Isux-Benchmark-Class": tc.class})
					if res.StatusCode != 200 {
						t.Errorf("status %d", res.StatusCode)
						res.Body.Close()
						return
					}
					body := &pbbench.ReceiveBenchmarkJobResponse{}
					testkit.DecodePB(t, res, body)
					if body.JobHandle != nil {
						ids <- body.JobHandle.JobId
					}
				}()
			}
			wg.Wait()
			close(ids)
			count := 0
			for id := range ids {
				count++
				if id != pending {
					t.Errorf("job %d, want %d", id, pending)
				}
			}
			want := 0
			if tc.want {
				want = 1
			}
			if count != want {
				t.Fatalf("received %d jobs, want %d", count, want)
			}
		})
	}
}

func TestBenchmarkClassRejectsUnknown(t *testing.T) {
	p := testkit.Start(t)
	res := p.DoPB(t, http.MethodPost, "/api/bench/receive", &pbbench.ReceiveBenchmarkJobRequest{Token: "token"}, map[string]string{"X-Isux-Benchmark-Class": "typo"})
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d", res.StatusCode)
	}
}
