package postgres

import (
	"context"
	"github.com/isucon/isucon2026/backend/internal/domain"
	"github.com/jackc/pgx/v5"
	"time"
)

func (r *NotificationRepository) ClaimPush(ctx context.Context, limit int) ([]domain.PushAttempt, error) {
	var attempts []domain.PushAttempt
	err := r.db.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT p.id,p.notification_id,p.subscription_id,s.endpoint,s.p256dh,s.auth_secret,p.attempts FROM push_attempts p JOIN browser_push_subscriptions s ON s.id=p.subscription_id AND s.active JOIN login_sessions l ON l.id=s.session_id AND l.active WHERE ((p.status IN('pending','retryable') AND p.next_attempt_at<=$1) OR (p.status='processing' AND p.updated_at<$2)) ORDER BY p.next_attempt_at,p.id FOR UPDATE OF p SKIP LOCKED LIMIT $3`, r.clock.Now(), r.clock.Now().Add(-time.Minute), limit)
		if err != nil {
			return err
		}
		for rows.Next() {
			var item domain.PushAttempt
			if err := rows.Scan(&item.ID, &item.NotificationID, &item.SubscriptionID, &item.Endpoint, &item.P256DH, &item.Auth, &item.Attempts); err != nil {
				rows.Close()
				return err
			}
			attempts = append(attempts, item)
		}
		rows.Close()
		for _, item := range attempts {
			if _, err := tx.Exec(ctx, `UPDATE push_attempts SET status='processing',updated_at=$2 WHERE id=$1`, item.ID, r.clock.Now()); err != nil {
				return err
			}
		}
		return nil
	})
	return attempts, err
}
func (r *NotificationRepository) FinishPush(ctx context.Context, item domain.PushAttempt, transient, permanent bool, errorClass string) error {
	return r.db.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		attempt := item.Attempts + 1
		state := domain.NextPushState(attempt, transient, permanent)
		next := r.clock.Now()
		if state == domain.PushRetryable {
			next = next.Add(time.Duration(1<<min(attempt, 6)) * time.Second)
		}
		if _, err := tx.Exec(ctx, `UPDATE push_attempts SET status=$2,attempts=$3,next_attempt_at=$4,last_error_class=$5,updated_at=$6 WHERE id=$1 AND status='processing'`, item.ID, state, attempt, next, errorClass, r.clock.Now()); err != nil {
			return err
		}
		if permanent {
			_, err := tx.Exec(ctx, `UPDATE browser_push_subscriptions SET active=false,invalidated_at=$2,reason='permanent_push_failure',updated_at=$2 WHERE id=$1`, item.SubscriptionID, r.clock.Now())
			return err
		}
		return nil
	})
}
