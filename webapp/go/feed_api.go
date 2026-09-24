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

type getFeedRequest struct {
	Feed           string     `param:"feed"`
	BeforePostedAt *time.Time `query:"beforePostedAt"`
	BeforeID       string     `query:"beforeId"`
}

type getFeedResponse struct {
	Items   []createPostResponse `json:"items"`
	HasMore bool                 `json:"hasMore"`
}

func getFeedHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input getFeedRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		switch input.Feed {
		case "friends", "friends-of-friends", "public", "following":
		default:
			return NewAPIError(http.StatusBadRequest, "invalid_post")
		}
		if (input.BeforePostedAt == nil) != (input.BeforeID == "") {
			return NewAPIError(http.StatusBadRequest, "invalid_pagination")
		}
		ctx := c.Request().Context()
		viewerID := GetSession(c).UserID
		var postIDs []string
		if err := h.DB.SelectContext(ctx, &postIDs, `
   SELECT id FROM posts
   WHERE deleted_at IS NULL AND author_id <> $1
     AND ($2::timestamptz IS NULL OR (posted_at, id) < ($2, $3))
   ORDER BY posted_at DESC, id DESC
  `, viewerID, input.BeforePostedAt, input.BeforeID); err != nil {
			return err
		}
		items := make([]createPostResponse, 0)
		for _, postID := range postIDs {
			allowed, err := canViewPost(ctx, h.DB, viewerID, postID)
			if err != nil {
				return err
			}
			if !allowed {
				continue
			}
			post, err := getPost(ctx, h.DB, postID)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			matches, err := matchesFeed(ctx, h.DB, viewerID, post, input.Feed)
			if err != nil {
				return err
			}
			if !matches {
				continue
			}
			author, err := getUser(ctx, h.DB, post.AuthorID)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			items = append(items, newPostResponse(post, author))
			if len(items) == 51 {
				break
			}
		}
		hasMore := len(items) > 50
		if hasMore {
			items = items[:50]
		}
		return c.JSON(http.StatusOK, getFeedResponse{Items: items, HasMore: hasMore})
	}
}

func matchesFeed(ctx context.Context, db sqlx.QueryerContext, viewerID string, post Post, feed string) (bool, error) {
	switch feed {
	case "public":
		return post.Visibility == "public", nil
	case "following":
		var followeeIDs []string
		if err := sqlx.SelectContext(ctx, db, &followeeIDs, `SELECT followee_id FROM follows WHERE follower_id = $1`, viewerID); err != nil {
			return false, err
		}
		for _, id := range followeeIDs {
			if id == post.AuthorID {
				return true, nil
			}
		}
		return false, nil
	case "friends", "friends-of-friends":
		friends, err := getFriendIDs(ctx, db, viewerID)
		if err != nil {
			return false, err
		}
		for _, id := range friends {
			if id == post.AuthorID {
				return feed == "friends", nil
			}
		}
		if feed == "friends" {
			return false, nil
		}
		authorFriends, err := getFriendIDs(ctx, db, post.AuthorID)
		if err != nil {
			return false, err
		}
		for _, friendID := range friends {
			for _, authorFriendID := range authorFriends {
				if friendID == authorFriendID {
					return true, nil
				}
			}
		}
	}
	return false, nil
}
