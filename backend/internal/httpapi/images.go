package httpapi

import (
	"context"
	"io"
	"net/http"

	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/domain"
	isurealimage "github.com/isucon/isucon2026/backend/internal/image"
	"github.com/labstack/echo/v5"
)

type ImageAuthorizer interface {
	ResolveAuthorizedImage(context.Context, domain.User, uuid.UUID) (string, error)
}

type ImageHandler struct {
	store      *isurealimage.Store
	authorizer ImageAuthorizer
}

type MultiImageAuthorizer []ImageAuthorizer

func (authorizers MultiImageAuthorizer) ResolveAuthorizedImage(ctx context.Context, viewer domain.User, imageID uuid.UUID) (string, error) {
	var last error
	for _, authorizer := range authorizers {
		key, err := authorizer.ResolveAuthorizedImage(ctx, viewer, imageID)
		if err == nil {
			return key, nil
		}
		last = err
	}
	return "", last
}

func NewImageHandler(store *isurealimage.Store, authorizer ImageAuthorizer) *ImageHandler {
	return &ImageHandler{store: store, authorizer: authorizer}
}

func (h *ImageHandler) Get(c *echo.Context) error {
	viewer, ok := Principal(c)
	if !ok {
		return Unauthorized("invalid_session")
	}
	imageID, err := uuid.Parse(c.Param("imageId"))
	if err != nil {
		return NotFound("image_not_found")
	}
	objectKey, err := h.authorizer.ResolveAuthorizedImage(c.Request().Context(), viewer, imageID)
	if err != nil {
		return NotFound("image_not_found")
	}
	file, err := h.store.Open(objectKey)
	if err != nil {
		return NotFound("image_not_found")
	}
	defer file.Close()
	c.Response().Header().Set(echo.HeaderContentType, "image/jpeg")
	c.Response().WriteHeader(http.StatusOK)
	_, err = io.Copy(c.Response(), file)
	return err
}
