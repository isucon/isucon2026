package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/domain"
	"github.com/isucon/isucon2026/backend/internal/repository/postgres"
	"github.com/isucon/isucon2026/backend/internal/service"
	"github.com/labstack/echo/v5"
)

type RelationshipHandler struct {
	service *service.RelationshipService
	cursor  *CursorCodec
	logger  *slog.Logger
}

func NewRelationshipHandler(service *service.RelationshipService, cursor *CursorCodec, logger *slog.Logger) *RelationshipHandler {
	return &RelationshipHandler{service: service, cursor: cursor, logger: logger}
}
func (h *RelationshipHandler) Search(c *echo.Context) error {
	user, ok := Principal(c)
	if !ok {
		return Unauthorized("invalid_session")
	}
	q := c.QueryParam("q")
	var beforeName *string
	var beforeID *uuid.UUID
	if token := c.QueryParam("cursor"); token != "" {
		decoded, err := h.cursor.Decode(token, "users:"+domain.NormalizeSearchText(q), Ascending)
		if err != nil || len(decoded.Values) != 2 {
			return BadRequest("invalid_cursor", err)
		}
		name := decoded.Values[0]
		id, err := uuid.Parse(decoded.Values[1])
		if err != nil {
			return BadRequest("invalid_cursor", err)
		}
		beforeName, beforeID = &name, &id
	}
	users, err := h.service.Search(c.Request().Context(), user.ID, q, beforeName, beforeID, 51)
	if err != nil {
		return relationshipHTTPError(err)
	}
	var next any
	if len(users) > 50 {
		users = users[:50]
		last := users[49]
		token, _ := h.cursor.Encode(Cursor{List: "users:" + domain.NormalizeSearchText(q), Direction: Ascending, Values: []string{last.UsernameNormalized, last.ID.String()}})
		next = token
	}
	items := make([]map[string]any, len(users))
	for index := range users {
		items[index] = userSummary(users[index])
	}
	h.log(c, "search_users", "success", user.ID, uuid.Nil)
	return c.JSON(http.StatusOK, map[string]any{"items": items, "nextCursor": next})
}
func (h *RelationshipHandler) GetUser(c *echo.Context) error {
	viewer, ok := Principal(c)
	if !ok {
		return Unauthorized("invalid_session")
	}
	target, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		return NotFound("user_not_found")
	}
	user, err := h.service.GetUser(c.Request().Context(), viewer.ID, target)
	if err != nil {
		return relationshipHTTPError(err)
	}
	h.log(c, "get_user", "success", viewer.ID, target)
	return c.JSON(http.StatusOK, userSummary(user))
}
func (h *RelationshipHandler) CreateRequest(c *echo.Context) error {
	actor, ok := Principal(c)
	if !ok {
		return Unauthorized("invalid_session")
	}
	var input struct {
		RecipientID uuid.UUID `json:"recipientId"`
	}
	if err := DecodeJSON(c, &input); err != nil {
		return err
	}
	request, err := h.service.CreateRequest(c.Request().Context(), actor.ID, input.RecipientID)
	if err != nil {
		return relationshipHTTPError(err)
	}
	h.log(c, "create_friend_request", "success", actor.ID, input.RecipientID)
	return c.JSON(http.StatusCreated, map[string]any{"relationId": request.ID, "status": request.Status, "updatedAt": request.UpdatedAt})
}
func (h *RelationshipHandler) Transition(c *echo.Context) error {
	actor, ok := Principal(c)
	if !ok {
		return Unauthorized("invalid_session")
	}
	id, err := uuid.Parse(c.Param("requestId"))
	if err != nil {
		return NotFound("friend_request_not_found")
	}
	request, err := h.service.Transition(c.Request().Context(), actor.ID, id, c.Param("action"))
	if err != nil {
		return relationshipHTTPError(err)
	}
	h.log(c, "transition_friend_request", "success", actor.ID, uuid.Nil)
	return c.JSON(http.StatusOK, map[string]any{"relationId": request.ID, "status": request.Status, "updatedAt": request.UpdatedAt})
}
func (h *RelationshipHandler) List(c *echo.Context) error {
	actor, ok := Principal(c)
	if !ok {
		return Unauthorized("invalid_session")
	}
	kind := domain.RelationshipKind(c.Param("kind"))
	list := "relationships:" + string(kind)
	var beforeAt *time.Time
	var beforeID *uuid.UUID
	if token := c.QueryParam("cursor"); token != "" {
		decoded, err := h.cursor.Decode(token, list, Descending)
		if err != nil || len(decoded.Values) != 2 {
			return BadRequest("invalid_cursor", err)
		}
		at, err1 := time.Parse(time.RFC3339Nano, decoded.Values[0])
		id, err2 := uuid.Parse(decoded.Values[1])
		if err1 != nil || err2 != nil {
			return BadRequest("invalid_cursor", ErrInvalidCursor)
		}
		beforeAt, beforeID = &at, &id
	}
	records, err := h.service.List(c.Request().Context(), actor.ID, kind, beforeAt, beforeID, 51)
	if err != nil {
		return relationshipHTTPError(err)
	}
	var next any
	if len(records) > 50 {
		records = records[:50]
		last := records[49]
		token, _ := h.cursor.Encode(Cursor{List: list, Direction: Descending, Values: []string{last.UpdatedAt.Format(time.RFC3339Nano), last.RelationID.String()}})
		next = token
	}
	items := make([]map[string]any, len(records))
	for index, record := range records {
		items[index] = map[string]any{"relationId": record.RelationID, "user": userSummary(record.User), "status": record.Status, "updatedAt": record.UpdatedAt}
	}
	h.log(c, "list_relationships", "success", actor.ID, uuid.Nil)
	return c.JSON(http.StatusOK, map[string]any{"items": items, "nextCursor": next})
}
func (h *RelationshipHandler) DeleteFriendship(c *echo.Context) error {
	return h.targetMutation(c, "delete_friendship", h.service.DeleteFriendship)
}
func (h *RelationshipHandler) Follow(c *echo.Context) error {
	return h.targetMutation(c, "follow_user", h.service.Follow)
}
func (h *RelationshipHandler) Unfollow(c *echo.Context) error {
	return h.targetMutation(c, "unfollow_user", h.service.Unfollow)
}
func (h *RelationshipHandler) Block(c *echo.Context) error {
	return h.targetMutation(c, "block_user", h.service.Block)
}
func (h *RelationshipHandler) Unblock(c *echo.Context) error {
	return h.targetMutation(c, "unblock_user", h.service.Unblock)
}
func (h *RelationshipHandler) targetMutation(c *echo.Context, operation string, action func(context.Context, uuid.UUID, uuid.UUID) error) error {
	actor, ok := Principal(c)
	if !ok {
		return Unauthorized("invalid_session")
	}
	target, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		return NotFound("user_not_found")
	}
	if err := action(c.Request().Context(), actor.ID, target); err != nil {
		h.log(c, operation, "failure", actor.ID, target)
		return relationshipHTTPError(err)
	}
	h.log(c, operation, "success", actor.ID, target)
	return c.NoContent(http.StatusNoContent)
}
func userSummary(user domain.User) map[string]any {
	return map[string]any{"id": user.ID, "username": user.Username, "displayName": user.DisplayName, "profileImageUrl": nil, "bio": user.Bio, "accountType": user.AccountType}
}
func relationshipHTTPError(err error) error {
	switch {
	case errors.Is(err, postgres.ErrNotFound):
		return NotFound("relationship_not_found")
	case errors.Is(err, postgres.ErrRelationshipForbidden), errors.Is(err, domain.ErrInvalidTransition):
		return Forbidden("relationship_action_forbidden")
	case errors.Is(err, postgres.ErrInvalid), errors.Is(err, domain.ErrSelfRelationship):
		return BadRequest("invalid_relationship", err)
	case errors.Is(err, postgres.ErrRelationshipConflict), errors.Is(err, postgres.ErrConflict):
		return Conflict("relationship_conflict")
	}
	return err
}
func (h *RelationshipHandler) log(c *echo.Context, operation, result string, actor, target uuid.UUID) {
	attributes := []any{"operation", operation, "result", result, "correlationId", CorrelationIDFrom(c), "userId", actor.String()}
	if target != uuid.Nil {
		attributes = append(attributes, "targetUserId", target.String())
	}
	h.logger.Info("relationship", attributes...)
}
