// Package model は DB の行と protobuf 変換（各モデルの to_pb 相当）を提供する。
// マスク規則（CT-6）や導出規則（DM-5 など）は isucon14-portal の app/models に一致させる。
package model

import (
	"context"
	"database/sql"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
)

// DBTX は *sql.DB と *sql.Tx の共通部分。
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// ErrNotFound は ActiveRecord::RecordNotFound 相当（404）。
type NotFoundError struct{ Msg string }

func (e *NotFoundError) Error() string {
	if e.Msg == "" {
		return "record not found"
	}
	return e.Msg
}

// InvalidError は ActiveRecord::RecordInvalid 相当（422）。
type InvalidError struct{ Messages []string }

func (e *InvalidError) Error() string {
	if len(e.Messages) > 0 {
		return e.Messages[0]
	}
	return "record invalid"
}

func ts(t time.Time) *timestamppb.Timestamp { return timestamppb.New(t) }

func tsn(t sql.NullTime) *timestamppb.Timestamp {
	if !t.Valid {
		return nil
	}
	return timestamppb.New(t.Time)
}

func nullStr(s sql.NullString) string { return s.String }
