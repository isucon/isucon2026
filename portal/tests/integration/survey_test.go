package integration_test

import (
	"testing"
	"time"

	pb "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/resources"
	"github.com/isucon/isucon2026/portal/internal/db"
	"github.com/isucon/isucon2026/portal/internal/job"
	"github.com/isucon/isucon2026/portal/internal/model"
	"github.com/isucon/isucon2026/portal/tests/testkit"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestSurveyLatestAndHistory(t *testing.T) {
	conn := testkit.Database(t)
	var teamID int64
	if err := conn.QueryRow(`INSERT INTO teams(name,email_address,invite_token,student,created_at,updated_at) VALUES('survey','survey@test.invalid','survey',false,now(),now()) RETURNING id`).Scan(&teamID); err != nil {
		t.Fatal(err)
	}
	for i, data := range []string{
		`{"language":"go","agent":"human","harness":"manual"}`,
		`{"language":"rust","agent":"model-a","harness":"tool-a"}`,
		`{"language":"go"}`,
	} {
		var jobID int64
		if err := conn.QueryRow(`INSERT INTO benchmark_jobs(team_id,status,created_at,updated_at) VALUES($1,1,now(),now()) RETURNING id`, teamID).Scan(&jobID); err != nil {
			t.Fatal(err)
		}
		survey := new(pb.SurveyResponse)
		if err := protojson.Unmarshal([]byte(data), survey); err != nil {
			t.Fatal(err)
		}
		for _, finished := range []bool{false, false, true} {
			job := &model.BenchmarkJob{ID: jobID, TeamID: teamID, Status: model.JobRunning}
			tx, err := conn.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			reported := survey
			if !finished {
				reported = &pb.SurveyResponse{Language: survey.Language}
			}
			result := &pb.BenchmarkResult{Finished: finished, Passed: false, MarkedAt: timestamppb.Now(), SurveyResponse: reported, Execution: &pb.BenchmarkResult_Execution{}}
			if err := model.SubmitResultFromPB(t.Context(), tx, job, result, time.Now()); err != nil {
				tx.Rollback()
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
		}
		var latestJob int64
		var language, agent, harness string
		if err := conn.QueryRow(`SELECT benchmark_job_id,language,agent,harness FROM survey_responses WHERE team_id=$1`, teamID).Scan(&latestJob, &language, &agent, &harness); err != nil {
			t.Fatal(err)
		}
		wantAgent, wantHarness := "", ""
		if i == 0 {
			wantAgent, wantHarness = "human", "manual"
		}
		if i == 1 {
			wantAgent, wantHarness = "model-a", "tool-a"
		}
		if latestJob != jobID || language != survey.Language || agent != wantAgent || harness != wantHarness {
			t.Fatalf("latest: %d %s %s %s", latestJob, language, agent, harness)
		}
		var count int
		if err := conn.QueryRow(`SELECT count(*) FROM survey_response_history WHERE team_id=$1`, teamID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != i+1 {
			t.Fatalf("history count=%d want %d", count, i+1)
		}
	}

	// Legacy benchmarks may report an empty survey before initialization.
	var emptyJobID int64
	if err := conn.QueryRow(`INSERT INTO benchmark_jobs(team_id,status,created_at,updated_at) VALUES($1,1,now(),now()) RETURNING id`, teamID).Scan(&emptyJobID); err != nil {
		t.Fatal(err)
	}
	for _, survey := range []*pb.SurveyResponse{nil, {}} {
		tx, err := conn.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		result := &pb.BenchmarkResult{MarkedAt: timestamppb.Now(), SurveyResponse: survey}
		if err := model.SubmitResultFromPB(t.Context(), tx, &model.BenchmarkJob{ID: emptyJobID, TeamID: teamID, Status: model.JobRunning}, result, time.Now()); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	var latestJobID int64
	var historyCount int
	if err := conn.QueryRow(`SELECT benchmark_job_id FROM survey_responses WHERE team_id=$1`, teamID).Scan(&latestJobID); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(`SELECT count(*) FROM survey_response_history WHERE team_id=$1`, teamID).Scan(&historyCount); err != nil {
		t.Fatal(err)
	}
	if latestJobID == emptyJobID || historyCount != 3 {
		t.Fatal("empty survey overwrote collected data")
	}
	var language, agent, harness string
	var created, updated time.Time
	if err := conn.QueryRow(`SELECT language,agent,harness,created_at,updated_at FROM survey_response_history WHERE team_id=$1 ORDER BY benchmark_job_id LIMIT 1`, teamID).Scan(&language, &agent, &harness, &created, &updated); err != nil {
		t.Fatal(err)
	}
	if language != "go" || agent != "human" || harness != "manual" || updated.Before(created) {
		t.Fatal("first survey snapshot was lost")
	}
	env := &job.Env{DB: conn}
	if err := env.ClearContestData(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"survey_responses", "survey_response_history"} {
		var count int
		if err := conn.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("contest reset left data in %s", table)
		}
	}
}

func TestSurveyMigrationPreservesLegacyData(t *testing.T) {
	conn := testkit.Database(t)
	// Reproduce the schema before this migration, including its existing survey.
	_, err := conn.Exec(`DROP TABLE IF EXISTS survey_response_history; ALTER TABLE survey_responses DROP COLUMN IF EXISTS agent, DROP COLUMN IF EXISTS harness; DELETE FROM schema_migrations WHERE version='005_survey_metadata.sql'; INSERT INTO survey_responses(team_id,benchmark_job_id,language,created_at,updated_at) VALUES(123,456,'ruby','2026-01-01','2026-01-02')`)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := db.Migrate(t.Context(), conn); err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range []string{"survey_responses", "survey_response_history"} {
		var count int
		if err := conn.QueryRow(`SELECT count(*) FROM ` + table + ` WHERE team_id=123 AND benchmark_job_id=456 AND language='ruby' AND agent='' AND harness='' AND created_at='2026-01-01' AND updated_at='2026-01-02'`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("%s: legacy data not preserved", table)
		}
	}
}
