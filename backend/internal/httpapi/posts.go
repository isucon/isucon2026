package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/domain"
	isurealimage "github.com/isucon/isucon2026/backend/internal/image"
	"github.com/isucon/isucon2026/backend/internal/repository/postgres"
	"github.com/isucon/isucon2026/backend/internal/service"
	"github.com/labstack/echo/v5"
)

type PostHandler struct {
	posts  *service.PostService
	cursor *CursorCodec
	logger *slog.Logger
}

func NewPostHandler(posts *service.PostService, cursor *CursorCodec, logger *slog.Logger) *PostHandler {
	return &PostHandler{posts: posts, cursor: cursor, logger: logger}
}

func (h *PostHandler) CurrentWindow(c *echo.Context) error {
	user, ok := Principal(c)
	if !ok {
		return Unauthorized("invalid_session")
	}
	window, slot, err := h.posts.CurrentWindow(c.Request().Context(), user.ID)
	if errors.Is(err, postgres.ErrNoCurrentWindow) {
		h.log(c, "get_current_posting_window", "success", user.ID, uuid.Nil)
		return c.JSON(http.StatusOK, map[string]any{"window": nil, "hasPosted": false, "canPost": false, "deletionCount": 0})
	}
	if err != nil {
		h.log(c, "get_current_posting_window", "failure", user.ID, uuid.Nil)
		return postHTTPError(err)
	}
	h.log(c, "get_current_posting_window", "success", user.ID, uuid.Nil)
	return c.JSON(http.StatusOK, map[string]any{
		"window":    map[string]any{"id": window.ID, "region": window.Region, "localDate": window.LocalDate.Format("2006-01-02"), "startsAt": window.StartsAt, "onTimeDeadline": window.OnTimeDeadline},
		"hasPosted": slot.ActivePostID != nil, "canPost": slot.CanPost(), "deletionCount": slot.DeletionCount,
	})
}

func (h *PostHandler) Create(c *echo.Context) error {
	user, ok := Principal(c)
	if !ok {
		return Unauthorized("invalid_session")
	}
	request := c.Request()
	if err := request.ParseMultipartForm(12 << 20); err != nil {
		h.log(c, "create_post", "input_error", user.ID, uuid.Nil)
		return BadRequest("invalid_post", err)
	}
	if err := rejectMultipartCredentialSource(request); err != nil {
		return err
	}
	file, _, err := request.FormFile("image")
	if err != nil {
		return BadRequest("invalid_post", err)
	}
	defer file.Close()
	locationFile, _, err := request.FormFile("location")
	if err != nil {
		return BadRequest("invalid_location", err)
	}
	defer locationFile.Close()
	var location domain.Location
	if err := json.NewDecoder(locationFile).Decode(&location); err != nil {
		return BadRequest("invalid_location", err)
	}
	record, err := h.posts.Create(request.Context(), user.ID, service.CreatePostInput{
		Image: file, Caption: request.FormValue("caption"), Visibility: domain.Visibility(request.FormValue("visibility")), Location: location,
	})
	if err != nil {
		h.log(c, "create_post", postResult(err), user.ID, uuid.Nil)
		return postHTTPError(err)
	}
	h.log(c, "create_post", "success", user.ID, record.Post.ID)
	return c.JSON(http.StatusCreated, postResponse(record))
}

func (h *PostHandler) Get(c *echo.Context) error {
	return h.getOne(c, false)
}

func (h *PostHandler) CurrentSelf(c *echo.Context) error {
	return h.getOne(c, true)
}

func (h *PostHandler) getOne(c *echo.Context, self bool) error {
	user, ok := Principal(c)
	if !ok {
		return Unauthorized("invalid_session")
	}
	var record domain.PostRecord
	var err error
	operation := "get_post"
	if self {
		operation = "get_my_current_post"
		record, err = h.posts.CurrentSelf(c.Request().Context(), user.ID)
	} else {
		postID, parseErr := uuid.Parse(c.Param("postId"))
		if parseErr != nil {
			return NotFound("post_not_found")
		}
		record, err = h.posts.Get(c.Request().Context(), user.ID, postID)
	}
	if err != nil {
		h.log(c, operation, postResult(err), user.ID, uuid.Nil)
		return postHTTPError(err)
	}
	h.log(c, operation, "success", user.ID, record.Post.ID)
	return c.JSON(http.StatusOK, postResponse(record))
}

func (h *PostHandler) Update(c *echo.Context) error {
	user, ok := Principal(c)
	if !ok {
		return Unauthorized("invalid_session")
	}
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		return NotFound("post_not_found")
	}
	var raw struct {
		Caption    *string `json:"caption"`
		Visibility *string `json:"visibility"`
	}
	if err := DecodeJSON(c, &raw); err != nil {
		return err
	}
	var visibility *domain.Visibility
	if raw.Visibility != nil {
		value := domain.Visibility(*raw.Visibility)
		visibility = &value
	}
	record, err := h.posts.Update(c.Request().Context(), user.ID, postID, service.UpdatePostInput{Caption: raw.Caption, Visibility: visibility})
	if err != nil {
		h.log(c, "update_post", postResult(err), user.ID, postID)
		return postHTTPError(err)
	}
	h.log(c, "update_post", "success", user.ID, postID)
	return c.JSON(http.StatusOK, postResponse(record))
}

