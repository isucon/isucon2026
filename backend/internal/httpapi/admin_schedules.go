package httpapi

import (
	"github.com/isucon/isucon2026/backend/internal/domain"
	"github.com/isucon/isucon2026/backend/internal/service"
	"github.com/labstack/echo/v5"
	"log/slog"
	"net/http"
	"time"
)

type AdminScheduleHandler struct {
	service *service.ScheduleService
	logger  *slog.Logger
}

func NewAdminScheduleHandler(service *service.ScheduleService, logger *slog.Logger) *AdminScheduleHandler {
	return &AdminScheduleHandler{service: service, logger: logger}
}
func (h *AdminScheduleHandler) Set(c *echo.Context) error {
	admin, ok := AdministratorPrincipal(c)
	if !ok {
		return Unauthorized("invalid_administrator_session")
	}
	var input struct {
		ScheduledAt time.Time `json:"scheduledAt"`
	}
	if err := DecodeJSON(c, &input); err != nil {
		return err
	}
	region := domain.Region(c.Param("region"))
	if err := h.service.Set(c.Request().Context(), region, input.ScheduledAt); err != nil {
		h.log(c, "set_schedule", "conflict", admin.ID.String(), string(region))
		return Conflict("schedule_conflict")
	}
	h.log(c, "set_schedule", "success", admin.ID.String(), string(region))
	return c.JSON(http.StatusOK, map[string]any{"region": region, "scheduledAt": input.ScheduledAt})
}
func (h *AdminScheduleHandler) Cancel(c *echo.Context) error {
	admin, ok := AdministratorPrincipal(c)
	if !ok {
		return Unauthorized("invalid_administrator_session")
	}
	region := domain.Region(c.Param("region"))
	if err := h.service.Cancel(c.Request().Context(), region); err != nil {
		return Conflict("schedule_conflict")
	}
	h.log(c, "cancel_schedule", "success", admin.ID.String(), string(region))
	return c.NoContent(http.StatusNoContent)
}
func (h *AdminScheduleHandler) log(c *echo.Context, operation, result, admin, region string) {
	h.logger.Info("notification schedule", "operation", operation, "result", result, "correlationId", CorrelationIDFrom(c), "administratorId", admin, "region", region)
}
