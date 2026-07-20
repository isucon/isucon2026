package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/repository/postgres"
	"github.com/isucon/isucon2026/backend/internal/service"
	"github.com/labstack/echo/v5"
)

type AdminUserHandler struct {
	service *service.AdminService
	logger  *slog.Logger
}

func NewAdminUserHandler(service *service.AdminService, logger *slog.Logger) *AdminUserHandler {
	return &AdminUserHandler{service: service, logger: logger}
}
func (h *AdminUserHandler) Login(c *echo.Context) error {
	var input loginRequest
	if err := DecodeJSON(c, &input); err != nil {
		return err
	}
	result, err := h.service.Login(c.Request().Context(), input.Username, input.Password)
	if err != nil {
		h.logger.Info("administrator authentication", "operation", "admin_login", "result", "authentication_failed", "correlationId", CorrelationIDFrom(c))
		return Unauthorized("invalid_credentials")
	}
	h.logger.Info("administrator authentication", "operation", "admin_login", "result", "success", "correlationId", CorrelationIDFrom(c), "administratorId", result.AdministratorID.String())
	return c.JSON(http.StatusOK, map[string]any{"token": result.Token, "administratorId": result.AdministratorID})
}
func (h *AdminUserHandler) Certify(c *echo.Context) error {
	administrator, ok := AdministratorPrincipal(c)
	if !ok {
		return Unauthorized("invalid_administrator_session")
	}
	target, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		return NotFound("user_not_found")
	}
	if err := h.service.Certify(c.Request().Context(), target); err != nil {
		h.logger.Info("certification", "operation", "certify_user", "result", "conflict", "correlationId", CorrelationIDFrom(c), "administratorId", administrator.ID.String(), "targetUserId", target.String())
		if errors.Is(err, postgres.ErrNotFound) {
			return NotFound("user_not_found")
		}
		return Conflict("certification_conflict")
	}
	h.logger.Info("certification", "operation", "certify_user", "result", "success", "correlationId", CorrelationIDFrom(c), "administratorId", administrator.ID.String(), "targetUserId", target.String())
	return c.NoContent(http.StatusNoContent)
}
func (h *AdminUserHandler) RejectRemoval(c *echo.Context) error {
	administrator, ok := AdministratorPrincipal(c)
	if !ok {
		return Unauthorized("invalid_administrator_session")
	}
	h.logger.Info("certification", "operation", "remove_certification", "result", "conflict", "correlationId", CorrelationIDFrom(c), "administratorId", administrator.ID.String())
	return Conflict("certification_irreversible")
}
