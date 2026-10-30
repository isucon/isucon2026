package integration_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"testing"

	"github.com/isucon/isucon2026/portal/internal/config"
	"github.com/isucon/isucon2026/portal/internal/contest"
	"github.com/isucon/isucon2026/portal/internal/model"
	"github.com/isucon/isucon2026/portal/tests/testkit"
)

func TestValidationEvidenceIsPrivateAndWriteOnce(t *testing.T) {
	p := testkit.Start(t)
	team, _ := p.CreateTeam(t, "evidence")
	ctx := context.Background()
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	j, err := model.CreateJob(ctx, tx, team, "worker", sql.NullInt64{}, sql.NullInt64{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = model.StartJob(ctx, tx, j, "worker"); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	request := func(token string, want int) {
		t.Helper()
		req, err := http.NewRequest(http.MethodPut, fmt.Sprintf("%s/api/bench/validation/%d", p.Server.URL, j.ID), bytes.NewBufferString(`{"version":1,"users":[{"secret_key":"private"}]}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		res, err := p.Server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != want {
			t.Fatalf("status=%d want %d", res.StatusCode, want)
		}
	}
	request("wrong", 401)
	request(j.Handle.String, 204)
	request(j.Handle.String, 409)
	if _, err := p.DB.Exec(`UPDATE benchmark_jobs SET status=4 WHERE id=$1`, j.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.DB.Exec(`INSERT INTO benchmark_results(team_id,benchmark_job_id,finished,passed,marked_at,created_at,updated_at) VALUES($1,$2,true,true,now(),now(),now())`, team, j.ID); err != nil {
		t.Fatal(err)
	}
	tx, err = p.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	validation, err := model.CreateJob(ctx, tx, team, "worker", sql.NullInt64{}, sql.NullInt64{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`UPDATE benchmark_jobs SET validation_source_job_id=$1 WHERE id=$2`, j.ID, validation.ID); err != nil {
		t.Fatal(err)
	}
	if err = model.StartJob(ctx, tx, validation, "worker"); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	read := func(handle string, want int) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/api/bench/validation/%d", p.Server.URL, validation.ID), nil)
		req.Header.Set("Authorization", "Bearer "+handle)
		res, err := p.Server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != want {
			t.Fatalf("GET=%d want=%d", res.StatusCode, want)
		}
	}
	read(j.Handle.String, 401)
	read(validation.Handle.String, 200)
	if _, err = p.DB.Exec(`UPDATE benchmark_results SET passed=false WHERE benchmark_job_id=$1`, j.ID); err != nil {
		t.Fatal(err)
	}
	read(validation.Handle.String, 409)

}

func TestPersistenceValidationDoesNotChangeCompetitionScores(t *testing.T) {
	p := testkit.Start(t)
	team, _ := p.CreateTeam(t, "validation-score")
	var sourceResult int64
	for _, validation := range []bool{false, true} {
		var jobID, resultID int64
		if err := p.DB.QueryRow(`INSERT INTO benchmark_jobs(team_id,status,post_validation,created_at,updated_at) VALUES($1,4,$2,now(),now()) RETURNING id`, team, validation).Scan(&jobID); err != nil {
			t.Fatal(err)
		}
		score := 100
		if validation {
			score = 9999
		}
		if err := p.DB.QueryRow(`INSERT INTO benchmark_results(team_id,benchmark_job_id,score,finished,passed,exit_status,marked_at,created_at,updated_at) VALUES($1,$2,$3,true,true,0,now(),now(),now()) RETURNING id`, team, jobID, score).Scan(&resultID); err != nil {
			t.Fatal(err)
		}
		if !validation {
			sourceResult = resultID
		}
	}
	if err := contest.MaintainBestForTeam(t.Context(), p.DB, config.Timing{}, team, nil); err != nil {
		t.Fatal(err)
	}
	var best int64
	if err := p.DB.QueryRow(`SELECT best_benchmark_result_for_admin_id FROM teams WHERE id=$1`, team).Scan(&best); err != nil {
		t.Fatal(err)
	}
	if best != sourceResult {
		t.Fatal("persistence check entered competition ranking")
	}
}
