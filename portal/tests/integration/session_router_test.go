package integration_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	common "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/services/common"
	"github.com/isucon/isucon2026/portal/tests/testkit"
)

func TestSessionRouterAddress(t *testing.T) {
	p := testkit.Start(t)
	teamID, leader := p.CreateTeam(t, "router-owner")
	_, otherLeader := p.CreateTeam(t, "other-team")
	now := time.Now()
	p.Cfg.Timing.ContestStart = pointer(now.Add(-time.Hour))
	p.Cfg.Timing.ContestEnd = pointer(now.Add(time.Hour))
	check := func(contestantID int64, want string) {
		t.Helper()
		headers := map[string]string{}
		if contestantID != 0 {
			headers["X-Isux-Session-Contestant-Id"] = fmt.Sprint(contestantID)
		}
		res := p.DoPB(t, http.MethodGet, "/api/session", nil, headers)
		got := &common.GetCurrentSessionResponse{}
		testkit.DecodePB(t, res, got)
		if got.GetRouterPublicIpv4Address() != want {
			t.Fatalf("router = %q, want %q", got.GetRouterPublicIpv4Address(), want)
		}
		if len(got.ContestantInstances) != 0 {
			t.Fatal("router must not enter contestant instances")
		}
	}
	check(leader, "")
	if _, err := p.DB.Exec(`INSERT INTO team_routers(team_id) VALUES($1)`, teamID); err != nil {
		t.Fatal(err)
	}
	check(leader, "")
	if _, err := p.DB.Exec(`UPDATE team_routers SET public_ipv4_address='203.0.113.5' WHERE team_id=$1`, teamID); err != nil {
		t.Fatal(err)
	}
	check(leader, "203.0.113.5")
	check(otherLeader, "")
	check(0, "")
	p.Cfg.Timing.ContestStart = pointer(now.Add(time.Hour))
	check(leader, "")
}
