package concurrency_test

import (
	"context"
	"sync"
	"testing"

	"github.com/isucon/isucon2026/portal/internal/db"
	"github.com/isucon/isucon2026/portal/tests/testkit"
)

// ローリング更新で portal が 2 プロセス同時に起動しても（PORTAL_AUTO_MIGRATE=1）、
// スキーマ適用が競合してエラーにならないこと。Migrate は advisory lock で直列化する。
func TestMigrateConcurrentlyIsSafe(t *testing.T) {
	conn := testkit.Database(t)
	ctx := context.Background()

	// 空の状態から同時に流す方が競合しやすいので、一度全テーブルを消す
	for _, table := range []string{
		"schema_migrations", "registration_records", "registration_source_records",
		"benchmark_executions", "benchmark_jobs", "benchmark_results", "clarifications",
		"contestant_instances", "contestants", "coupons", "env_checks", "extra_time_assignments",
		"instance_command_execute_request_results", "instance_command_execute_requests",
		"notifications", "push_subscriptions", "ssh_public_keys", "survey_responses", "teams", "jobs",
	} {
		if _, err := conn.ExecContext(ctx, `DROP TABLE IF EXISTS `+table+` CASCADE`); err != nil {
			t.Fatal(err)
		}
	}

	const workers = 8
	errs := make(chan error, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			errs <- db.Migrate(ctx, conn)
		}()
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent migrate failed: %v", err)
		}
	}

	var n int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM teams`).Scan(&n); err != nil {
		t.Fatalf("schema must be applied: %v", err)
	}
}
