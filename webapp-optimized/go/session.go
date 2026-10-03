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
	user      *User     // Only populated for read-only handlers; never reused across requests.
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
	return loadSessionFromHeader(c, db, false)
}

func loadSessionFromHeader(c *echo.Context, db *sqlx.DB, includeUser bool) (*Session, error) {
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
	var state struct {
		Session
		ReadUser User `db:"read_user"`
	}
	columns := "s.user_id, s.token_hash, s.created_at, u.region"
	if includeUser {
		columns += ", " + userColumnsAs("u", "read_user")
	}
	err := db.GetContext(c.Request().Context(), &state, `SELECT `+columns+`
        FROM login_sessions s JOIN users u ON u.id=s.user_id
        WHERE s.token_hash=$1 AND s.active=true AND u.status='active'`, tokenHash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}
	if includeUser {
		state.Session.user = &state.ReadUser
	}
	return &state.Session, nil
}

func SessionMiddleware(handlerContext *HandlerContext, authRequired bool) echo.MiddlewareFunc {
	return sessionMiddleware(handlerContext, authRequired, false, false)
}

// ReadSessionMiddleware folds the current user's data into authentication.
// Write handlers must keep reading their user under their transaction's locks.
func ReadSessionMiddleware(handlerContext *HandlerContext, authRequired bool) echo.MiddlewareFunc {
	return sessionMiddleware(handlerContext, authRequired, true, false)
}

// BatchSessionMiddleware is for handlers whose successful responses always
// follow a DB query. Generic handlers must use the eager SessionMiddleware.
func BatchSessionMiddleware(h *HandlerContext, required bool) echo.MiddlewareFunc {
	return sessionMiddleware(h, required, false, true)
}

func sessionMiddleware(handlerContext *HandlerContext, authRequired, includeUser, batch bool) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if batch && authRequired && sessionBatchEnabled(handlerContext.DB.DriverName()) {
				header := c.Request().Header.Get(echo.HeaderAuthorization)
				if !strings.HasPrefix(header, "Bearer ") {
					return NewAPIError(http.StatusUnauthorized, "invalid_session")
				}
				hash := sha256.Sum256([]byte(strings.TrimPrefix(header, "Bearer ")))
				marker := "deferred-actor-" + uuid.NewString()
				state := &batchSession{readonly: c.Request().Method == http.MethodGet, marker: marker, session: &Session{UserID: marker, TokenHash: hex.EncodeToString(hash[:])}}
				ctx := context.WithValue(c.Request().Context(), batchSessionKey{}, state)
				c.SetRequest(c.Request().WithContext(ctx))
				c.Set("session", state.session)
				err := next(c)
				if !state.attempted {
					// Binding/validation can fail before any SQL. Authenticate
					// anyway so 401/region errors retain their original priority.
					state.attempted = true
					var actor string
					db := handlerContext.DB
					if state.readonly {
						db = handlerContext.ReadDB
					}
					checkErr := db.GetContext(ctx, &actor, `SELECT user_id FROM optimized_auth($1,$2)`, state.session.TokenHash, state.region)
					if checkErr != nil {
						return sessionBatchError(checkErr)
					}
				}
				return err
			}
			session, err := loadSessionFromHeader(c, handlerContext.DB, includeUser)
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
	tx, err := db.Beginx()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	var lockedUserID string
	if err := tx.Get(&lockedUserID, `SELECT id FROM users WHERE id = $1 FOR NO KEY UPDATE`, userID); err != nil {
		return fmt.Errorf("failed to lock user: %w", err)
	}

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
	session, err := createLoginSessionTx(ctx, tx, userID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}
	return session, nil
}

func createLoginSessionTx(ctx context.Context, tx *sqlx.Tx, userID string) (*Session, error) {
	randomToken := uuid.NewString()
	tokenHashBytes := sha256.Sum256([]byte(randomToken))
	tokenHash := hex.EncodeToString(tokenHashBytes[:])
	var lockedUserID string
	if err := tx.GetContext(ctx, &lockedUserID, `SELECT id FROM users WHERE id = $1 AND status = 'active' FOR NO KEY UPDATE`, userID); err != nil {
		return nil, fmt.Errorf("failed to lock user: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
  UPDATE login_sessions
  SET active = false, invalidated_at = clock_timestamp(), reason = 'replaced'
  WHERE user_id = $1 AND active = true
 `, userID); err != nil {
		return nil, fmt.Errorf("failed to invalidate sessions: %w", err)
	}
	sessionID := uuid.NewString()
	var session Session
	if err := tx.GetContext(ctx, &session, `
      INSERT INTO login_sessions (id, user_id, token_hash) VALUES ($1, $2, $3)
      RETURNING user_id, token_hash, created_at
    `, sessionID, userID, tokenHash); err != nil {
		return nil, fmt.Errorf("failed to insert session: %w", err)
	}
	session.Token = randomToken
	return &session, nil
}

func getLoginSession(ctx context.Context, db sqlx.QueryerContext, sessionID string) (*Session, error) {
	var session Session
	if err := sqlx.GetContext(ctx, db, &session, `SELECT user_id, token_hash, created_at FROM login_sessions WHERE id = $1`, sessionID); err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}
	return &session, nil
}

func readSessionUser(c *echo.Context, h *HandlerContext) (User, error) {
	session := GetSession(c)
	if session.user != nil {
		user := *session.user
		return user, user.setImageKey()
	}
	return getUser(c.Request().Context(), h.ReadDB, session.UserID)
}
