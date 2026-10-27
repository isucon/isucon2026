package isureal

import (
	"net/http"

	"github.com/labstack/echo/v5"
)

func RegionMiddleware(h *HandlerContext) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			session := GetSession(c)
			if session == nil {
				return NewAPIError(http.StatusUnauthorized, "invalid_session")
			}
			if session.Region != requestRegion(c, h.Config) {
				return NewAPIError(http.StatusForbidden, "region_mismatch")
			}
			return next(c)
		}
	}
}

// nginx がリクエストを受けたサーバーのリージョンを X-Isureal-Region で渡す
func requestRegion(c *echo.Context, config *Config) string {
	if region := c.Request().Header.Get("X-Isureal-Region"); validRegion(region) {
		return region
	}
	return config.Region
}
