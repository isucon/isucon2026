package main

import (
	"context"
	pbadmin "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/services/admin"
	"net/http"
	"testing"

	"github.com/isucon/isucon2026/portal/tests/testkit"
)

func TestDebugCommandRequiresOptIn(t *testing.T) {
	t.Setenv("PORTAL_LOCAL_DEBUG", "")
	if err := run([]string{"register"}); err == nil {
		t.Fatal("must reject without explicit local environment")
	}
}

func TestRegisterIsIdempotentAndRefreshesAddresses(t *testing.T) {
	conn := testkit.Database(t)
	ctx := context.Background()
	first, err := register(ctx, conn, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := register(ctx, conn, "127.0.0.2")
	if err != nil {
		t.Fatal(err)
	}
	if first.TeamID != second.TeamID || first.ContestantID != second.ContestantID {
		t.Fatal("duplicate fixture")
	}
	var count int
	if err := conn.QueryRow(`SELECT count(*) FROM contestant_instances WHERE team_id=$1 AND public_ipv4_address='127.0.0.2'`, first.TeamID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("instances: %d", count)
	}
	if _, err := register(ctx, conn, "not-an-ip"); err == nil {
		t.Fatal("invalid IP accepted")
	}
	var teams int
	if err := conn.QueryRow(`SELECT count(*) FROM teams`).Scan(&teams); err != nil {
		t.Fatal(err)
	}
	if teams != 1 {
		t.Fatalf("teams: %d", teams)
	}
}

func TestDebugEnqueueUsesAuthenticatedAPI(t *testing.T) {
	p := testkit.Start(t)
	ctx := context.Background()
	f, err := register(ctx, p.DB, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	result := &pbadmin.EnqueueBenchmarkJobResponse{}
	req := &pbadmin.EnqueueBenchmarkJobRequest{TeamId: f.TeamID, TargetNumber: 1}
	if err := callAPI(ctx, p.Cfg, p.Server.URL, http.MethodPost, "/api/admin/benchmark_jobs", req, result); err != nil {
		t.Fatal(err)
	}
	if result.Job == nil || result.Job.TeamId != f.TeamID || result.Job.TargetId == 0 {
		t.Fatalf("wrong job: %v", result.Job)
	}
	if err := callAPI(ctx, p.Cfg, p.Server.URL, http.MethodPost, "/api/admin/benchmark_jobs", req, &pbadmin.EnqueueBenchmarkJobResponse{}); err == nil {
		t.Fatal("duplicate enqueue accepted")
	}
}
