package integration_test

import (
	"context"
	"testing"

	"github.com/isucon/isucon2026/portal/internal/db"
	"github.com/isucon/isucon2026/portal/tests/testkit"
)

// CT-8: isucon14-portal の 16 テーブルがそのまま作られ、再適用も安全であること。
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
	}
	for _, table := range expected {
		var n int
		if err := conn.QueryRow(`
			SELECT COUNT(*) FROM information_schema.tables
			WHERE table_schema = DATABASE() AND table_name = ?`, table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("table %s is missing", table)
		}
	}
}
