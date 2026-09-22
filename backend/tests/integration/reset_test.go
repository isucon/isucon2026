package integration_test

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/isucon/isucon2026/backend/internal/schema"
	"github.com/isucon/isucon2026/backend/internal/seed"
	"github.com/isucon/isucon2026/backend/tests/testkit"
)

func TestMinimalSeedIsRepeatable(t *testing.T) {
	pool := testkit.Database(t)
	_, filename, _, _ := runtime.Caller(0)
	schemaPath := filepath.Join(filepath.Dir(filename), "..", "..", "schema", "schema.sql")
	for iteration := 0; iteration < 2; iteration++ {
		if err := seed.Minimal(context.Background(), pool, schemaPath); err != nil {
			t.Fatal(err)
		}
		var users, administrators, settings int
		if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM users`).Scan(&users); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM administrators`).Scan(&administrators); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM app_settings`).Scan(&settings); err != nil {
			t.Fatal(err)
		}
		if users != 4 || administrators != 1 || settings != 1 {
			t.Fatalf("iteration %d counts: users=%d admins=%d settings=%d", iteration, users, administrators, settings)
		}
		var invalidConstraints int
		if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM users WHERE account_type<>'regular' OR status<>'active' OR username<>username_normalized`).Scan(&invalidConstraints); err != nil {
			t.Fatal(err)
		}
		if invalidConstraints != 0 {
			t.Fatalf("invalid seeded users = %d", invalidConstraints)
		}
	}
}

func TestMinimalSeedIfEmptyInitializesOnceAndPreservesData(t *testing.T) {
	pool := testkit.Database(t)
	_, filename, _, _ := runtime.Caller(0)
	schemaPath := filepath.Join(filepath.Dir(filename), "..", "..", "schema", "schema.sql")
	ctx := context.Background()

	if err := schema.Reset(ctx, pool, schemaPath); err != nil {
		t.Fatal(err)
	}
	if err := seed.MinimalIfEmpty(ctx, pool, schemaPath); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET display_name='Compose User' WHERE username='alice'`); err != nil {
		t.Fatal(err)
	}
	if err := seed.MinimalIfEmpty(ctx, pool, schemaPath); err != nil {
		t.Fatal(err)
	}

	var users int
	var displayName string
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT display_name FROM users WHERE username='alice'`).Scan(&displayName); err != nil {
		t.Fatal(err)
	}
	if users != 4 || displayName != "Compose User" {
		t.Fatalf("users=%d alice display_name=%q", users, displayName)
	}
}
