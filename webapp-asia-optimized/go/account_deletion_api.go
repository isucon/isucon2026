package isureal

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/labstack/echo/v5"
)

func deleteAccountHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		ctx := c.Request().Context()
		userID := GetSession(c).UserID
		unlock := lockKeys(userKey(userID), sessionKey(userID), postingKey(userID), profileKey(userID))
		defer unlock()
		_, err := getUserCached(ctx, h.DB, userID)
		if errors.Is(err, sql.ErrNoRows) {
			return NewAPIError(http.StatusNotFound, "account_not_found")
		}
		if err != nil {
			return err
		}
		tx, err := h.DB.BeginTxx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(ctx, `UPDATE users SET status='deleted', deleted_at=clock_timestamp(), updated_at=clock_timestamp() WHERE id=$1`, userID); err != nil {
			return err
		}
		var invalidated []string
		if err := tx.SelectContext(ctx, &invalidated, `UPDATE login_sessions SET active=false, invalidated_at=clock_timestamp(), reason='account_deletion' WHERE user_id=$1 AND active=true RETURNING token_hash`, userID); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		userCache.invalidate(userID)
		// 退会したユーザーの投稿に付いた RealMoji の画像も表示できなくなる
		imageMetaCache.clear()
		sessionCache.invalidate(invalidated...)
		return c.NoContent(http.StatusNoContent)
	}
}
