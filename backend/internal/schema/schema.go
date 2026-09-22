package schema

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func Load(path string) (string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read schema: %w", err)
	}
	sql := string(body)
	if strings.TrimSpace(sql) == "" {
		return "", fmt.Errorf("schema %s is empty", path)
	}
	return sql, nil
}

// Apply executes schema.sql in one transaction. The target database must not
// already contain those objects.
func Apply(ctx context.Context, pool *pgxpool.Pool, path string) error {
	sql, err := Load(path)
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, sql); err != nil {
			return fmt.Errorf("apply schema: %w", err)
		}
		return nil
	})
}

// Reset drops the public schema and applies schema.sql in one transaction.
func Reset(ctx context.Context, pool *pgxpool.Pool, path string) error {
	sql, err := Load(path)
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
			return fmt.Errorf("reset schema: %w", err)
		}
		if _, err := tx.Exec(ctx, sql); err != nil {
			return fmt.Errorf("apply schema: %w", err)
		}
		return nil
	})
}

// Ensure applies schema.sql when the application tables are absent.
func Ensure(ctx context.Context, pool *pgxpool.Pool, path string) error {
	var ready bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.users') IS NOT NULL`).Scan(&ready); err != nil {
		return err
	}
	if ready {
		return nil
	}
	return Apply(ctx, pool, path)
}
