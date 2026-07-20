package httpapi

import (
	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/domain"
	"github.com/isucon/isucon2026/backend/internal/service"
	"github.com/labstack/echo/v5"
	"net/http"
	"time"
)

type MemoryHandler struct {
	service *service.MemoryService
	cursor  *CursorCodec
}

func NewMemoryHandler(service *service.MemoryService, cursor *CursorCodec) *MemoryHandler {
	return &MemoryHandler{service: service, cursor: cursor}
}
func (h *MemoryHandler) Month(c *echo.Context) error { return h.list(c, c.QueryParam("month"), false) }
func (h *MemoryHandler) Date(c *echo.Context) error  { return h.list(c, c.Param("date"), true) }
func (h *MemoryHandler) list(c *echo.Context, value string, date bool) error {
	actor, ok := Principal(c)
	if !ok {
		return Unauthorized("invalid_session")
	}
	list := "memories:" + value
	var at *time.Time
	var id *uuid.UUID
	if token := c.QueryParam("cursor"); token != "" {
		decoded, err := h.cursor.Decode(token, list, Descending)
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
	var records []domain.MemoryRecord
	var err error
	if date {
		records, err = h.service.Date(c.Request().Context(), actor.ID, value, at, id, 51)
	} else {
		records, err = h.service.Month(c.Request().Context(), actor.ID, value, at, id, 51)
	}
	if err != nil {
		return BadRequest("invalid_memory_period", err)
	}
	var next any
	if len(records) > 50 {
		records = records[:50]
		last := records[49].PostRecord.Post
		next, _ = h.cursor.Encode(Cursor{List: list, Direction: Descending, Values: []string{last.PostedAt.Format(time.RFC3339Nano), last.ID.String()}})
	}
	items := make([]map[string]any, len(records))
	for index := range records {
		items[index] = postResponse(records[index].PostRecord)
		items[index]["realmojis"] = records[index].RealMojis
		items[index]["comments"] = records[index].Comments
	}
	return c.JSON(http.StatusOK, map[string]any{"items": items, "nextCursor": next})
}
