package isureal

import (
	"net/http"

	"github.com/labstack/echo/v5"
)

type deleteFriendshipRequest struct {
	UserID string `param:"userId" json:"-"`
}

func deleteFriendshipHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input deleteFriendshipRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		if _, err := runWrite(c.Request().Context(), h.DB, "friend_delete", GetSession(c).UserID,
			map[string]any{"target": input.UserID}, nil); err != nil {
			return err
		}
		return c.NoContent(http.StatusNoContent)
	}
}

type followRequest struct {
	UserID string `param:"userId" json:"-"`
}

func followHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input followRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		if _, err := runWrite(c.Request().Context(), h.DB, "follow", GetSession(c).UserID,
			map[string]any{"target": input.UserID}, nil); err != nil {
			return err
		}
		return c.NoContent(http.StatusNoContent)
	}
}

type unfollowRequest struct {
	UserID string `param:"userId" json:"-"`
}

func unfollowHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input unfollowRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		if _, err := runWrite(c.Request().Context(), h.DB, "unfollow", GetSession(c).UserID,
			map[string]any{"target": input.UserID}, nil); err != nil {
			return err
		}
		return c.NoContent(http.StatusNoContent)
	}
}

type blockRequest struct {
	UserID string `param:"userId" json:"-"`
}

func blockHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input blockRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		if _, err := runWrite(c.Request().Context(), h.DB, "block", GetSession(c).UserID,
			map[string]any{"target": input.UserID}, nil); err != nil {
			return err
		}
		return c.NoContent(http.StatusNoContent)
	}
}

type unblockRequest struct {
	UserID string `param:"userId" json:"-"`
}

func unblockHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input unblockRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		if _, err := runWrite(c.Request().Context(), h.DB, "unblock", GetSession(c).UserID,
			map[string]any{"target": input.UserID}, nil); err != nil {
			return err
		}
		return c.NoContent(http.StatusNoContent)
	}
}
