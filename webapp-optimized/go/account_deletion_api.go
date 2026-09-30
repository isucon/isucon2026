package isureal

import (
	"net/http"

	"github.com/labstack/echo/v5"
)

func deleteAccountHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		if _, err := runWrite(c.Request().Context(), h.DB, "account_delete", GetSession(c).UserID, nil, nil); err != nil {
			return err
		}
		return c.NoContent(http.StatusNoContent)
	}
}
