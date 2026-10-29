package integration_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	pbadmin "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/services/admin"
	"github.com/isucon/isucon2026/portal/internal/model"
	"github.com/isucon/isucon2026/portal/tests/testkit"
)

func TestAdminDashboardEarliestUnansweredClarification(t *testing.T) {
	p := testkit.Start(t)
	now := time.Date(2026, 10, 28, 6, 18, 2, 370901000, time.UTC)
	oldest := now.Add(-30 * time.Minute)
	newest := now.Add(-2 * time.Minute)
	insert := func(createdAt time.Time, admin bool, answeredAt any) int64 {
		t.Helper()
		var id int64
		if err := p.DB.QueryRow(`INSERT INTO clarifications (created_at, updated_at, admin, answered_at)
			VALUES ($1, $1, $2, $3) RETURNING id`, createdAt, admin, answeredAt).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	// Older answered questions and admin announcements must not affect the summary.
	insert(now.Add(-time.Hour), false, now)
	insert(now.Add(-2*time.Hour), true, nil)
	oldestID := insert(oldest, false, nil)
	newestID := insert(newest, false, nil)
	headers := testkit.TimingHeaders(now)
	headers["X-Isux-Session-Staff"] = "1"

	for _, tc := range []struct {
		name     string
		answerID int64
		count    int64
		earliest time.Time
	}{
		{"multiple unanswered", 0, 2, oldest},
		{"oldest answered", oldestID, 1, newest},
		{"none unanswered", newestID, 0, time.Time{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.answerID != 0 {
				if _, err := p.DB.Exec(`UPDATE clarifications SET answered_at = $1 WHERE id = $2`, now, tc.answerID); err != nil {
					t.Fatal(err)
				}
			}
			res := p.DoPB(t, http.MethodGet, "/api/admin/dashboard", nil, headers)
			if res.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, body = %s", res.StatusCode, readAll(t, res))
			}
			if got := res.Header.Get("Cache-Control"); got != "no-store" {
				t.Fatalf("Cache-Control = %q, want no-store", got)
			}
			body := &pbadmin.DashboardResponse{}
			testkit.DecodePB(t, res, body)
			if body.UnansweredClarificationCount != tc.count {
				t.Errorf("unanswered count = %d, want %d", body.UnansweredClarificationCount, tc.count)
			}
			got := body.EarliestUnansweredClarificationAt
			if tc.earliest.IsZero() {
				if got != nil {
					t.Errorf("earliest = %v, want absent timestamp", got)
				}
			} else if got == nil || !got.AsTime().Equal(tc.earliest) {
				t.Errorf("earliest = %v, want %v", got, tc.earliest)
			}
		})
	}
}

func TestAdminDashboardPendingJobs(t *testing.T) {
	p := testkit.Start(t)
	now := time.Now().UTC().Truncate(time.Second)
	oldest := now.Add(-2 * time.Minute)
	for i, row := range []struct {
		status int32
		at     time.Time
	}{
		{model.JobPending, oldest}, {model.JobPending, now},
		{model.JobRunning, now.Add(-time.Hour)}, {model.JobErrored, now.Add(-2 * time.Hour)},
		{model.JobCancelled, now.Add(-3 * time.Hour)}, {model.JobFinished, now.Add(-4 * time.Hour)},
	} {
		teamID, _ := p.CreateTeam(t, fmt.Sprintf("pending-summary-%d", i))
		if _, err := p.DB.Exec(`INSERT INTO benchmark_jobs (team_id, status, created_at, updated_at) VALUES ($1, $2, $3, $3)`, teamID, row.status, row.at); err != nil {
			t.Fatal(err)
		}
	}
	for _, count := range []int64{2, 0} {
		res := p.DoPB(t, http.MethodGet, "/api/admin/dashboard", nil, map[string]string{"X-Isux-Session-Staff": "1"})
		if res.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", res.StatusCode)
		}
		if got := res.Header.Get("Cache-Control"); got != "no-store" {
			t.Fatalf("Cache-Control = %q, want no-store", got)
		}
		body := &pbadmin.DashboardResponse{}
		testkit.DecodePB(t, res, body)
		if body.PendingBenchmarkJobCount != count {
			t.Fatalf("pending = %d, want %d", body.PendingBenchmarkJobCount, count)
		}
		if count > 0 {
			if body.EarliestPendingBenchmarkJobAt == nil || !body.EarliestPendingBenchmarkJobAt.AsTime().Equal(oldest) {
				t.Fatalf("earliest = %v, want %v", body.EarliestPendingBenchmarkJobAt, oldest)
			}
		} else if body.EarliestPendingBenchmarkJobAt != nil {
			t.Fatal("expected no earliest pending timestamp")
		}
		if _, err := p.DB.Exec(`UPDATE benchmark_jobs SET status = 1 WHERE status = 0`); err != nil {
			t.Fatal(err)
		}
	}
}
