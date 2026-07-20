package postgres

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/isucon/isucon2026/backend/internal/clock"
	"github.com/isucon/isucon2026/backend/internal/domain"
	"github.com/isucon/isucon2026/backend/internal/idempotency"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type IdempotencyRepository struct {
	db    *DB
	ids   domain.IDGenerator
	clock clock.Clock
}

func NewIdempotencyRepository(db *DB, ids domain.IDGenerator, source clock.Clock) *IdempotencyRepository {
	return &IdempotencyRepository{db: db, ids: ids, clock: source}
}

type storedResponse struct {
	Header map[string][]string `json:"header"`
	Body   string              `json:"body"`
}

func (r *IdempotencyRepository) Execute(ctx context.Context, request idempotency.Request, callback idempotency.Callback) (idempotency.Result, bool, error) {
	for attempt := 0; attempt < 8; attempt++ {
		result, replayed, retry, err := r.executeOnce(ctx, request, callback)
		if !retry {
			return result, replayed, err
		}
	}
	return idempotency.Result{}, false, idempotency.ErrInFlight
}

func (r *IdempotencyRepository) executeOnce(ctx context.Context, request idempotency.Request, callback idempotency.Callback) (idempotency.Result, bool, bool, error) {
	tx, err := r.db.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return idempotency.Result{}, false, false, err
	}
	defer tx.Rollback(ctx)

	var generation int64
	var requestHash, status string
	var responseStatus *int
	var responseJSON []byte
	var expiresAt *time.Time
	err = tx.QueryRow(ctx, `SELECT generation,request_hash,status,response_status,response_body,expires_at FROM idempotency_records WHERE caller_scope_type=$1 AND caller_scope_key=$2 AND operation=$3 AND key=$4 ORDER BY generation DESC LIMIT 1 FOR UPDATE`,
		request.Scope.Type, request.Scope.Key, request.Operation, request.Key).
		Scan(&generation, &requestHash, &status, &responseStatus, &responseJSON, &expiresAt)
	now := r.clock.Now()
	requestHash = strings.TrimSpace(requestHash)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		generation = 1
	case err != nil:
		return idempotency.Result{}, false, false, err
	case status == "completed" && expiresAt != nil && now.Before(*expiresAt):
		if requestHash != request.RequestHash {
			return idempotency.Result{}, false, false, idempotency.ErrRequestConflict
		}
		result, err := decodeStoredResponse(*responseStatus, responseJSON)
		return result, true, false, err
	case status == "processing":
		return idempotency.Result{}, false, true, nil
	default:
		generation++
	}

	_, err = tx.Exec(ctx, `INSERT INTO idempotency_records(id,caller_scope_type,caller_scope_key,operation,key,generation,request_hash,status) VALUES($1,$2,$3,$4,$5,$6,$7,'processing')`,
		r.ids.New(), request.Scope.Type, request.Scope.Key, request.Operation, request.Key, generation, request.RequestHash)
	if err != nil {
		var pgError *pgconn.PgError
		if errors.As(err, &pgError) && pgError.Code == "23505" {
			return idempotency.Result{}, false, true, nil
		}
		return idempotency.Result{}, false, false, err
	}

	result, err := callback(contextWithTx(ctx, tx))
	if err != nil {
		return idempotency.Result{}, false, false, err
	}
	if result.Status == 503 {
		return result, false, false, nil
	}
	encoded, err := encodeStoredResponse(result)
	if err != nil {
		return idempotency.Result{}, false, false, err
	}
	finalizedAt := r.clock.Now()
	if _, err := tx.Exec(ctx, `UPDATE idempotency_records SET status='completed',response_status=$1,response_body=$2,finalized_at=$3,expires_at=$4 WHERE caller_scope_type=$5 AND caller_scope_key=$6 AND operation=$7 AND key=$8 AND generation=$9`,
		result.Status, encoded, finalizedAt, finalizedAt.Add(24*time.Hour), request.Scope.Type, request.Scope.Key, request.Operation, request.Key, generation); err != nil {
		return idempotency.Result{}, false, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return idempotency.Result{}, false, false, MapError(err)
	}
	return result, false, false, nil
}

func encodeStoredResponse(result idempotency.Result) ([]byte, error) {
	return json.Marshal(storedResponse{Header: result.Header, Body: base64.StdEncoding.EncodeToString(result.Body)})
}

func decodeStoredResponse(status int, encoded []byte) (idempotency.Result, error) {
	var stored storedResponse
	if err := json.Unmarshal(encoded, &stored); err != nil {
		return idempotency.Result{}, fmt.Errorf("decode idempotency response: %w", err)
	}
	body, err := base64.StdEncoding.DecodeString(stored.Body)
	if err != nil {
		return idempotency.Result{}, fmt.Errorf("decode idempotency body: %w", err)
	}
	return idempotency.Result{Status: status, Header: stored.Header, Body: body}, nil
}
