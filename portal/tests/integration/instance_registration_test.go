package integration_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	pbadmin "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/services/admin"
	"net/http"
	"testing"
	"time"

	pbbench "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/services/bench"
	"github.com/isucon/isucon2026/portal/tests/testkit"
)

func TestInstanceRegistrationAndReplacement(t *testing.T) {
	p := testkit.Start(t)
	p.Cfg.AdminOnly = true
	teamID, _ := p.CreateTeam(t, "cfn2026")
	tok := checkerTokenParam(t, p, teamID)
	timing := testkit.TimingHeaders(time.Now())
	post := func(number int, id, ip, token string, status int) {
		t.Helper()
		data, _ := json.Marshal(map[string]any{"token": token, "number": number, "instance_id": id, "public_ipv4_address": ip, "private_ipv4_address": "192.168.11.10"})
		req, _ := http.NewRequest("POST", p.Server.URL+"/api/instance_registrations", bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		for k, v := range timing {
			req.Header.Set(k, v)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != status {
			t.Fatalf("registration status = %d, want %d", res.StatusCode, status)
		}
	}
	post(1, "i-0123456789abcdef0", "203.0.113.1", tok, 204)
	for n := 2; n <= 4; n++ {
		post(n, fmt.Sprintf("i-0123456789abcdef%d", n), fmt.Sprintf("203.0.113.%d", n), tok, 204)
	}
	var targetID int64
	if err := p.DB.QueryRow(`SELECT id FROM contestant_instances WHERE team_id=$1 AND number=1`, teamID).Scan(&targetID); err != nil {
		t.Fatal(err)
	}
	post(1, "i-0123456789abcdeff", "203.0.113.11", tok, 204)
	var replacedID int64
	if err := p.DB.QueryRow(`SELECT id FROM contestant_instances WHERE team_id=$1 AND number=1 AND cloud_id=$2 AND public_ipv4_address=$3`, teamID, "i-0123456789abcdeff", "203.0.113.11").Scan(&replacedID); err != nil {
		t.Fatal(err)
	}
	if replacedID != targetID {
		t.Fatal("recreation must preserve the portal instance ID")
	}
	if _, err := p.DB.Exec(`INSERT INTO benchmark_jobs(team_id,target_id,status,created_at,updated_at) VALUES($1,$2,0,now(),now())`, teamID, targetID); err != nil {
		t.Fatal(err)
	}
	receive := func() *pbbench.ReceiveBenchmarkJobResponse {
		t.Helper()
		res := p.DoPB(t, "POST", "/api/bench/receive", &pbbench.ReceiveBenchmarkJobRequest{Token: p.Cfg.BenchToken}, nil)
		got := &pbbench.ReceiveBenchmarkJobResponse{}
		testkit.DecodePB(t, res, got)
		return got
	}
	if receive().JobHandle != nil {
		t.Fatal("must wait for Router registration")
	}
	post(5, "i-0123456789abcdef0", "203.0.113.5", tok, 204)
	post(5, "i-0123456789abcdef1", "203.0.113.6", tok, 204)
	var cloud, public, private string
	if err := p.DB.QueryRow(`SELECT cloud_id,public_ipv4_address,private_ipv4_address FROM team_routers WHERE team_id=$1`, teamID).Scan(&cloud, &public, &private); err != nil {
		t.Fatal(err)
	}
	if cloud != "i-0123456789abcdef1" || public != "203.0.113.6" || private != "192.168.11.10" {
		t.Fatalf("replacement = %s %s %s", cloud, public, private)
	}
	var count int
	if err := p.DB.QueryRow(`SELECT count(*) FROM contestant_instances WHERE team_id=$1`, teamID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 4 {
		t.Fatal("Router must not enter the portal's server list")
	}
	list := &pbadmin.ListContestantInstancesResponse{}
	res := p.DoPB(t, "GET", "/api/admin/contestant_instances", nil, map[string]string{"X-Isux-Session-Staff": "1"})
	testkit.DecodePB(t, res, list)
	if len(list.ContestantInstances) != 4 {
		t.Fatalf("visible instances = %v", list.ContestantInstances)
	}
	for _, instance := range list.ContestantInstances {
		if instance.Number > 4 || instance.PublicIpv4Address == "203.0.113.6" {
			t.Fatal("Router is visible in the portal")
		}
	}
	got := receive().JobHandle
	if got == nil || got.TargetIpv4Address != "203.0.113.6" || len(got.AllIpv4Addresses) != 4 || len(got.ExtraCommandArgs) < 2 || got.ExtraCommandArgs[0] != "--host" {
		t.Fatalf("benchmark routing = %v", got)
	}
	post(5, "i-0123456789abcdef1", "203.0.113.7", "invalid", 401)
	post(6, "i-0123456789abcdef1", "203.0.113.7", tok, 400)
	post(1, "invalid", "203.0.113.7", tok, 400)
	post(1, "i-0123456789abcdef1", "not-an-ip", tok, 400)
	timing["X-Isux-Timing-Contest-End"] = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	post(5, "i-0123456789abcdef1", "203.0.113.8", tok, 409)
	var checks int
	if err := p.DB.QueryRow(`SELECT count(*) FROM env_checks WHERE team_id=$1`, teamID).Scan(&checks); err != nil {
		t.Fatal(err)
	}
	if checks != 0 {
		t.Fatal("registration must not record a successful environment validation")
	}
}
