package httpapi

import (
	"errors"
	"github.com/isucon/isucon2026/backend/internal/service"
	"github.com/labstack/echo/v5"
	"net/http"
)

type PushSubscriptionHandler struct {
	service *service.PushSubscriptionService
}

func NewPushSubscriptionHandler(service *service.PushSubscriptionService) *PushSubscriptionHandler {
	return &PushSubscriptionHandler{service: service}
}
func (h *PushSubscriptionHandler) Replace(c *echo.Context) error {
	user, ok := Principal(c)
	if !ok {
		return Unauthorized("invalid_session")
	}
	token, err := bearerToken(c.Request().Header.Get(echo.HeaderAuthorization))
	if err != nil {
		return Unauthorized("invalid_session")
	}
	var input struct {
		Endpoint string `json:"endpoint"`
		Keys     struct {
			P256dh string `json:"p256dh"`
			Auth   string `json:"auth"`
		} `json:"keys"`
	}
	if err := DecodeJSON(c, &input); err != nil {
		return err
	}
	if err := h.service.Replace(c.Request().Context(), user.ID, token, input.Endpoint, input.Keys.P256dh, input.Keys.Auth); err != nil {
		if errors.Is(err, service.ErrInvalidPushSubscription) {
			return BadRequest("invalid_push_subscription", err)
		}
		return err
	}
	return c.NoContent(http.StatusNoContent)
}
func (h *PushSubscriptionHandler) Remove(c *echo.Context) error {
	user, ok := Principal(c)
	if !ok {
		return Unauthorized("invalid_session")
	}
	token, err := bearerToken(c.Request().Header.Get(echo.HeaderAuthorization))
	if err != nil {
		return Unauthorized("invalid_session")
	}
	if err := h.service.Remove(c.Request().Context(), user.ID, token); err != nil {
		return Unauthorized("invalid_session")
	}
	return c.NoContent(http.StatusNoContent)
}
