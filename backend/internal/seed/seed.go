package seed

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/schema"
	"github.com/isucon/isucon2026/backend/internal/service"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var fixedTime = time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)

func FixedTime() time.Time { return fixedTime }

type fixtureUser struct{ ID, Username, DisplayName, Region string }

var users = []fixtureUser{
	{"00000000-0000-0000-0000-000000000101", "alice", "Alice", "asia"},
	{"00000000-0000-0000-0000-000000000102", "bob", "Bob", "asia"},
	{"00000000-0000-0000-0000-000000000103", "carol", "Carol", "asia"},
	{"00000000-0000-0000-0000-000000000104", "dave", "Dave", "europe"},
}

func Minimal(ctx context.Context, pool *pgxpool.Pool, schemaPath string) error {
	if err := schema.Reset(ctx, pool, schemaPath); err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		return insertMinimal(ctx, tx)
	})
}

// MinimalIfEmpty applies schema.sql when the tables are absent and installs
// the deterministic local fixture only when no application data exists. It is
// safe to run on every Docker Compose startup without erasing data from an
// existing environment.
func MinimalIfEmpty(ctx context.Context, pool *pgxpool.Pool, schemaPath string) error {
	if err := schema.Ensure(ctx, pool, schemaPath); err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(4953555245414)`); err != nil {
			return err
		}
		var hasData bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM users
			UNION ALL
			SELECT 1 FROM administrators
			UNION ALL
			SELECT 1 FROM app_settings
		)`).Scan(&hasData); err != nil {
			return err
		}
		if hasData {
			return nil
		}
		return insertMinimal(ctx, tx)
	})
}

func insertMinimal(ctx context.Context, tx pgx.Tx) error {
	userHash, err := service.HashPassword("password")
	if err != nil {
		return err
	}
	adminHash, err := service.HashPassword("admin-password")
	if err != nil {
		return err
	}
	for _, fixture := range users {
		id := uuid.MustParse(fixture.ID)
		if _, err := tx.Exec(ctx, `INSERT INTO users(id,username,username_normalized,password_hash,display_name,display_name_search,region,created_at,updated_at) VALUES($1,$2,$2,$3,$4,$5,$6,$7,$7)`, id, fixture.Username, userHash, fixture.DisplayName, service.NormalizeDisplayNameForSearch(fixture.DisplayName), fixture.Region, fixedTime); err != nil {
			return fmt.Errorf("seed user %s: %w", fixture.Username, err)
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO administrators(id,login_name_normalized,password_hash,enabled,created_at) VALUES($1,'root',$2,true,$3)`, uuid.MustParse("00000000-0000-0000-0000-000000000001"), adminHash, fixedTime); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO app_settings(key,value,updated_at) VALUES('fixture','"minimal"'::jsonb,$1)`, fixedTime)
	return err
}
