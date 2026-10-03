package isureal

import (
	"database/sql"
	"errors"
	"net/http"
	"time"

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
		viewer, err := loadViewerContext(ctx, h.DB, GetSession(c).UserID)
		if errors.Is(err, sql.ErrNoRows) {
			return c.JSON(http.StatusOK, getFeedResponse{Items: []createPostResponse{}})
		}
		if err != nil {
			return err
		}
		following := map[string]bool{}
		if input.Feed == "following" {
			var followeeIDs []string
			if err := h.DB.SelectContext(ctx, &followeeIDs, `SELECT followee_id FROM follows WHERE follower_id = $1`, viewer.ID); err != nil {
				return err
			}
			for _, id := range followeeIDs {
				following[id] = true
			}
		}
		posts, err := selectPostsWithAuthor(ctx, h.DB, `
   AND p.author_id <> $1 AND w.ended_at IS NULL
   AND ($2::timestamptz IS NULL OR (p.posted_at, p.id) < ($2, $3))
   ORDER BY p.posted_at DESC, p.id DESC
  `, viewer.ID, input.BeforePostedAt, input.BeforeID)
		if err != nil {
			return err
		}
		items := make([]createPostResponse, 0)
		for _, post := range posts {
			if !viewer.canView(post.Post) || !matchesFeed(viewer, following, post.Post, input.Feed) {
				continue
			}
			items = append(items, newPostResponse(post.Post, post.Author))
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

func matchesFeed(viewer viewerContext, following map[string]bool, post Post, feed string) bool {
	switch feed {
	case "public":
		return post.Visibility == "public"
	case "following":
		return following[post.AuthorID]
	case "friends":
		return viewer.Friends[post.AuthorID]
	case "friends-of-friends":
		return !viewer.Friends[post.AuthorID] && viewer.FriendsOfFriends[post.AuthorID]
	default:
		return false
	}
}
