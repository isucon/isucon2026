package postgres

import "errors"
import (
	"context"
	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/domain"
	"github.com/jackc/pgx/v5"
	"time"
)

type AccountRepository struct {
	db    *DB
	clock interface{ Now() time.Time }
}

func NewAccountRepository(db *DB, clock interface{ Now() time.Time }) *AccountRepository {
	return &AccountRepository{db: db, clock: clock}
}
func (r *AccountRepository) RequestDeletion(ctx context.Context, userID uuid.UUID) (time.Time, error) {
	now := r.clock.Now()
	due := now.Add(domain.AccountDeletionGrace)
	err := r.db.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := LockUsers(ctx, tx, userID); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE users SET status='deletion_pending',deletion_requested_at=$2,purge_due_at=$3,updated_at=$2 WHERE id=$1 AND status='active'`, userID, now, due)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrConflict
		}
		if _, err := tx.Exec(ctx, `UPDATE browser_push_subscriptions SET active=false,invalidated_at=$2,reason='account_deletion',updated_at=$2 WHERE user_id=$1 AND active`, userID, now); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE login_sessions SET active=false,invalidated_at=$2,reason='account_deletion' WHERE user_id=$1 AND active`, userID, now)
		return err
	})
	return due, err
}
func (r *AccountRepository) DueAccounts(ctx context.Context, limit int) ([]uuid.UUID, error) {
	rows, err := r.db.Pool.Query(ctx, `SELECT id FROM users WHERE status='deletion_pending' AND purge_due_at<=$1 ORDER BY purge_due_at,id LIMIT $2 FOR UPDATE SKIP LOCKED`, r.clock.Now(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
func (r *AccountRepository) Purge(ctx context.Context, userID uuid.UUID) ([]string, error) {
	var keys []string
	var imageIDs []uuid.UUID
	err := r.db.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := LockUsers(ctx, tx, userID); err != nil {
			if errors.Is(err, ErrNotFound) {
				return nil
			}
			return err
		}
		var due time.Time
		if err := tx.QueryRow(ctx, `SELECT purge_due_at FROM users WHERE id=$1 AND status='deletion_pending' FOR UPDATE`, userID).Scan(&due); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		if r.clock.Now().Before(due) {
			return ErrConflict
		}
		rows, err := tx.Query(ctx, `SELECT id,object_key FROM image_objects WHERE owner_user_id=$1 OR id IN(SELECT image_id FROM posts WHERE author_id=$1) OR id IN(SELECT x.image_id FROM realmojis x JOIN posts p ON p.id=x.post_id WHERE p.author_id=$1)`, userID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var imageID uuid.UUID
			var key string
			if err := rows.Scan(&imageID, &key); err != nil {
				rows.Close()
				return err
			}
			keys = append(keys, key)
			imageIDs = append(imageIDs, imageID)
		}
		rows.Close()
		statements := []string{`DELETE FROM comments WHERE author_id=$1 OR post_id IN(SELECT id FROM posts WHERE author_id=$1)`, `DELETE FROM realmojis WHERE reactor_id=$1 OR post_id IN(SELECT id FROM posts WHERE author_id=$1)`, `UPDATE user_posting_window_states SET active_post_id=NULL WHERE user_id=$1`, `DELETE FROM user_posting_window_states WHERE user_id=$1`, `DELETE FROM posts WHERE author_id=$1`, `DELETE FROM friend_requests WHERE requester_id=$1 OR recipient_id=$1`, `DELETE FROM friendships WHERE user_low_id=$1 OR user_high_id=$1`, `DELETE FROM follows WHERE follower_id=$1 OR followee_id=$1`, `DELETE FROM block_relations WHERE blocker_id=$1 OR blocked_id=$1`, `DELETE FROM notifications WHERE user_id=$1`, `DELETE FROM browser_push_subscriptions WHERE user_id=$1`, `DELETE FROM login_sessions WHERE user_id=$1`, `UPDATE users SET profile_image_id=NULL WHERE id=$1`}
		for _, statement := range statements {
			if _, err := tx.Exec(ctx, statement, userID); err != nil {
				return err
			}
		}
		if len(imageIDs) > 0 {
			if _, err := tx.Exec(ctx, `DELETE FROM image_objects WHERE id=ANY($1)`, imageIDs); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID); err != nil {
			return err
		}
		return nil
	})
	return keys, err
}
