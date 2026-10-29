package isureal

import (
	"bytes"
	"image/jpeg"
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
)

func putProfileImageHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		if c.Request().Header.Get("Content-Type") != "image/jpeg" {
			return NewAPIError(http.StatusUnsupportedMediaType, "unsupported_media_type")
		}
		data, rejection, err := readJPEGClassified(c.Request().Body, 512, c.Request().ContentLength)
		if err != nil {
			return err
		}
		if rejection == jpegInvalid {
			logAbuseFailure(c, "put_profile_image", http.StatusBadRequest, "invalid_image")
		}
		if rejection != jpegAccepted {
			return NewAPIError(http.StatusBadRequest, "invalid_profile_image")
		}
		if _, err := jpeg.Decode(bytes.NewReader(data)); err != nil {
			logAbuseFailure(c, "put_profile_image", http.StatusBadRequest, "invalid_image")
			return NewAPIError(http.StatusBadRequest, "invalid_profile_image")
		}

		imageID := uuid.NewString()
		if err := storeUploadedImage(imageID, data); err != nil {
			return err
		}
		if _, err := runWrite(c.Request().Context(), h.DB, "profile_image_put", GetSession(c).UserID,
			map[string]any{"imageId": imageID}, []byte{}); err != nil {
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
