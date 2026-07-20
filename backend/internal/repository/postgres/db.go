package postgres

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DB struct{ Pool *pgxpool.Pool }

type executorContextKey struct{}

type Querier interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func contextWithTx(ctx context.Context, tx pgx.Tx) context.Context {
	return context.WithValue(ctx, executorContextKey{}, tx)
}

func Executor(ctx context.Context, fallback Querier) Querier {
	if tx, ok := ctx.Value(executorContextKey{}).(pgx.Tx); ok {
		return tx
	}
	return fallback
}

func Open(ctx context.Context, databaseURL string) (*DB, error) {
	configuration, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database configuration: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, configuration)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return &DB{Pool: pool}, nil
}

func (db *DB) Close() { db.Pool.Close() }

func (db *DB) InTx(ctx context.Context, options pgx.TxOptions, function func(pgx.Tx) error) error {
	if tx, ok := ctx.Value(executorContextKey{}).(pgx.Tx); ok {
		nested, err := tx.Begin(ctx)
		if err != nil {
			return MapError(err)
		}
		defer nested.Rollback(ctx)
		if err := function(nested); err != nil {
			return MapError(err)
		}
		return MapError(nested.Commit(ctx))
	}
	err := pgx.BeginTxFunc(ctx, db.Pool, options, function)
	return MapError(err)
}

func LockUsers(ctx context.Context, tx pgx.Tx, ids ...uuid.UUID) error {
	ordered := append([]uuid.UUID(nil), ids...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].String() < ordered[j].String() })
	for index, id := range ordered {
		if index > 0 && id == ordered[index-1] {
			continue
		}
		if err := tx.QueryRow(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, id).Scan(&id); err != nil {
			return MapError(err)
		}
	}
	return nil
}

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
	ErrInvalid  = errors.New("invalid data")
)

func MapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) {
		switch pgError.Code {
		case "23505", "23503", "40001", "40P01":
			return fmt.Errorf("%w: %s", ErrConflict, pgError.ConstraintName)
		case "23502", "23514", "22P02":
			return fmt.Errorf("%w: %s", ErrInvalid, pgError.ConstraintName)
		}
	}
	return err
}
