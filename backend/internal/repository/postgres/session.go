package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/clock"
	"github.com/isucon/isucon2026/backend/internal/domain"
	"github.com/isucon/isucon2026/backend/internal/service"
	"github.com/jackc/pgx/v5"
)

type SessionRepository struct {
	db    *DB
	ids   domain.IDGenerator
	clock clock.Clock
}

func NewSessionRepository(db *DB, ids domain.IDGenerator, source clock.Clock) *SessionRepository {
	return &SessionRepository{db: db, ids: ids, clock: source}
}

func (r *SessionRepository) CreateUserAndSession(ctx context.Context, record service.RegistrationRecord) (domain.User, uuid.UUID, error) {
	user := domain.User{ID: r.ids.New(), Username: record.Username, UsernameNormalized: record.UsernameNormalized, DisplayName: record.DisplayName, Region: record.Region, AccountType: "regular", Status: "active"}
	sessionID := r.ids.New()
	err := r.db.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO users(id,username,username_normalized,password_hash,display_name,display_name_search,region) VALUES($1,$2,$3,$4,$5,$6,$7)`,
			user.ID, record.Username, record.UsernameNormalized, record.PasswordHash, record.DisplayName, record.DisplayNameSearch, record.Region)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO login_sessions(id,user_id,token_hash,active,created_at) VALUES($1,$2,$3,true,$4)`, sessionID, user.ID, record.TokenHash, r.clock.Now())
		return err
	})
	if errors.Is(err, ErrConflict) {
		return domain.User{}, uuid.Nil, service.ErrUsernameTaken
	}
	return user, sessionID, err
}

func (r *SessionRepository) CredentialByUsername(ctx context.Context, normalized string) (domain.User, string, bool, error) {
	var user domain.User
	var passwordHash string
	err := r.db.Pool.QueryRow(ctx, `SELECT id,username,username_normalized,password_hash,display_name,bio,region,account_type,status FROM users WHERE username_normalized=$1`, normalized).
		Scan(&user.ID, &user.Username, &user.UsernameNormalized, &passwordHash, &user.DisplayName, &user.Bio, &user.Region, &user.AccountType, &user.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, "", false, nil
	}
	if err != nil {
		return domain.User{}, "", false, MapError(err)
	}
	return user, passwordHash, true, nil
}

func (r *SessionRepository) ReplaceSession(ctx context.Context, userID uuid.UUID, tokenHash string) (uuid.UUID, error) {
	sessionID := r.ids.New()
	err := r.db.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := LockUsers(ctx, tx, userID); err != nil {
			return err
		}
		var status string
		var purgeDue *time.Time
		if err := tx.QueryRow(ctx, `SELECT status,purge_due_at FROM users WHERE id=$1`, userID).Scan(&status, &purgeDue); err != nil {
			return err
		}
		now := r.clock.Now()
		if status == "deletion_pending" && purgeDue != nil && now.Before(*purgeDue) {
			if _, err := tx.Exec(ctx, `UPDATE users SET status='active',deletion_requested_at=NULL,purge_due_at=NULL,updated_at=$2 WHERE id=$1`, userID, now); err != nil {
				return err
			}
			status = "active"
		}
		if status != "active" {
			return service.ErrInactiveAccount
		}
		if _, err := tx.Exec(ctx, `UPDATE browser_push_subscriptions SET active=false,invalidated_at=$2,reason='session_replaced',updated_at=$2 WHERE user_id=$1 AND active`, userID, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE login_sessions SET active=false,invalidated_at=$2,reason='replaced' WHERE user_id=$1 AND active`, userID, now); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO login_sessions(id,user_id,token_hash,active,created_at) VALUES($1,$2,$3,true,$4)`, sessionID, userID, tokenHash, now)
		return err
	})
	return sessionID, err
}

