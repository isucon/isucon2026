package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/isucon/isucon2026/backend/internal/clock"
	"github.com/jackc/pgx/v5"
)

const virtualOffsetKey = "virtual_time_offset_ns"

// SettingsRepository stores operational values shared across processes via
// the app_settings table.
type SettingsRepository struct {
	db    *DB
	clock clock.Clock
}

func NewSettingsRepository(db *DB, clk clock.Clock) *SettingsRepository {
	return &SettingsRepository{db: db, clock: clk}
}

// SaveVirtualOffset persists the virtual clock offset so other processes
// (e.g. the worker) can follow the API's virtual time.
func (r *SettingsRepository) SaveVirtualOffset(ctx context.Context, offset time.Duration) error {
	_, err := r.db.Pool.Exec(ctx, `INSERT INTO app_settings(key,value,updated_at) VALUES($1,to_jsonb($2::bigint),$3)
		ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=EXCLUDED.updated_at`,
		virtualOffsetKey, int64(offset), r.clock.Now())
	return err
}

// LoadVirtualOffset returns the persisted offset. The second return value is
// false when no offset has been stored yet.
func (r *SettingsRepository) LoadVirtualOffset(ctx context.Context) (time.Duration, bool, error) {
	var nanos int64
	err := r.db.Pool.QueryRow(ctx, `SELECT (value#>>'{}')::bigint FROM app_settings WHERE key=$1`, virtualOffsetKey).Scan(&nanos)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return time.Duration(nanos), true, nil
}
