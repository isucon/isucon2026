package httpapi

import "context"

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
	"time"
)

type InteractionHandler struct {
	service *service.InteractionService
	cursor  *CursorCodec
	logger  *slog.Logger
}

func NewInteractionHandler(service *service.InteractionService, cursor *CursorCodec, logger *slog.Logger) *InteractionHandler {
	return &InteractionHandler{service: service, cursor: cursor, logger: logger}
}
func (h *InteractionHandler) ReplaceRealMoji(c *echo.Context) error {
	actor, postID, err := interactionPrincipal(c, "postId")
	if err != nil {
		return err
	}
	if err := c.Request().ParseMultipartForm(4 << 20); err != nil {
		return BadRequest("invalid_realmoji", err)
	}
	if err := rejectMultipartCredentialSource(c.Request()); err != nil {
		return err
	}
	file, _, err := c.Request().FormFile("image")
	if err != nil {
		return BadRequest("invalid_realmoji", err)
	}
	defer file.Close()
	record, err := h.service.ReplaceRealMoji(c.Request().Context(), actor.ID, postID, domain.ReactionType(c.Request().FormValue("reactionType")), file)
	if err != nil {
		return interactionHTTPError(err)
	}
	h.log(c, "replace_realmoji", "success", actor.ID, postID)
	return c.JSON(http.StatusCreated, realMojiResponse(record))
}
func (h *InteractionHandler) CreateComment(c *echo.Context) error {
	actor, postID, err := interactionPrincipal(c, "postId")
	if err != nil {
		return err
	}
	var input struct {
		Body string `json:"body"`
	}
	if err := DecodeJSON(c, &input); err != nil {
		return err
	}
	record, err := h.service.CreateComment(c.Request().Context(), actor.ID, postID, input.Body)
	if err != nil {
		return interactionHTTPError(err)
	}
	h.log(c, "create_comment", "success", actor.ID, postID)
	return c.JSON(http.StatusCreated, commentResponse(record))
}
func (h *InteractionHandler) DeleteRealMoji(c *echo.Context) error {
	return h.delete(c, "realMojiId", "delete_realmoji", h.service.DeleteRealMoji)
}
func (h *InteractionHandler) DeleteComment(c *echo.Context) error {
	return h.delete(c, "commentId", "delete_comment", h.service.DeleteComment)
}
func (h *InteractionHandler) delete(c *echo.Context, param, operation string, action func(context.Context, uuid.UUID, uuid.UUID) error) error {
	actor, ok := Principal(c)
	if !ok {
		return Unauthorized("invalid_session")
	}
	id, err := uuid.Parse(c.Param(param))
	if err != nil {
		return NotFound("interaction_not_found")
	}
	if err := action(c.Request().Context(), actor.ID, id); err != nil {
		return interactionHTTPError(err)
	}
	h.log(c, operation, "success", actor.ID, uuid.Nil)
	return c.NoContent(http.StatusNoContent)
}
func (h *InteractionHandler) ListRealMojis(c *echo.Context) error {
	actor, postID, err := interactionPrincipal(c, "postId")
	if err != nil {
		return err
	}
	at, id, err := h.decodeCursor(c, "realmojis:"+postID.String(), Descending)
	if err != nil {
		return err
	}
	records, err := h.service.ListRealMojis(c.Request().Context(), actor.ID, postID, at, id, 51)
	if err != nil {
		return interactionHTTPError(err)
	}
	var next any
	if len(records) > 50 {
		records = records[:50]
		last := records[49].RealMoji
		next, _ = h.cursor.Encode(Cursor{List: "realmojis:" + postID.String(), Direction: Descending, Values: []string{last.CreatedAt.Format(time.RFC3339Nano), last.ID.String()}})
	}
	items := make([]map[string]any, len(records))
	for index := range records {
		items[index] = realMojiResponse(records[index])
	}
	return c.JSON(http.StatusOK, map[string]any{"items": items, "nextCursor": next})
}
func (h *InteractionHandler) ListComments(c *echo.Context) error {
	actor, postID, err := interactionPrincipal(c, "postId")
	if err != nil {
		return err
	}
	at, id, err := h.decodeCursor(c, "comments:"+postID.String(), Ascending)
	if err != nil {
		return err
	}
	records, err := h.service.ListComments(c.Request().Context(), actor.ID, postID, at, id, 51)
	if err != nil {
		return interactionHTTPError(err)
	}
	var next any
	if len(records) > 50 {
		records = records[:50]
		last := records[49].Comment
		next, _ = h.cursor.Encode(Cursor{List: "comments:" + postID.String(), Direction: Ascending, Values: []string{last.CreatedAt.Format(time.RFC3339Nano), last.ID.String()}})
	}
	items := make([]map[string]any, len(records))
	for index := range records {
		items[index] = commentResponse(records[index])
	}
	return c.JSON(http.StatusOK, map[string]any{"items": items, "nextCursor": next})
}
func (h *InteractionHandler) decodeCursor(c *echo.Context, list string, direction CursorDirection) (*time.Time, *uuid.UUID, error) {
	token := c.QueryParam("cursor")
	if token == "" {
		return nil, nil, nil
	}
	decoded, err := h.cursor.Decode(token, list, direction)
	if err != nil || len(decoded.Values) != 2 {
		return nil, nil, BadRequest("invalid_cursor", err)
	}
	at, err1 := time.Parse(time.RFC3339Nano, decoded.Values[0])
	id, err2 := uuid.Parse(decoded.Values[1])
	if err1 != nil || err2 != nil {
		return nil, nil, BadRequest("invalid_cursor", ErrInvalidCursor)
	}
	return &at, &id, nil
}
func interactionPrincipal(c *echo.Context, param string) (domain.User, uuid.UUID, error) {
	actor, ok := Principal(c)
	if !ok {
		return actor, uuid.Nil, Unauthorized("invalid_session")
	}
	id, err := uuid.Parse(c.Param(param))
	if err != nil {
		return actor, uuid.Nil, NotFound("post_not_found")
	}
	return actor, id, nil
}
func realMojiResponse(record domain.RealMojiRecord) map[string]any {
	return map[string]any{"id": record.RealMoji.ID, "reactor": userSummary(record.Reactor), "reactionType": record.RealMoji.ReactionType, "imageUrl": "/api/v1/images/" + record.RealMoji.ImageID.String(), "createdAt": record.RealMoji.CreatedAt}
}
func commentResponse(record domain.CommentRecord) map[string]any {
	return map[string]any{"id": record.Comment.ID, "author": userSummary(record.Author), "body": record.Comment.Body, "createdAt": record.Comment.CreatedAt}
}
func interactionHTTPError(err error) error {
	switch {
	case errors.Is(err, service.ErrInteractionNotAllowed), errors.Is(err, postgres.ErrInteractionForbidden):
		return Forbidden("interaction_forbidden")
	case errors.Is(err, postgres.ErrNotFound):
		return NotFound("interaction_not_found")
	case errors.Is(err, service.ErrInvalidPost), errors.Is(err, postgres.ErrInvalid):
		return BadRequest("invalid_interaction", err)
	case errors.Is(err, isurealimage.ErrPayloadTooLarge):
		return PayloadTooLarge()
	case errors.Is(err, isurealimage.ErrUnsupportedMedia):
		return UnsupportedMedia()
	case errors.Is(err, isurealimage.ErrSanitizerUnavailable):
		return ServiceUnavailable("image_sanitizer_unavailable", err)
	}
	return err
}
func (h *InteractionHandler) log(c *echo.Context, operation, result string, user, post uuid.UUID) {
	attributes := []any{"operation", operation, "result", result, "correlationId", CorrelationIDFrom(c), "userId", user.String()}
	if post != uuid.Nil {
		attributes = append(attributes, "postId", post.String())
	}
	h.logger.Info("interaction", attributes...)
}
