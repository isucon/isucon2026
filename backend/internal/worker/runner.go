package worker

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/clock"
	"github.com/isucon/isucon2026/backend/internal/repository/postgres"
	"github.com/jackc/pgx/v5"
)

type Job struct {
	ID       uuid.UUID
	Kind     string
	Payload  json.RawMessage
	Attempts int
}
type Handler interface {
	Handle(context.Context, pgx.Tx, Job) error
}
type HandlerFunc func(context.Context, pgx.Tx, Job) error

func (function HandlerFunc) Handle(ctx context.Context, tx pgx.Tx, job Job) error {
	return function(ctx, tx, job)
}

type Runner struct {
	DB           *postgres.DB
	Clock        clock.Clock
	PollInterval time.Duration
	BatchSize    int
	Handlers     map[string]Handler
	Logger       *slog.Logger
}

func (r *Runner) Run(ctx context.Context) error {
	if r.PollInterval <= 0 || r.BatchSize <= 0 {
		return errors.New("worker polling configuration must be positive")
	}
	ticker := time.NewTicker(r.PollInterval)
	defer ticker.Stop()
	for {
		if err := r.Poll(ctx); err != nil && !errors.Is(err, context.Canceled) {
			r.Logger.Error("worker poll failed", "operation", "worker_poll", "result", "unexpected_error", "correlationId", uuid.Nil.String(), "errorType", "database")
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (r *Runner) Poll(ctx context.Context) error {
	return r.DB.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id,kind,payload,attempts FROM worker_jobs WHERE status IN ('pending','retryable') AND next_attempt_at <= $1 ORDER BY next_attempt_at,id FOR UPDATE SKIP LOCKED LIMIT $2`, r.Clock.Now(), r.BatchSize)
		if err != nil {
			return err
		}
		var jobs []Job
		for rows.Next() {
			var job Job
			if err := rows.Scan(&job.ID, &job.Kind, &job.Payload, &job.Attempts); err != nil {
				rows.Close()
				return err
			}
			jobs = append(jobs, job)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for _, job := range jobs {
			handler := r.Handlers[job.Kind]
			if handler == nil {
				if _, err := tx.Exec(ctx, `UPDATE worker_jobs SET status='failed',last_error_class='unknown_kind',updated_at=$2 WHERE id=$1`, job.ID, r.Clock.Now()); err != nil {
					return err
				}
				continue
			}
			if err := handler.Handle(ctx, tx, job); err != nil {
				attempts := job.Attempts + 1
				status := "retryable"
				if attempts >= 4 {
					status = "failed"
				}
				delay := time.Duration(1<<min(attempts, 6)) * time.Second
				if _, updateErr := tx.Exec(ctx, `UPDATE worker_jobs SET status=$2,attempts=$3,next_attempt_at=$4,last_error_class='handler_error',updated_at=$5 WHERE id=$1`, job.ID, status, attempts, r.Clock.Now().Add(delay), r.Clock.Now()); updateErr != nil {
					return updateErr
				}
				continue
			}
			if _, err := tx.Exec(ctx, `UPDATE worker_jobs SET status='completed',attempts=attempts+1,updated_at=$2 WHERE id=$1`, job.ID, r.Clock.Now()); err != nil {
				return err
			}
		}
		return nil
	})
}
