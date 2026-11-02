package integration_test

import (
	"net/http"
	"testing"
	"time"

	pbmisc "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/misc"
	audience "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/services/audience"
	"github.com/isucon/isucon2026/portal/internal/contest"
	"github.com/isucon/isucon2026/portal/internal/job"
	"github.com/isucon/isucon2026/portal/tests/testkit"
	"google.golang.org/protobuf/proto"
)

func TestLeaderboardDeadlineUsesEnqueueTime(t *testing.T) {
	p := testkit.Start(t)
	ctx := t.Context()
	team, _ := p.CreateTeam(t, "deadline")
	end := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
	p.Cfg.Timing.ContestEnd = &end
	insert := func(enqueued, marked time.Time, score int, finished, postValidation bool) int64 {
		t.Helper()
		var jobID, resultID int64
		if err := p.DB.QueryRow(`INSERT INTO benchmark_jobs(team_id,status,created_at,updated_at,post_validation) VALUES($1,4,$2,$2,$3) RETURNING id`, team, enqueued, postValidation).Scan(&jobID); err != nil {
			t.Fatal(err)
		}
		if err := p.DB.QueryRow(`INSERT INTO benchmark_results(team_id,benchmark_job_id,score,finished,passed,exit_status,marked_at,created_at,updated_at) VALUES($1,$2,$3,$4,true,0,$5,$5,$5) RETURNING id`, team, jobID, score, finished, marked).Scan(&resultID); err != nil {
			t.Fatal(err)
		}
		return resultID
	}
	insert(end.Add(-2*time.Minute), end.Add(-time.Minute), 100, true, false)
	accepted := insert(end.Add(-time.Microsecond), end.Add(5*time.Minute), 200, true, false)
	// Exactly at the deadline and later are excluded, even with larger scores.
	insert(end, end.Add(6*time.Minute), 300, true, false)
	insert(end.Add(time.Second), end.Add(7*time.Minute), 400, true, false)
	insert(end.Add(-time.Second), end.Add(8*time.Minute), 500, false, false)
	insert(end.Add(-time.Second), end.Add(9*time.Minute), 600, true, true)

	if err := contest.MaintainBestBenchmarkResults(ctx, p.DB, p.Cfg.Timing, nil); err != nil {
		t.Fatal(err)
	}
	for _, params := range []contest.LeaderboardParams{{}, {TeamID: team}, {History: true}} {
		lb, _, err := contest.BuildLeaderboard(ctx, p.DB, p.Cfg.Timing, end.Add(time.Hour), params)
		if err != nil {
			t.Fatal(err)
		}
		if len(lb.Teams) != 1 || lb.Teams[0].GetLatestScore().GetScore() != 200 || lb.Teams[0].GetBestScore().GetScore() != 200 {
			t.Fatalf("params=%+v: deadline must use enqueue time: %v", params, lb)
		}
		if params.History && len(lb.Teams[0].GetScoreHistory().GetScores()) != 2 {
			t.Fatalf("history contains ineligible results: %v", lb)
		}
	}
	etag, err := contest.LeaderboardEtag(ctx, p.DB, p.Cfg.Timing, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	var decoded pbmisc.LeaderboardEtag
	if err := proto.Unmarshal(etag, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.LatestResultId != accepted {
		t.Fatalf("ETag latest result = %d, want %d", decoded.LatestResultId, accepted)
	}
	if err := p.Jobs.UpdateContestantDashboard(ctx, job.UpdateDashboardArgs{Frozen: true}); err != nil {
		t.Fatal(err)
	}
	res := p.DoPB(t, http.MethodGet, "/api/audience/dashboard", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatal(res.Status)
	}
	var cached audience.DashboardResponse
	testkit.DecodePB(t, res, &cached)
	if cached.GetLeaderboard().GetTeams()[0].GetLatestScore().GetScore() != 200 {
		t.Fatalf("cached dashboard excluded late completion: %v", &cached)
	}
	for _, tc := range []struct {
		name             string
		freeze, deadline *time.Time
		params           contest.LeaderboardParams
		want             int64
	}{
		{"admin", nil, &end, contest.LeaderboardParams{Admin: true}, 400},
		{"frozen audience", &end, &end, contest.LeaderboardParams{}, 100},
		{"frozen own team", &end, &end, contest.LeaderboardParams{TeamID: team}, 200},
		{"no deadline", nil, nil, contest.LeaderboardParams{}, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p.Cfg.Timing.ContestFreeze, p.Cfg.Timing.ContestEnd = tc.freeze, tc.deadline
			lb, _, err := contest.BuildLeaderboard(ctx, p.DB, p.Cfg.Timing, end.Add(time.Hour), tc.params)
			if err != nil {
				t.Fatal(err)
			}
			if got := lb.Teams[0].GetLatestScore().GetScore(); got != tc.want {
				t.Fatalf("latest score = %d, want %d", got, tc.want)
			}
		})
	}
}
