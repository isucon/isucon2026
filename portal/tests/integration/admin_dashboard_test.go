package integration_test

import (
	"net/http"
	"testing"
	"time"

	pbadmin "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/services/admin"
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
