package isureal

import (
	"net/http"

	"github.com/labstack/echo/v5"
)

func putProfileImageHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		if c.Request().Header.Get("Content-Type") != "image/jpeg" {
			return NewAPIError(http.StatusUnsupportedMediaType, "unsupported_media_type")
		}
		data, ok, err := readJPEG(c.Request().Body, 512)
		if err != nil {
			return err
		}
		if !ok {
			return NewAPIError(http.StatusBadRequest, "invalid_profile_image")
		}

		if _, err := runWrite(c.Request().Context(), h.DB, "profile_image_put", GetSession(c).UserID, nil, data); err != nil {
			return err
		}
		return c.NoContent(http.StatusNoContent)
	}
}

func deleteProfileImageHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		if _, err := runWrite(c.Request().Context(), h.DB, "profile_image_delete", GetSession(c).UserID, nil, nil); err != nil {
			return err
		}
		return c.NoContent(http.StatusNoContent)
	}
}
