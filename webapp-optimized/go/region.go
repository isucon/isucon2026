package isureal

import (
	"net/http"

	"github.com/labstack/echo/v5"
)

func RegionMiddleware(h *HandlerContext) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if state, _ := c.Request().Context().Value(batchSessionKey{}).(*batchSession); state != nil && !state.attempted {
				state.region = h.Config.Region
				return next(c)
			}
			session := GetSession(c)
			if session == nil {
				return NewAPIError(http.StatusUnauthorized, "invalid_session")
			}
			if session.Region != h.Config.Region {
				return NewAPIError(http.StatusForbidden, "region_mismatch")
			}
			return next(c)
		}
	}
}
