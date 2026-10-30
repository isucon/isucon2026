package integration_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	pb "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/resources"
	"github.com/isucon/isucon2026/portal/internal/external"
	"github.com/isucon/isucon2026/portal/internal/job"
	"github.com/isucon/isucon2026/portal/internal/model"
	"github.com/isucon/isucon2026/portal/tests/testkit"
)

type captureCommands struct{ count int }

func (q *captureCommands) Enqueue(context.Context, string, any) error { q.count++; return nil }

func TestRestartIncludesRouterWithoutExposingItAsContestant(t *testing.T) {
	p := testkit.Start(t)
	team, _ := p.CreateTeam(t, "restart-router")
	ctx := context.Background()
	if err := model.RegisterTeamRouter(ctx, p.DB, team, "i-router", "192.168.10.10", "203.0.113.5"); err != nil {
		t.Fatal(err)
	}
	for n := int64(1); n <= 4; n++ {
		if err := model.UpsertInstance(ctx, p.DB, team, n, fmt.Sprintf("i-%d", n), pb.ContestantInstance_RUNNING, fmt.Sprintf("192.168.%d.10", n), sql.NullString{String: fmt.Sprintf("203.0.113.%d", n), Valid: true}); err != nil {
			t.Fatal(err)
		}
	}
	q := &captureCommands{}
	env := &job.Env{DB: p.DB, SSH: &external.SSHExecutor{PrivateKeyPath: "/nonexistent-test-key"}}
	env.SetQueue(q)
	if err := env.ExecuteOnMultipleContestantInstances(ctx, []int64{team}, "sudo reboot", true); err != nil {
		t.Fatal(err)
	}
	requests, err := model.ListCommandRequests(ctx, p.DB)
	if err != nil {
		t.Fatal(err)
	}
	results, err := model.ListCommandRequestResults(ctx, p.DB, requests[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 5 || q.count != 1 {
		t.Fatalf("expected router first, then four servers: %d/%d", len(results), q.count)
	}
	var routerResult *model.InstanceCommandExecuteRequestResult
	for _, r := range results {
		if r.RouterTeamID.Int64 == team {
			routerResult = r
		}
	}
	if routerResult == nil {
		t.Fatal("router target missing")
	}
	if err := env.ExecuteOnContestantInstance(ctx, routerResult.ID); err != nil {
		t.Fatal(err)
	}
	if q.count != 5 {
		t.Fatalf("service commands were not queued after router: %d", q.count)
	}
	done, err := model.FindCommandRequestResult(ctx, p.DB, routerResult.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !done.FinishedAt.Valid || done.ExitCode.Int64 != -1 {
		t.Fatal("router SSH failure was not recorded")
	}

	instances, err := model.TeamInstances(ctx, p.DB, team)
	if err != nil || len(instances) != 4 {
		t.Fatalf("router exposed: %v %v", instances, err)
	}
}
