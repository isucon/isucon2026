package isureal

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
)

type getMemoriesRequest struct {
	Month          string     `query:"month"`
	BeforePostedAt *time.Time `query:"beforePostedAt"`
	BeforeID       string     `query:"beforeId"`
}

type memoryPostResponse struct {
	createPostResponse
	RealMojis []replaceRealMojiResponse `json:"realmojis"`
	Comments  []createCommentResponse   `json:"comments"`
}

type getMemoriesResponse struct {
	Items   []memoryPostResponse `json:"items"`
	HasMore bool                 `json:"hasMore"`
}

type getMemoriesByDateRequest struct {
	Date           string     `param:"date"`
	BeforePostedAt *time.Time `query:"beforePostedAt"`
	BeforeID       string     `query:"beforeId"`
}

type getMemoriesByDateResponse getMemoriesResponse

func getMemoriesHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input getMemoriesRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		start, err := time.Parse("2006-01", input.Month)
		if err != nil {
			return NewAPIError(http.StatusBadRequest, "invalid_memory_period")
		}
		response, err := listMemories(c.Request().Context(), h.DB, GetSession(c).UserID, start, start.AddDate(0, 1, 0), input.BeforePostedAt, input.BeforeID)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusOK, response)
	}
}

func getMemoriesByDateHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input getMemoriesByDateRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		start, err := time.Parse(time.DateOnly, input.Date)
		if err != nil {
			return NewAPIError(http.StatusBadRequest, "invalid_memory_period")
		}
		response, err := listMemories(c.Request().Context(), h.DB, GetSession(c).UserID, start, start.AddDate(0, 0, 1), input.BeforePostedAt, input.BeforeID)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusOK, getMemoriesByDateResponse(response))
	}
}

func listMemories(ctx context.Context, db sqlx.QueryerContext, userID string, start, end time.Time, beforeAt *time.Time, beforeID string) (getMemoriesResponse, error) {
	var response getMemoriesResponse
	if (beforeAt == nil) != (beforeID == "") {
		return response, NewAPIError(http.StatusBadRequest, "invalid_pagination")
	}
	var postIDs []string
	if err := sqlx.SelectContext(ctx, db, &postIDs, `
  SELECT p.id FROM posts p JOIN posting_windows w ON w.id = p.posting_window_id
  WHERE p.author_id = $1 AND p.deleted_at IS NULL
    AND w.local_date >= $2 AND w.local_date < $3
    AND ($4::timestamptz IS NULL OR (p.posted_at, p.id) < ($4, $5))
  ORDER BY p.posted_at DESC, p.id DESC
 `, userID, start, end, beforeAt, beforeID); err != nil {
		return response, err
	}
	items := make([]memoryPostResponse, 0)
	for _, postID := range postIDs {
		post, err := getPost(ctx, db, postID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return response, err
		}
		author, err := getUser(ctx, db, post.AuthorID)
		if err != nil {
			return response, err
		}
		items = append(items, memoryPostResponse{createPostResponse: newPostResponse(post, author)})
		if len(items) == 51 {
			break
		}
	}
	response.HasMore = len(items) > 50
	if response.HasMore {
		items = items[:50]
	}
	for i := range items {
		if err := loadMemoryInteractions(ctx, db, &items[i]); err != nil {
			return response, err
		}
	}
	response.Items = items
	return response, nil
}

func loadMemoryInteractions(ctx context.Context, db sqlx.QueryerContext, item *memoryPostResponse) error {
	item.RealMojis = make([]replaceRealMojiResponse, 0)
	item.Comments = make([]createCommentResponse, 0)
	var reactionIDs []string
	if err := sqlx.SelectContext(ctx, db, &reactionIDs, `SELECT id FROM realmojis WHERE post_id=$1 AND replaced_at IS NULL AND deleted_at IS NULL ORDER BY created_at DESC,id DESC`, item.ID); err != nil {
		return err
	}
	for _, id := range reactionIDs {
		reaction, err := getRealMoji(ctx, db, id)
		if err != nil {
			return err
		}
		if reaction.ReplacedAt != nil || reaction.DeletedAt != nil {
			continue
		}
		user, err := getUser(ctx, db, reaction.ReactorID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		item.RealMojis = append(item.RealMojis, newRealMojiResponse(reaction, user))
		if len(item.RealMojis) == 50 {
			break
		}
	}
	var commentIDs []string
	if err := sqlx.SelectContext(ctx, db, &commentIDs, `SELECT id FROM comments WHERE post_id=$1 AND deleted_at IS NULL ORDER BY created_at,id`, item.ID); err != nil {
		return err
	}
	for _, id := range commentIDs {
		comment, err := getComment(ctx, db, id)
		if err != nil {
			return err
		}
		if comment.DeletedAt != nil {
			continue
		}
		user, err := getUser(ctx, db, comment.AuthorID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		item.Comments = append(item.Comments, newCommentResponse(comment, user))
		if len(item.Comments) == 50 {
			break
		}
	}
	return nil
}