func (h *PostHandler) Delete(c *echo.Context) error {
	user, ok := Principal(c)
	if !ok {
		return Unauthorized("invalid_session")
	}
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		return NotFound("post_not_found")
	}
	if err := h.posts.Delete(c.Request().Context(), user.ID, postID); err != nil {
		h.log(c, "delete_post", postResult(err), user.ID, postID)
		return postHTTPError(err)
	}
	h.log(c, "delete_post", "success", user.ID, postID)
	return c.NoContent(http.StatusNoContent)
}

func (h *PostHandler) Feed(c *echo.Context) error {
	user, ok := Principal(c)
	if !ok {
		return Unauthorized("invalid_session")
	}
	kind := domain.FeedKind(c.Param("feed"))
	list := "feed:" + string(kind)
	var beforeTime *time.Time
	var beforeID *uuid.UUID
	if token := c.QueryParam("cursor"); token != "" {
		decoded, err := h.cursor.Decode(token, list, Descending)
		if err != nil || len(decoded.Values) != 2 {
			return BadRequest("invalid_cursor", err)
		}
		instant, timeErr := time.Parse(time.RFC3339Nano, decoded.Values[0])
		id, idErr := uuid.Parse(decoded.Values[1])
		if timeErr != nil || idErr != nil {
			return BadRequest("invalid_cursor", ErrInvalidCursor)
		}
		beforeTime, beforeID = &instant, &id
	}
	records, err := h.posts.Feed(c.Request().Context(), user.ID, kind, beforeTime, beforeID, 51)
	if err != nil {
		h.log(c, "get_feed", postResult(err), user.ID, uuid.Nil)
		return postHTTPError(err)
	}
	var next any
	if len(records) > 50 {
		records = records[:50]
		last := records[len(records)-1].Post
		token, err := h.cursor.Encode(Cursor{List: list, Direction: Descending, Values: []string{last.PostedAt.Format(time.RFC3339Nano), last.ID.String()}})
		if err != nil {
			return ServiceUnavailable("cursor_unavailable", err)
		}
		next = token
	}
	items := make([]map[string]any, len(records))
	for index := range records {
		items[index] = postResponse(records[index])
	}
	h.log(c, "get_feed", "success", user.ID, uuid.Nil)
	return c.JSON(http.StatusOK, map[string]any{"items": items, "nextCursor": next})
}

func postResponse(record domain.PostRecord) map[string]any {
	post := record.Post
	return map[string]any{
		"id": post.ID, "author": map[string]any{"id": record.Author.ID, "username": record.Author.Username, "displayName": record.Author.DisplayName, "profileImageUrl": nil, "bio": record.Author.Bio, "accountType": record.Author.AccountType},
		"postingWindowId": post.PostingWindowID, "imageUrl": "/api/v1/images/" + post.ImageID.String(), "caption": post.Caption,
		"visibility": post.Visibility, "location": post.Location, "postedAt": post.PostedAt, "onTime": post.OnTime, "lateSeconds": post.LateSeconds,
	}
}

func postHTTPError(err error) error {
	switch {
	case errors.Is(err, postgres.ErrNotFound), errors.Is(err, service.ErrPostNotVisible):
		return NotFound("post_not_found")
	case errors.Is(err, postgres.ErrPostNotEditable):
		return Forbidden("post_not_editable")
	case errors.Is(err, postgres.ErrPostSlotClosed), errors.Is(err, postgres.ErrNoCurrentWindow), errors.Is(err, postgres.ErrConflict):
		return Conflict("post_slot_unavailable")
	case errors.Is(err, service.ErrInvalidPost), errors.Is(err, postgres.ErrInvalid):
		return BadRequest("invalid_post", err)
	case errors.Is(err, isurealimage.ErrPayloadTooLarge):
		return PayloadTooLarge()
	case errors.Is(err, isurealimage.ErrUnsupportedMedia):
		return UnsupportedMedia()
	case errors.Is(err, isurealimage.ErrSanitizerUnavailable):
		return ServiceUnavailable("image_sanitizer_unavailable", err)
	default:
		return err
	}
}

func postResult(err error) string {
	if appError, ok := postHTTPError(err).(*AppError); ok {
		return strconv.Itoa(appError.Status) + "_" + appError.Code
	}
	return "unexpected_error"
}

func (h *PostHandler) log(c *echo.Context, operation, result string, userID, postID uuid.UUID) {
	attributes := []any{"operation", operation, "result", result, "correlationId", CorrelationIDFrom(c), "userId", userID.String()}
	if postID != uuid.Nil {
		attributes = append(attributes, "postId", postID.String())
	}
	h.logger.Info(fmt.Sprintf("post %s", result), attributes...)
}
