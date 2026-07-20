package httpapi

import (
	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/service"
	"github.com/labstack/echo/v5"
	"net/http"
	"time"
)

type NotificationHandler struct {
	service *service.NotificationService
	cursor  *CursorCodec
}

func NewNotificationHandler(service *service.NotificationService, cursor *CursorCodec) *NotificationHandler {
	return &NotificationHandler{service: service, cursor: cursor}
}
func (h *NotificationHandler) List(c *echo.Context) error {
	user, ok := Principal(c)
	if !ok {
		return Unauthorized("invalid_session")
	}
	var at *time.Time
	var id *uuid.UUID
	if token := c.QueryParam("cursor"); token != "" {
		decoded, err := h.cursor.Decode(token, "notifications", Descending)
		if err != nil || len(decoded.Values) != 2 {
			return BadRequest("invalid_cursor", err)
		}
		parsedAt, e1 := time.Parse(time.RFC3339Nano, decoded.Values[0])
		parsedID, e2 := uuid.Parse(decoded.Values[1])
		if e1 != nil || e2 != nil {
			return BadRequest("invalid_cursor", ErrInvalidCursor)
		}
		at, id = &parsedAt, &parsedID
	}
	records, err := h.service.List(c.Request().Context(), user.ID, at, id, 51)
	if err != nil {
		return err
	}
	var next any
	if len(records) > 50 {
		records = records[:50]
		last := records[49]
		next, _ = h.cursor.Encode(Cursor{List: "notifications", Direction: Descending, Values: []string{last.CreatedAt.Format(time.RFC3339Nano), last.ID.String()}})
	}
	items := make([]map[string]any, len(records))
	for index, n := range records {
		items[index] = map[string]any{"id": n.ID, "postingWindowId": n.PostingWindowID, "region": n.Region, "createdAt": n.CreatedAt, "read": n.ReadAt != nil}
	}
	return c.JSON(http.StatusOK, map[string]any{"items": items, "nextCursor": next})
}
func (h *NotificationHandler) Open(c *echo.Context) error {
	user, ok := Principal(c)
	if !ok {
		return Unauthorized("invalid_session")
	}
	id, err := uuid.Parse(c.Param("notificationId"))
	if err != nil {
		return NotFound("notification_not_found")
	}
	destination, path, err := h.service.Open(c.Request().Context(), user.ID, id)
	if err != nil {
		return NotFound("notification_not_found")
	}
	return c.JSON(http.StatusOK, map[string]any{"destination": destination, "path": path})
}
