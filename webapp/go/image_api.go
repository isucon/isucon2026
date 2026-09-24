package isureal

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/labstack/echo/v5"
)

type getImageRequest struct {
	ImageID string `param:"imageId"`
}

func getImageHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input getImageRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		ctx := c.Request().Context()
		viewer := GetSession(c).UserID
		var ownerID string
		allowed := false
		err := h.DB.GetContext(ctx, &ownerID, `SELECT id FROM users WHERE profile_image_id=$1 AND status='active'`, input.ImageID)
		if err == nil {
			blocked, err := isBlocked(ctx, h.DB, viewer, ownerID)
			if err != nil {
				return err
			}
			allowed = !blocked
		} else if errors.Is(err, sql.ErrNoRows) {
			var postID string
			err := h.DB.GetContext(ctx, &postID, `SELECT id FROM posts WHERE image_id=$1 AND deleted_at IS NULL`, input.ImageID)
			if errors.Is(err, sql.ErrNoRows) {
				err = h.DB.GetContext(ctx, &postID, `SELECT post_id FROM realmojis WHERE image_id=$1 AND replaced_at IS NULL AND deleted_at IS NULL`, input.ImageID)
			}
			if errors.Is(err, sql.ErrNoRows) {
				return NewAPIError(http.StatusNotFound, "image_not_found")
			}
			if err != nil {
				return err
			}
			allowed, err = canViewPost(ctx, h.DB, viewer, postID)
			if err != nil {
				return err
			}
		} else {
			return err
		}
		if !allowed {
			return NewAPIError(http.StatusNotFound, "image_not_found")
		}
		var object struct {
			Data      []byte `db:"data"`
			MediaType string `db:"media_type"`
		}
		if err := h.DB.GetContext(ctx, &object, `SELECT data, media_type FROM image_objects WHERE id = $1`, input.ImageID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return NewAPIError(http.StatusNotFound, "image_not_found")
			}
			return err
		}
		return c.Blob(http.StatusOK, object.MediaType, object.Data)
	}
}
