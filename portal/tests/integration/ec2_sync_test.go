package integration_test

import (
	"context"
	"testing"

	resources "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/resources"
	bench "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/services/bench"
	"github.com/isucon/isucon2026/portal/internal/external"
	"github.com/isucon/isucon2026/portal/internal/job"
	"github.com/isucon/isucon2026/portal/internal/model"
	"github.com/isucon/isucon2026/portal/tests/testkit"
)

func TestEC2SyncPreservesLegacyHostAndStoppedState(t *testing.T) {
	p := testkit.Start(t)
	ctx := context.Background()
	team, _ := p.CreateTeam(t, "legacy")
	e := &job.Env{DB: p.DB}
	instances := []external.EC2Instance{}
	for n := int64(1); n <= 5; n++ {
		instances = append(instances, external.EC2Instance{TeamID: team, Number: n, InstanceID: string(rune('a' + n)), PrivateIPAddress: "192.168.1.10", PublicIPAddress: "203.0.113.10", State: "running", BenchmarkHost: "app.team7-aws.127.0.0.1.nip.io"})
	}
	if err := e.SyncEC2Instances(ctx, instances); err != nil {
		t.Fatal(err)
	}
	servers, err := model.TeamInstances(ctx, p.DB, team)
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 4 {
		t.Fatalf("visible servers=%d", len(servers))
	}
	target := servers[0].ID
	if _, err := p.DB.Exec(`INSERT INTO benchmark_jobs(team_id,target_id,status,created_at,updated_at) VALUES($1,$2,0,now(),now())`, team, target); err != nil {
		t.Fatal(err)
	}
	res := p.DoPB(t, "POST", "/api/bench/receive", &bench.ReceiveBenchmarkJobRequest{Token: p.Cfg.BenchToken}, nil)
	got := &bench.ReceiveBenchmarkJobResponse{}
	testkit.DecodePB(t, res, got)
	if got.JobHandle == nil || got.JobHandle.TargetIpv4Address != "203.0.113.10" || got.JobHandle.ExtraCommandArgs[1] != "app.team7-aws.127.0.0.1.nip.io" {
		t.Fatalf("route=%v", got)
	}
	for n := range instances {
		instances[n].State = "stopped"
		instances[n].PublicIPAddress = ""
	}
	if err := e.SyncEC2Instances(ctx, instances); err != nil {
		t.Fatal(err)
	}
	servers, err = model.TeamInstances(ctx, p.DB, team)
	if err != nil {
		t.Fatal(err)
	}
	if servers[0].ID != target || servers[0].Status != int32(resources.ContestantInstance_STOPPED) || servers[0].PublicIPv4Address.Valid {
		t.Fatalf("stopped=%v", servers[0])
	}
	router, err := model.FindTeamRouter(ctx, p.DB, team)
	if err != nil || router.PublicIP.Valid {
		t.Fatalf("router=%v err=%v", router, err)
	}
	if err := e.SyncEC2Instances(ctx, append(instances, instances[0])); err == nil {
		t.Fatal("duplicate assignments must fail")
	}
	if err := model.RegisterTeamRouter(ctx, p.DB, team, "cfn-router", "192.168.1.1", "203.0.113.20"); err != nil {
		t.Fatal(err)
	}
	router, err = model.FindTeamRouter(ctx, p.DB, team)
	if err != nil || router.BenchmarkHost.Valid {
		t.Fatalf("CFn must reset legacy host: %v %v", router, err)
	}
}

func TestEnsureTrialTeamsDoesNotOverwriteExistingTeams(t *testing.T) {
	db := testkit.Database(t)
	ctx := context.Background()
	e := &job.Env{DB: db}
	if err := e.EnsureTrialTeams(ctx, []int64{8, 9, 10, 11, 12}); err != nil {
		t.Fatal(err)
	}
	if err := e.EnsureTrialTeams(ctx, []int64{8, 9, 10, 11, 12}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE teams SET name='existing team' WHERE id=8`); err != nil {
		t.Fatal(err)
	}
	if err := e.EnsureTrialTeams(ctx, []int64{8}); err == nil {
		t.Fatal("must reject occupied team ID")
	}
}
