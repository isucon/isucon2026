package integration_test

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/migrate"
	"github.com/jackc/pgx/v5/pgxpool"
)

var testNamespace = uuid.MustParse("00000000-0000-0000-0000-000000000001")

func resetFoundation(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	_, filename, _, _ := runtime.Caller(0)
	directory := filepath.Join(filepath.Dir(filename), "..", "..", "migrations")
	if err := migrate.Reset(ctx, pool, directory); err != nil {
		t.Fatal(err)
	}
}
