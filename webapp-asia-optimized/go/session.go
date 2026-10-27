package isureal

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
)

type Session struct {
	Region    string    `db:"region" json:"-"`
	Token     string    `db:"-" json:"-"`
	UserID    string    `db:"user_id"`
	TokenHash string    `db:"token_hash"`
	CreatedAt time.Time `db:"created_at"`
}

func GetSession(c *echo.Context) *Session {
	sess, ok := c.Get("session").(*Session)
	if !ok {
		return nil
	}
	return sess
}

func getSessionFromHeader(c *echo.Context, db *sqlx.DB) (*Session, error) {
	authHeader := c.Request().Header.Get(echo.HeaderAuthorization)
	if authHeader == "" {
		return nil, nil
	}
	if !strings.HasPrefix(authHeader, "Bearer ") {
		c.Logger().Warn("Invalid authorization header format(not started with Bearer)")
		return nil, NewAPIError(http.StatusUnauthorized, "invalid_session")
	}

	token := strings.TrimPrefix(authHeader, "Bearer ")

	tokenHashBytes := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(tokenHashBytes[:])
	ctx := c.Request().Context()
	loginSession, err := getLoginSessionByTokenHashCached(ctx, db, tokenHash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}
	if !loginSession.Active {
		return nil, nil
	}
	user, err := getUserAnyStatusCached(ctx, db, loginSession.UserID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get session user: %w", err)
	}
	if user.Status != "active" {
		return nil, nil
	}
	return &Session{UserID: loginSession.UserID, TokenHash: loginSession.TokenHash, CreatedAt: loginSession.CreatedAt, Region: user.Region}, nil
}

func SessionMiddleware(handlerContext *HandlerContext, authRequired bool) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if err := state.ensureLoaded(c.Request().Context(), handlerContext.DB); err != nil {
				return err
			}
			session, err := getSessionFromHeader(c, handlerContext.DB)
			if err != nil {
				return err
			}
			if session == nil && authRequired {
				return NewAPIError(http.StatusUnauthorized, "invalid_session")
			}
			c.Set("session", session)
			return next(c)
		}
	}
}

func InvalidateLoginSession(db *sqlx.DB, userID string, tokenHash string) error {
	unlock := lockKeys(sessionKey(userID))
	defer unlock()
	tx, err := db.Beginx()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		UPDATE login_sessions
		SET active = false, invalidated_at = clock_timestamp(), reason = 'logout'
		WHERE user_id = $1 AND active = true AND token_hash = $2	
	`, userID, tokenHash); err != nil {
		return fmt.Errorf("failed to invalidate sessions: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	sessionCache.invalidate(tokenHash)

	return nil
}

// 2026-09-03: リファクタリング後にこの関数の利用がないようです。消すのはいかがでしょうか
func CreateLoginSession(db *sqlx.DB, userID string) (*Session, error) {
	ctx := context.Background()
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()
	unlock := lockKeys(sessionKey(userID))
	defer unlock()
	session, replaced, err := createLoginSessionTx(ctx, tx, userID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}
	sessionCache.invalidate(replaced...)
	return session, nil
}

// 置き換えられたセッションの token_hash も返す（コミット後にキャッシュを無効化するため）
func createLoginSessionTx(ctx context.Context, tx *sqlx.Tx, userID string) (*Session, []string, error) {
	randomToken := uuid.NewString()
	tokenHashBytes := sha256.Sum256([]byte(randomToken))
	tokenHash := hex.EncodeToString(tokenHashBytes[:])
	// 呼び出し側がユーザーのロックを保持している
	if user, err := getUserCached(ctx, tx, userID); err != nil {
		return nil, nil, fmt.Errorf("failed to lock user: %w", err)
	} else if user.Status != "active" {
		return nil, nil, fmt.Errorf("failed to lock user: %w", sql.ErrNoRows)
	}
	var replaced []string
	if err := tx.SelectContext(ctx, &replaced, `
  UPDATE login_sessions
  SET active = false, invalidated_at = clock_timestamp(), reason = 'replaced'
  WHERE user_id = $1 AND active = true
  RETURNING token_hash
 `, userID); err != nil {
		return nil, nil, fmt.Errorf("failed to invalidate sessions: %w", err)
	}
	sessionID := newID()
	if _, err := tx.ExecContext(ctx, `
  INSERT INTO login_sessions (id, user_id, token_hash)
  VALUES ($1, $2, $3)
 `, sessionID, userID, tokenHash); err != nil {
		return nil, nil, fmt.Errorf("failed to insert session: %w", err)
	}
	session, err := getLoginSession(ctx, tx, sessionID)
	if err != nil {
		return nil, nil, err
	}
	session.Token = randomToken
	return session, replaced, nil
}

func getLoginSession(ctx context.Context, db sqlx.QueryerContext, sessionID string) (*Session, error) {
	var session Session
	if err := sqlx.GetContext(ctx, db, &session, `SELECT user_id, token_hash, created_at FROM login_sessions WHERE id = $1`, sessionID); err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}
	return &session, nil
}
