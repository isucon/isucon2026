package isureal

import (
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
)

type createCommentRequest struct {
	PostID string `param:"postId" json:"-"`
	Body   string `json:"body"`
}

type createCommentResponse struct {
	ID        string                   `json:"id"`
	Author    relationshipUserResponse `json:"author"`
	Body      string                   `json:"body"`
	CreatedAt time.Time                `json:"createdAt"`
}

func newCommentResponse(comment Comment, user User) createCommentResponse {
	return createCommentResponse{ID: comment.ID, Author: newRelationshipUserResponse(user), Body: comment.Body, CreatedAt: comment.CreatedAt}
}

func createCommentHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input createCommentRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		if !validUserText(input.Body, 1, 1000) {
			return NewAPIError(http.StatusBadRequest, "invalid_interaction")
		}
		ctx := c.Request().Context()
		actor := GetSession(c).UserID
		post, err := getPost(ctx, h.DB, input.PostID)
		if errors.Is(err, sql.ErrNoRows) {
			return NewAPIError(http.StatusForbidden, "interaction_forbidden")
		}
		if err != nil {
			return err
		}
		tx, err := h.DB.BeginTxx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		for _, id := range slices.Compact([]string{min(actor, post.AuthorID), max(actor, post.AuthorID)}) {
			var lockedID string
			if err := tx.GetContext(ctx, &lockedID, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, id); err != nil {
				return err
			}
		}
		allowed, err := canComment(ctx, tx, actor, post.ID)
		if err != nil {
			return err
		}
		if !allowed {
			return NewAPIError(http.StatusForbidden, "interaction_forbidden")
		}
		id := uuid.NewString()
		if _, err := tx.ExecContext(ctx, `INSERT INTO comments(id,post_id,author_id,body,created_at) VALUES($1,$2,$3,$4,clock_timestamp())`, id, post.ID, actor, input.Body); err != nil {
			return err
		}
		comment, err := getComment(ctx, tx, id)
		if err != nil {
			return err
		}
		user, err := getUser(ctx, tx, actor)
		if err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		return c.JSON(http.StatusCreated, newCommentResponse(comment, user))
	}
}

type getCommentsRequest struct {
	PostID         string     `param:"postId"`
	AfterCreatedAt *time.Time `query:"afterCreatedAt"`
	AfterID        string     `query:"afterId"`
}

type getCommentsResponse struct {
	Items   []createCommentResponse `json:"items"`
	HasMore bool                    `json:"hasMore"`
}

func getCommentsHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input getCommentsRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		if (input.AfterCreatedAt == nil) != (input.AfterID == "") {
			return NewAPIError(http.StatusBadRequest, "invalid_pagination")
		}
		ctx := c.Request().Context()
		allowed, err := canComment(ctx, h.DB, GetSession(c).UserID, input.PostID)
		if err != nil {
			return err
		}
		if !allowed {
			return NewAPIError(http.StatusForbidden, "interaction_forbidden")
		}
		var ids []string
		if err := h.DB.SelectContext(ctx, &ids, `SELECT id FROM comments WHERE post_id=$1 AND deleted_at IS NULL AND ($2::timestamptz IS NULL OR (created_at,id)>($2,$3)) ORDER BY created_at,id`, input.PostID, input.AfterCreatedAt, input.AfterID); err != nil {
			return err
		}
		items := make([]createCommentResponse, 0)
		for _, id := range ids {
			comment, err := getComment(ctx, h.DB, id)
			if err != nil {
				return err
			}
			if comment.DeletedAt != nil {
				continue
			}
			user, err := getUser(ctx, h.DB, comment.AuthorID)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			items = append(items, newCommentResponse(comment, user))
			if len(items) == 51 {
				break
			}
		}
		hasMore := len(items) > 50
		if hasMore {
			items = items[:50]
		}
		return c.JSON(http.StatusOK, getCommentsResponse{Items: items, HasMore: hasMore})
	}
}

type deleteCommentRequest struct {
	CommentID string `param:"commentId" json:"-"`
}

func deleteCommentHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input deleteCommentRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		ctx := c.Request().Context()
		actor := GetSession(c).UserID
		tx, err := h.DB.BeginTxx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		var lockedID string
		if err := tx.GetContext(ctx, &lockedID, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, actor); err != nil {
			return err
		}
		var record struct {
			AuthorID     string `db:"author_id"`
			PostAuthorID string `db:"post_author_id"`
		}
		err = tx.GetContext(ctx, &record, `SELECT c.author_id,p.author_id AS post_author_id FROM comments c JOIN posts p ON p.id=c.post_id WHERE c.id=$1 AND c.deleted_at IS NULL FOR UPDATE OF c`, input.CommentID)
		if errors.Is(err, sql.ErrNoRows) {
			return NewAPIError(http.StatusNotFound, "interaction_not_found")
		}
		if err != nil {
			return err
		}
		if actor != record.AuthorID && actor != record.PostAuthorID {
			return NewAPIError(http.StatusForbidden, "interaction_forbidden")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE comments SET deleted_at=clock_timestamp() WHERE id=$1`, input.CommentID); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		return c.NoContent(http.StatusNoContent)
	}
}
