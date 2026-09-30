package isureal

import (
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
		if items, hasMore, ok := cachedFeed(c, h, input); ok {
			return c.JSON(http.StatusOK, getFeedResponse{Items: items, HasMore: hasMore})
		}
		// Apply visibility before LIMIT, so invisible posts never consume page slots.
		conditions := `
   AND p.author_id <> $1 AND w.ended_at IS NULL
   AND EXISTS (SELECT 1 FROM viewer v WHERE EXISTS (
     SELECT 1 FROM posts own
     JOIN posting_windows posted ON posted.id = own.posting_window_id
     JOIN posting_windows current ON current.region = v.region
       AND current.ended_at IS NULL AND current.local_date = posted.local_date
     WHERE own.author_id = v.id AND own.deleted_at IS NULL
   ))
   AND NOT EXISTS (SELECT 1 FROM block_relations b
     WHERE b.blocker_id = $1 AND b.blocked_id = p.author_id)
   AND NOT EXISTS (SELECT 1 FROM block_relations b
     WHERE b.blocked_id = $1 AND b.blocker_id = p.author_id)
  `
		switch input.Feed {
		case "public":
			conditions += ` AND p.visibility = 'public'`
		case "friends":
			conditions += ` AND p.author_id IN (SELECT id FROM friends)
              AND p.visibility IN ('public', 'friends', 'friends_of_friends')`
		case "friends-of-friends":
			conditions += ` AND p.author_id NOT IN (SELECT id FROM friends)
              AND p.author_id IN (SELECT id FROM friends_of_friends)
              AND p.visibility IN ('public', 'friends_of_friends')`
		case "following":
			conditions += ` AND EXISTS (SELECT 1 FROM follows f WHERE f.follower_id = $1 AND f.followee_id = p.author_id)
              AND (p.visibility = 'public'
                OR (p.visibility IN ('friends', 'friends_of_friends') AND p.author_id IN (SELECT id FROM friends))
                OR (p.visibility = 'friends_of_friends' AND p.author_id IN (SELECT id FROM friends_of_friends)))`
		}
		args := []any{GetSession(c).UserID}
		if input.BeforePostedAt != nil {
			conditions += ` AND (p.posted_at, p.id) < ($2, $3)`
			args = append(args, input.BeforePostedAt, input.BeforeID)
		}
		conditions += ` ORDER BY p.posted_at DESC, p.id DESC LIMIT 51`
		posts, err := selectPostsWithAuthorCTE(ctx, h.ReadDB, viewerRelationsCTE, conditions, args...)
		if err != nil {
			return err
		}
		items := make([]createPostResponse, 0, len(posts))
		for _, post := range posts {
			items = append(items, newPostResponse(post.Post, post.Author))
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

// cachedFeed answers from the regional hot state when it knows the session and
// the viewer, which is equivalent to the SQL below.
func cachedFeed(c *echo.Context, h *HandlerContext, input getFeedRequest) ([]createPostResponse, bool, bool) {
	state, _ := c.Request().Context().Value(batchSessionKey{}).(*batchSession)
	if state == nil || state.attempted {
		return nil, false, false
	}
	user, ok := hot.authenticate(state.session.TokenHash, h.Config.Region)
	if !ok {
		return nil, false, false
	}
	var before *feedCursor
	if input.BeforePostedAt != nil {
		before = &feedCursor{PostedAt: *input.BeforePostedAt, ID: input.BeforeID}
	}
	posts, ok := hot.feed(user.ID, input.Feed, before)
	if !ok {
		return nil, false, false
	}
	state.attempted = true
	state.session.UserID, state.session.Region = user.ID, user.Region
	items := make([]createPostResponse, 0, len(posts))
	for _, post := range posts {
		items = append(items, newPostResponse(post.Post, post.Author))
	}
	hasMore := len(items) > 50
	if hasMore {
		items = items[:50]
	}
	return items, hasMore, true
}
