package integration_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	pbresources "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/resources"
	pbadmin "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/services/admin"
	pbcontestant "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/services/contestant"
	"github.com/isucon/isucon2026/portal/internal/model"
	"github.com/isucon/isucon2026/portal/tests/testkit"
)

func TestBenchmarkJobListEnqueuedBy(t *testing.T) {
	p := testkit.Start(t)
	teamID, leaderID := p.CreateTeam(t, "job-list")
	otherTeam, otherLeader := p.CreateTeam(t, "other-list")
	insert := func(team int64, by any, status int32) int64 {
		t.Helper()
		var id int64
		if err := p.DB.QueryRow(`INSERT INTO benchmark_jobs (team_id, enqueued_by_id, status, created_at, updated_at) VALUES ($1, $2, $3, now(), now()) RETURNING id`, team, by, status).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	pending := insert(teamID, leaderID, model.JobPending)
	finished := insert(teamID, leaderID, model.JobFinished)
	anonymous := insert(teamID, nil, model.JobFinished)
	missing := insert(teamID, 999999, model.JobFinished)
	other := insert(otherTeam, otherLeader, model.JobFinished)
	if _, err := p.DB.Exec(`INSERT INTO benchmark_results (team_id, benchmark_job_id, score, finished, passed, marked_at, created_at, updated_at) VALUES ($1, $2, 12345, true, false, now(), now(), now())`, teamID, finished); err != nil {
		t.Fatal(err)
	}
	headers := testkit.TimingHeaders(time.Now())
	headers["X-Isux-Session-Contestant-Id"] = fmt.Sprint(leaderID)
	adminHeaders := map[string]string{"X-Isux-Session-Staff": "1"}
	for _, tc := range []struct {
		name, path string
		admin      bool
		ids        []int64
	}{
		{"contestant", "/api/contestant/benchmark_jobs", false, []int64{missing, anonymous, finished, pending}},
		{"dashboard", "/api/contestant/benchmark_jobs?limit=5", false, []int64{missing, anonymous, finished, pending}},
		{"pending", "/api/contestant/benchmark_jobs?status=0", false, []int64{pending}},
		{"admin", "/api/admin/benchmark_jobs?page=0", true, []int64{other, missing, anonymous, finished, pending}},
		{"admin_failed", fmt.Sprintf("/api/admin/benchmark_jobs?team_id=%d&failed_only=1&page=0", teamID), true, []int64{finished}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var jobs []*pbresources.BenchmarkJob
			if tc.admin {
				body := &pbadmin.ListBenchmarkJobsResponse{}
				res := p.DoPB(t, http.MethodGet, tc.path, nil, adminHeaders)
				if res.StatusCode != http.StatusOK {
					t.Fatalf("status = %d", res.StatusCode)
				}
				testkit.DecodePB(t, res, body)
				jobs = body.Jobs
			} else {
				body := &pbcontestant.ListBenchmarkJobsResponse{}
				res := p.DoPB(t, http.MethodGet, tc.path, nil, headers)
				if res.StatusCode != http.StatusOK {
					t.Fatalf("status = %d", res.StatusCode)
				}
				testkit.DecodePB(t, res, body)
				jobs = body.Jobs
			}
			if len(jobs) != len(tc.ids) {
				t.Fatalf("jobs = %v", jobs)
			}
			for i, j := range jobs {
				if j.Id != tc.ids[i] {
					t.Errorf("job ID = %d, want %d", j.Id, tc.ids[i])
				}
				if j.Id == anonymous || j.Id == missing {
					if j.EnqueuedBy != nil {
						t.Errorf("job %d: unexpected enqueued_by %v", j.Id, j.EnqueuedBy)
					}
					continue
				}
				wantName := "job-list-leader"
				if j.Id == other {
					wantName = "other-list-leader"
				}
				if j.GetEnqueuedBy().GetName() != wantName || j.GetEnqueuedBy().GetAvatarUrl() != "https://test.invalid/avatar" {
					t.Errorf("job %d: enqueued_by = %v", j.Id, j.EnqueuedBy)
				}
				if j.Id == finished && j.GetScore() != 12345 {
					t.Errorf("score = %d", j.GetScore())
				}
			}
		})
	}
}
