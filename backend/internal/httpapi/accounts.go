package httpapi

import (
	"errors"
	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/domain"
	isurealimage "github.com/isucon/isucon2026/backend/internal/image"
	"github.com/isucon/isucon2026/backend/internal/repository/postgres"
	"github.com/isucon/isucon2026/backend/internal/service"
	"github.com/labstack/echo/v5"
	"log/slog"
	"net/http"
)

type AccountHandler struct {
	service *service.AccountService
	logger  *slog.Logger
}

func NewAccountHandler(service *service.AccountService, logger *slog.Logger) *AccountHandler {
	return &AccountHandler{service: service, logger: logger}
}
func (h *AccountHandler) Get(c *echo.Context) error {
	actor, ok := Principal(c)
	if !ok {
		return Unauthorized("invalid_session")
	}
	user, pending, err := h.service.Get(c.Request().Context(), actor.ID)
	if err != nil {
		return accountHTTPError(err)
	}
	return c.JSON(http.StatusOK, profileResponse(user, pending))
}
func (h *AccountHandler) Update(c *echo.Context) error {
	actor, ok := Principal(c)
	if !ok {
		return Unauthorized("invalid_session")
	}
	var input struct{ DisplayName, Bio, Region *string }
	if err := DecodeJSON(c, &input); err != nil {
		return err
	}
	user, pending, err := h.service.Update(c.Request().Context(), actor.ID, domain.ProfileUpdate{DisplayName: input.DisplayName, Bio: input.Bio, Region: input.Region})
	if err != nil {
		return accountHTTPError(err)
	}
	h.log(c, "update_profile", "success", actor.ID)
	return c.JSON(http.StatusOK, profileResponse(user, pending))
}
func (h *AccountHandler) PutImage(c *echo.Context) error {
	actor, ok := Principal(c)
	if !ok {
		return Unauthorized("invalid_session")
	}
	if c.Request().Header.Get(echo.HeaderContentType) != "image/jpeg" {
		return UnsupportedMedia()
	}
	if err := h.service.SetProfileImage(c.Request().Context(), actor.ID, c.Request().Body); err != nil {
		return accountHTTPError(err)
	}
	h.log(c, "put_profile_image", "success", actor.ID)
	return c.NoContent(http.StatusNoContent)
}
func (h *AccountHandler) DeleteImage(c *echo.Context) error {
	actor, ok := Principal(c)
	if !ok {
		return Unauthorized("invalid_session")
	}
	if err := h.service.DeleteProfileImage(c.Request().Context(), actor.ID); err != nil {
		return accountHTTPError(err)
	}
	h.log(c, "delete_profile_image", "success", actor.ID)
	return c.NoContent(http.StatusNoContent)
}
func (h *AccountHandler) DeleteAccount(c *echo.Context) error {
	actor, ok := Principal(c)
	if !ok {
		return Unauthorized("invalid_session")
	}
	due, err := h.service.RequestDeletion(c.Request().Context(), actor.ID)
	if err != nil {
		return accountHTTPError(err)
	}
	h.log(c, "request_account_deletion", "success", actor.ID)
	return c.JSON(http.StatusAccepted, map[string]any{"purgeDueAt": due})
}
func profileResponse(user domain.User, pending *string) map[string]any {
	var image any
	if user.ProfileImageID != nil {
		image = "/api/v1/images/" + user.ProfileImageID.String()
	}
	return map[string]any{"id": user.ID, "username": user.Username, "displayName": user.DisplayName, "profileImageUrl": image, "bio": user.Bio, "region": user.Region, "pendingRegion": pending, "accountType": user.AccountType, "status": user.Status}
}
func accountHTTPError(err error) error {
	switch {
	case errors.Is(err, postgres.ErrNotFound):
		return NotFound("account_not_found")
	case errors.Is(err, postgres.ErrConflict):
		return Conflict("account_conflict")
	case errors.Is(err, domain.ErrInvalidDisplayName), errors.Is(err, domain.ErrInvalidBio), errors.Is(err, domain.ErrInvalidRegion):
		return BadRequest("invalid_profile", err)
	case errors.Is(err, isurealimage.ErrPayloadTooLarge):
		return PayloadTooLarge()
	case errors.Is(err, isurealimage.ErrUnsupportedMedia):
		return UnsupportedMedia()
	case errors.Is(err, isurealimage.ErrSanitizerUnavailable):
		return ServiceUnavailable("image_sanitizer_unavailable", err)
	}
	return err
}
func (h *AccountHandler) log(c *echo.Context, operation, result string, user uuid.UUID) {
	h.logger.Info("account", "operation", operation, "result", result, "correlationId", CorrelationIDFrom(c), "userId", user.String())
}
