// Package db は MySQL 接続とスキーマ適用を提供する。
package db

import (
	"context"
	"database/sql"
	_ "embed"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

//go:embed schema.sql
var schema string

func Open(dsn string) (*sql.DB, error) {
	conn, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(20)
	conn.SetMaxIdleConns(20)
	conn.SetConnMaxLifetime(5 * time.Minute)
	return conn, nil
}

// Migrate は初期スキーマ（isucon14-portal の schema.rb 相当）を適用する。
// すべて CREATE TABLE IF NOT EXISTS なので何度でも安全に呼べる。
func Migrate(ctx context.Context, conn *sql.DB) error {
	_, err := conn.ExecContext(ctx, schema)
	return err
}
