package integration_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/isucon/isucon2026/backend/internal/schema"
	"github.com/isucon/isucon2026/backend/internal/seed"
	"github.com/isucon/isucon2026/backend/tests/testkit"
)

func schemaPath(t *testing.T) string {
	t.Helper()
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "..", "schema", "schema.sql")
}

func TestSchemaResetIsRepeatableAndSeedCleanly(t *testing.T) {
	ctx := context.Background()
	pool := testkit.Database(t)
	path := schemaPath(t)
	if err := schema.Reset(ctx, pool, path); err != nil {
		t.Fatalf("first reset: %v", err)
	}
	if err := schema.Reset(ctx, pool, path); err != nil {
		t.Fatalf("second reset: %v", err)
	}
	if err := seed.Minimal(ctx, pool, path); err != nil {
		t.Fatalf("clean seed: %v", err)
	}
	var users int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if users != 4 {
		t.Fatalf("users=%d", users)
	}
	var migrationsTable bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.schema_migrations') IS NOT NULL`).Scan(&migrationsTable); err != nil {
		t.Fatal(err)
	}
	if migrationsTable {
		t.Fatal("schema_migrations table is still present")
	}
}

func TestSchemaConstraintsAndIndexesArePresent(t *testing.T) {
	ctx := context.Background()
	pool := testkit.Database(t)
	wantedConstraints := []string{"users_username_normalized_key", "posting_windows_region_local_date_key", "notifications_user_id_posting_window_id_key", "push_attempts_notification_id_subscription_id_key"}
	for _, name := range wantedConstraints {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conname=$1)`, name).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Errorf("missing constraint %s", name)
		}
	}
	wantedIndexes := []string{"login_sessions_one_active", "idempotency_expiry", "notifications_user_list", "push_attempts_claim"}
	for _, name := range wantedIndexes {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_indexes WHERE schemaname='public' AND indexname=$1)`, name).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Errorf("missing index %s", name)
		}
	}
}

func TestFailedSchemaResetRollsBack(t *testing.T) {
	ctx := context.Background()
	pool := testkit.Database(t)
	broken := filepath.Join(t.TempDir(), "schema.sql")
	body := []byte("CREATE TABLE rollback_probe(id bigint PRIMARY KEY); INSERT INTO table_that_does_not_exist(id) VALUES(1);\n")
	if err := os.WriteFile(broken, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := schema.Reset(ctx, pool, broken); err == nil {
		t.Fatal("broken schema unexpectedly succeeded")
	}
	var probe, users bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.rollback_probe') IS NOT NULL`).Scan(&probe); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.users') IS NOT NULL`).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if probe || !users {
		t.Fatalf("failed schema reset leaked probe=%v users=%v", probe, users)
	}
}
