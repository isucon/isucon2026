package integration_test

import (
	"context"
	"testing"

	"github.com/isucon/isucon2026/portal/internal/db"
	"github.com/isucon/isucon2026/portal/tests/testkit"
)

// CT-8: isucon14-portal の 16 テーブル + ジョブキュー用 jobs が作られ、再適用も安全であること。
func TestMigrationCreatesAllTablesAndIsIdempotent(t *testing.T) {
	conn := testkit.Database(t) // 中で Migrate 済み

	// 再適用しても壊れない（CREATE TABLE IF NOT EXISTS）
	if err := db.Migrate(context.Background(), conn); err != nil {
		t.Fatalf("re-applying schema must be safe: %v", err)
	}

	expected := []string{
		"benchmark_executions", "benchmark_jobs", "benchmark_results", "clarifications",
		"contestant_instances", "contestants", "coupons", "env_checks", "extra_time_assignments",
		"instance_command_execute_request_results", "instance_command_execute_requests",
		"notifications", "push_subscriptions", "ssh_public_keys", "survey_responses", "teams",
		"jobs",
	}
	for _, table := range expected {
		var n int
		if err := conn.QueryRow(`
			SELECT COUNT(*) FROM information_schema.tables
			WHERE table_schema = current_schema() AND table_name = $1`, table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("table %s is missing", table)
		}
	}
}

// DM-4: 同一チームに pending/running のジョブは 1 件まで（部分一意インデックス）。
func TestMigrationEnforcesOneActiveJobPerTeam(t *testing.T) {
	conn := testkit.Database(t)
	ctx := context.Background()

	var teamID int64
	if err := conn.QueryRowContext(ctx, `
		INSERT INTO teams (name, email_address, invite_token, student, created_at, updated_at)
		VALUES ('dm4', 'dm4@test.invalid', 'tok', false, now(), now()) RETURNING id`).Scan(&teamID); err != nil {
		t.Fatal(err)
	}
	insert := func(status int) error {
		_, err := conn.ExecContext(ctx, `
			INSERT INTO benchmark_jobs (team_id, status, created_at, updated_at) VALUES ($1, $2, now(), now())`, teamID, status)
		return err
	}
	if err := insert(0); err != nil { // pending
		t.Fatal(err)
	}
	if err := insert(1); err == nil { // running を重ねる → 一意違反
		t.Fatal("second active job for the same team must be rejected")
	}
	if err := insert(4); err != nil { // finished は何件でも良い
		t.Fatalf("closed jobs must not be limited: %v", err)
	}
}