func (r *SessionRepository) LogoutSession(ctx context.Context, tokenHash string) error {
	return r.db.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var sessionID, userID uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id,user_id FROM login_sessions WHERE token_hash=$1 AND active FOR UPDATE`, tokenHash).Scan(&sessionID, &userID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return service.ErrInvalidSession
			}
			return err
		}
		now := r.clock.Now()
		if _, err := tx.Exec(ctx, `UPDATE browser_push_subscriptions SET active=false,invalidated_at=$2,reason='logout',updated_at=$2 WHERE session_id=$1 AND active`, sessionID, now); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE login_sessions SET active=false,invalidated_at=$2,reason='logout' WHERE id=$1`, sessionID, now)
		return err
	})
}

func (r *SessionRepository) AuthenticateUser(ctx context.Context, token string) (domain.User, error) {
	var user domain.User
	err := r.db.Pool.QueryRow(ctx, `SELECT u.id,u.username,u.username_normalized,u.display_name,u.bio,u.region,u.account_type,u.status FROM login_sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=$1 AND s.active AND u.status='active'`, service.HashSessionToken(token)).
		Scan(&user.ID, &user.Username, &user.UsernameNormalized, &user.DisplayName, &user.Bio, &user.Region, &user.AccountType, &user.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, service.ErrInvalidSession
	}
	return user, MapError(err)
}

func (r *SessionRepository) AuthenticateAdministrator(ctx context.Context, token string) (domain.Administrator, error) {
	var administrator domain.Administrator
	err := r.db.Pool.QueryRow(ctx, `SELECT a.id,a.login_name_normalized,a.enabled FROM administrator_sessions s JOIN administrators a ON a.id=s.administrator_id WHERE s.token_hash=$1 AND s.active AND a.enabled`, service.HashSessionToken(token)).
		Scan(&administrator.ID, &administrator.LoginName, &administrator.Enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Administrator{}, service.ErrInvalidSession
	}
	return administrator, MapError(err)
}

func (r *SessionRepository) AdministratorCredential(ctx context.Context, normalized string) (domain.Administrator, string, bool, error) {
	var administrator domain.Administrator
	var passwordHash string
	err := r.db.Pool.QueryRow(ctx, `SELECT id,login_name_normalized,password_hash,enabled FROM administrators WHERE login_name_normalized=$1`, normalized).Scan(&administrator.ID, &administrator.LoginName, &passwordHash, &administrator.Enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Administrator{}, "", false, nil
	}
	return administrator, passwordHash, true, MapError(err)
}
func (r *SessionRepository) ReplaceAdministratorSession(ctx context.Context, administratorID uuid.UUID, tokenHash string) (uuid.UUID, error) {
	id := r.ids.New()
	err := r.db.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		now := r.clock.Now()
		if _, err := tx.Exec(ctx, `UPDATE administrator_sessions SET active=false,invalidated_at=$2 WHERE administrator_id=$1 AND active`, administratorID, now); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO administrator_sessions(id,administrator_id,token_hash,active,created_at) VALUES($1,$2,$3,true,$4)`, id, administratorID, tokenHash, now)
		return err
	})
	return id, err
}

func (r *SessionRepository) ActiveSessionCount(ctx context.Context, userID uuid.UUID) (int, error) {
	var count int
	err := r.db.Pool.QueryRow(ctx, `SELECT count(*) FROM login_sessions WHERE user_id=$1 AND active`, userID).Scan(&count)
	return count, err
}

func (r *SessionRepository) ActivePushSubscriptionCount(ctx context.Context, userID uuid.UUID) (int, error) {
	var count int
	err := r.db.Pool.QueryRow(ctx, `SELECT count(*) FROM browser_push_subscriptions WHERE user_id=$1 AND active`, userID).Scan(&count)
	return count, err
}

func (r *SessionRepository) InsertTestPushSubscription(ctx context.Context, userID, sessionID uuid.UUID) error {
	_, err := r.db.Pool.Exec(ctx, `INSERT INTO browser_push_subscriptions(id,session_id,user_id,endpoint,p256dh,auth_secret) VALUES($1,$2,$3,'https://push.invalid/test','test-p256dh','test-auth')`, r.ids.New(), sessionID, userID)
	return err
}
