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

// migrateLockKey は Migrate を直列化する advisory lock のキー（任意の定数）。
const migrateLockKey = 0x1543_0001

// Migrate は初期スキーマを適用する。
// すべて IF NOT EXISTS なので何度でも安全に呼べる（引数なしの Exec は simple protocol で複文を通す）。
// PostgreSQL の DDL はトランザクション内で実行でき、複数プロセスが同時に起動しても
// （ローリング更新の PORTAL_AUTO_MIGRATE）競合しないよう advisory lock で直列化する。
func Migrate(ctx context.Context, conn *sql.DB) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, migrateLockKey); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, schema); err != nil {
		return err
	}
	return tx.Commit()
}
