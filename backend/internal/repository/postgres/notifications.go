package postgres

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/domain"
	"github.com/isucon/isucon2026/backend/internal/service"
	"github.com/jackc/pgx/v5"
	"time"
)

type NotificationRepository struct {
	db    *DB
	ids   domain.IDGenerator
	clock interface{ Now() time.Time }
}

func NewNotificationRepository(db *DB, ids domain.IDGenerator, clock interface{ Now() time.Time }) *NotificationRepository {
	return &NotificationRepository{db: db, ids: ids, clock: clock}
}
func (r *NotificationRepository) SetSchedule(ctx context.Context, schedule domain.NotificationSchedule) error {
	return r.db.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var existingAt *time.Time
		err := tx.QueryRow(ctx, `SELECT scheduled_at FROM notification_schedules WHERE region=$1 FOR UPDATE`, schedule.Region).Scan(&existingAt)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		now := r.clock.Now()
		if existingAt != nil && !existingAt.After(now) {
			return ErrConflict
		}
		var started bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM posting_windows WHERE region=$1 AND local_date=$2)`, schedule.Region, schedule.LocalDate).Scan(&started); err != nil {
			return err
		}
		if started {
			return ErrConflict
		}
		_, err = tx.Exec(ctx, `INSERT INTO notification_schedules(region,scheduled_at,local_date,created_at,updated_at)VALUES($1,$2,$3,$4,$4) ON CONFLICT(region)DO UPDATE SET scheduled_at=EXCLUDED.scheduled_at,local_date=EXCLUDED.local_date,updated_at=EXCLUDED.updated_at`, schedule.Region, schedule.ScheduledAt, schedule.LocalDate, now)
		return err
	})
}
func (r *NotificationRepository) CancelSchedule(ctx context.Context, region domain.Region) error {
	tag, err := Executor(ctx, r.db.Pool).Exec(ctx, `DELETE FROM notification_schedules WHERE region=$1 AND scheduled_at>$2`, region, r.clock.Now())
	if err != nil {
		return MapError(err)
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}
func (r *NotificationRepository) StartOneDue(ctx context.Context) (domain.Region, bool, error) {
	var region domain.Region
	err := r.db.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var scheduledAt, timeLocal time.Time
		err := tx.QueryRow(ctx, `SELECT region,scheduled_at,local_date FROM notification_schedules WHERE scheduled_at<=$1 ORDER BY scheduled_at,region FOR UPDATE SKIP LOCKED LIMIT 1`, r.clock.Now()).Scan(&region, &scheduledAt, &timeLocal)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE posting_windows SET ended_at=$2 WHERE region=$1 AND ended_at IS NULL`, region, scheduledAt); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE users SET region=pending_region,pending_region=NULL,updated_at=$2 WHERE region=$1 AND pending_region IS NOT NULL AND status='active'`, region, scheduledAt); err != nil {
			return err
		}
		windowID := r.ids.New()
		offset := map[domain.Region]int{domain.RegionAmerica: -300, domain.RegionEurope: 60, domain.RegionAsia: 540}[region]
		if _, err := tx.Exec(ctx, `INSERT INTO posting_windows(id,region,local_date,utc_offset_minutes,starts_at,on_time_deadline)VALUES($1,$2,$3,$4,$5,$6)`, windowID, region, timeLocal, offset, scheduledAt, scheduledAt.Add(2*time.Minute)); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id FROM users WHERE region=$1 AND status='active' ORDER BY id`, region)
		if err != nil {
			return err
		}
		var users []uuid.UUID
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			users = append(users, id)
		}
		rows.Close()
		for _, userID := range users {
			notificationID := r.ids.New()
			if _, err := tx.Exec(ctx, `INSERT INTO notifications(id,user_id,posting_window_id,region,created_at)VALUES($1,$2,$3,$4,$5) ON CONFLICT(user_id,posting_window_id)DO NOTHING`, notificationID, userID, windowID, region, scheduledAt); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO push_attempts(id,notification_id,subscription_id,status,next_attempt_at,created_at,updated_at) SELECT $1,$2,s.id,'pending',$3,$3,$3 FROM browser_push_subscriptions s JOIN login_sessions l ON l.id=s.session_id AND l.active WHERE s.user_id=$4 AND s.active ON CONFLICT(notification_id,subscription_id)DO NOTHING`, r.ids.New(), notificationID, scheduledAt, userID); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `DELETE FROM notification_schedules WHERE region=$1`, region)
		return err
	})
	return region, region != "", err
}
func (r *NotificationRepository) List(ctx context.Context, user uuid.UUID, beforeAt *time.Time, beforeID *uuid.UUID, limit int) ([]domain.Notification, error) {
	rows, err := Executor(ctx, r.db.Pool).Query(ctx, `SELECT id,user_id,posting_window_id,region,created_at,read_at FROM notifications WHERE user_id=$1 AND ($2::timestamptz IS NULL OR (created_at,id)<($2,$3)) ORDER BY created_at DESC,id DESC LIMIT $4`, user, beforeAt, beforeID, limit)
	if err != nil {
		return nil, MapError(err)
	}
	defer rows.Close()
	var records []domain.Notification
	for rows.Next() {
		var n domain.Notification
		if err := rows.Scan(&n.ID, &n.UserID, &n.PostingWindowID, &n.Region, &n.CreatedAt, &n.ReadAt); err != nil {
			return nil, err
		}
		records = append(records, n)
	}
	return records, rows.Err()
}
func (r *NotificationRepository) Open(ctx context.Context, user, id uuid.UUID) (domain.NotificationDestination, string, error) {
	var destination domain.NotificationDestination
	var path string
	err := r.db.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var windowID uuid.UUID
		var localDate time.Time
		var current, posted bool
		if err := tx.QueryRow(ctx, `SELECT n.posting_window_id,w.local_date,w.ended_at IS NULL,EXISTS(SELECT 1 FROM user_posting_window_states s WHERE s.user_id=$1 AND s.posting_window_id=n.posting_window_id AND s.active_post_id IS NOT NULL) FROM notifications n JOIN posting_windows w ON w.id=n.posting_window_id WHERE n.id=$2 AND n.user_id=$1 FOR UPDATE OF n`, user, id).Scan(&windowID, &localDate, &current, &posted); err != nil {
			return err
		}
		destination = domain.Destination(current, posted)
		switch destination {
		case domain.DestinationCreatePost:
			path = "/posts/new"
		case domain.DestinationCurrentPost:
			path = "/posts/me/current"
		case domain.DestinationMemory:
			path = "/memories?date=" + localDate.Format("2006-01-02")
		}
		_, err := tx.Exec(ctx, `UPDATE notifications SET read_at=COALESCE(read_at,$3) WHERE id=$1 AND user_id=$2`, id, user, r.clock.Now())
		return err
	})
	return destination, path, MapError(err)
}
func (r *NotificationRepository) ReplacePush(ctx context.Context, user uuid.UUID, token, endpoint, p256dh, auth string) error {
	return r.db.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var sessionID uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM login_sessions WHERE user_id=$1 AND token_hash=$2 AND active FOR UPDATE`, user, service.HashSessionToken(token)).Scan(&sessionID); err != nil {
			return service.ErrInvalidSession
		}
		now := r.clock.Now()
		if _, err := tx.Exec(ctx, `UPDATE browser_push_subscriptions SET active=false,invalidated_at=$2,reason='replaced',updated_at=$2 WHERE session_id=$1 AND active`, sessionID, now); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO browser_push_subscriptions(id,session_id,user_id,endpoint,p256dh,auth_secret,active,created_at,updated_at)VALUES($1,$2,$3,$4,$5,$6,true,$7,$7)`, r.ids.New(), sessionID, user, endpoint, p256dh, auth, now)
		return err
	})
}
func (r *NotificationRepository) RemovePush(ctx context.Context, user uuid.UUID, token string) error {
	tag, err := Executor(ctx, r.db.Pool).Exec(ctx, `UPDATE browser_push_subscriptions s SET active=false,invalidated_at=$3,reason='removed',updated_at=$3 FROM login_sessions l WHERE s.session_id=l.id AND l.user_id=$1 AND l.token_hash=$2 AND l.active AND s.active`, user, service.HashSessionToken(token), r.clock.Now())
	if err != nil {
		return MapError(err)
	}
	if tag.RowsAffected() == 0 {
		return service.ErrInvalidSession
	}
	return nil
}
