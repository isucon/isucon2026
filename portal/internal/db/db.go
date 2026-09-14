// Package db は PostgreSQL 接続とスキーマ適用を提供する。
package db

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed schema.sql
var schema string

// Open は postgres:// 形式の URL で接続する。
// タイムスタンプの解釈を揺らさないため、URL に timezone が無ければ UTC を強制する。
func Open(databaseURL string) (*sql.DB, error) {
	if databaseURL == "" {
		return nil, errors.New("database url is empty")
	}
	conn, err := sql.Open("pgx", withUTC(databaseURL))
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(20)
	conn.SetMaxIdleConns(20)
	conn.SetConnMaxLifetime(5 * time.Minute)
	return conn, nil
}

func withUTC(databaseURL string) string {
	if containsParam(databaseURL, "timezone=") {
		return databaseURL
	}
	sep := "?"
	if containsParam(databaseURL, "?") {
		sep = "&"
	}
	return databaseURL + sep + "timezone=UTC"
}

func containsParam(s, needle string) bool {
	for i := 0; i+len(needle) <= len(s); i++ {
		if s[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// Migrate は初期スキーマを適用する。
// すべて IF NOT EXISTS なので何度でも安全に呼べる（引数なしの Exec は simple protocol で複文を通す）。
func Migrate(ctx context.Context, conn *sql.DB) error {
	_, err := conn.ExecContext(ctx, schema)
	return err
}
