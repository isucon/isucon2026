package integration_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	admin "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/services/admin"
	"github.com/isucon/isucon2026/portal/internal/db"
	"github.com/isucon/isucon2026/portal/internal/model"
	"github.com/isucon/isucon2026/portal/tests/testkit"
)

func TestTeamOnsiteSupportExcludedHTTP(t *testing.T) {
	p := testkit.Start(t)
	id, leader := p.CreateTeam(t, "support-excluded")
	path := fmt.Sprintf("/api/admin/teams/%d", id)
	headers := map[string]string{"X-Isux-Session-Staff": "1"}
	get := func() *admin.GetTeamResponse {
		t.Helper()
		res := p.DoPB(t, http.MethodGet, path, nil, headers)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("GET team: %d", res.StatusCode)
		}
		body := &admin.GetTeamResponse{}
		testkit.DecodePB(t, res, body)
		return body
	}
	if get().Team.OnsiteSupportExcluded {
		t.Fatal("new team must have support enabled")
	}
	for _, excluded := range []bool{true, false} {
		team := get().Team
		team.OnsiteSupportExcluded = excluded
		res := p.DoPB(t, http.MethodPut, path, &admin.UpdateTeamRequest{Team: team}, headers)
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("update: %d", res.StatusCode)
		}
		if get().Team.OnsiteSupportExcluded != excluded {
			t.Fatal("detail did not retain flag")
		}
		list := &admin.ListTeamsResponse{}
		testkit.DecodePB(t, p.DoPB(t, http.MethodGet, "/api/admin/teams", nil, headers), list)
		if len(list.Teams) != 1 || list.Teams[0].OnsiteSupportExcluded != excluded {
			t.Fatalf("list: %v", list)
		}
		stored, err := model.FindTeam(context.Background(), p.DB, id)
		if err != nil {
			t.Fatal(err)
		}
		if stored.OnsiteSupportExcluded != excluded || !stored.Active() {
			t.Fatalf("stored team: %+v", stored)
		}
		contestant, err := model.FindActiveContestant(context.Background(), p.DB, leader)
		if err != nil || contestant == nil {
			t.Fatalf("participant lost access: %v", err)
		}
		count, err := model.ActiveVisibleTeamCount(context.Background(), p.DB)
		if err != nil || count != 1 {
			t.Fatalf("active count=%d: %v", count, err)
		}
	}
}

func TestSupportFlagMigrationExistingTeams(t *testing.T) {
	conn := testkit.Database(t)
	_, err := conn.Exec(`ALTER TABLE teams DROP COLUMN onsite_support_excluded;
 DELETE FROM schema_migrations WHERE version='007_team_onsite_support.sql';
 INSERT INTO teams(name,email_address,invite_token,student,created_at,updated_at) VALUES('existing','a@example.invalid','token',false,now(),now())`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := db.Migrate(context.Background(), conn); err != nil {
			t.Fatal(err)
		}
	}
	var excluded bool
	if err := conn.QueryRow(`SELECT onsite_support_excluded FROM teams WHERE name='existing'`).Scan(&excluded); err != nil {
		t.Fatal(err)
	}
	if excluded {
		t.Fatal("existing teams must default to supported")
	}
}
