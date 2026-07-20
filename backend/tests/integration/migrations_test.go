package integration_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/isucon/isucon2026/backend/internal/migrate"
	"github.com/isucon/isucon2026/backend/internal/seed"
	"github.com/isucon/isucon2026/backend/tests/testkit"
)

func migrationDirectory(t *testing.T) string {
	t.Helper()
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "..", "migrations")
}

func TestMigrationsAreForwardOnlyRepeatableAndSeedCleanly(t *testing.T) {
	ctx := context.Background()
	pool := testkit.Database(t)
	directory := migrationDirectory(t)
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".down.sql") {
			t.Fatalf("down migration is not allowed: %s", entry.Name())
		}
	}
	if err := migrate.Up(ctx, pool, directory); err != nil {
		t.Fatalf("repeated up: %v", err)
	}
	if err := migrate.Reset(ctx, pool, directory); err != nil {
		t.Fatalf("first reset: %v", err)
	}
	if err := migrate.Reset(ctx, pool, directory); err != nil {
		t.Fatalf("second reset: %v", err)
	}
	if err := seed.Minimal(ctx, pool, directory); err != nil {
		t.Fatalf("clean seed: %v", err)
	}
	var migrations, users int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&migrations); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if migrations != 7 || users != 4 {
		t.Fatalf("migrations=%d users=%d", migrations, users)
	}
}

func TestMigrationConstraintsAndIndexesArePresent(t *testing.T) {
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

func TestFailedUpgradeRollsBackWholeMigration(t *testing.T) {
	ctx := context.Background()
	pool := testkit.Database(t)
	temporary := t.TempDir()
	entries, err := os.ReadDir(migrationDirectory(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".up.sql") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(migrationDirectory(t), entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(temporary, entry.Name()), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	broken := `CREATE TABLE rollback_probe(id bigint PRIMARY KEY); INSERT INTO table_that_does_not_exist(id) VALUES(1);`
	if err := os.WriteFile(filepath.Join(temporary, "000008_broken.up.sql"), []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Up(ctx, pool, temporary); err == nil {
		t.Fatal("broken migration unexpectedly succeeded")
	}
	var tableExists, versionExists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.rollback_probe') IS NOT NULL`).Scan(&tableExists); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=8)`).Scan(&versionExists); err != nil {
		t.Fatal(err)
	}
	if tableExists || versionExists {
		t.Fatalf("failed migration leaked table=%v version=%v", tableExists, versionExists)
	}
}
