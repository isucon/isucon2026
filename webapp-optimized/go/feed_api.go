package isureal

import (
	"cmp"
	"context"
	"net/http"
	"slices"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
)

type getFeedRequest struct {
	Feed           string     `param:"feed"`
	BeforeScore    *int64     `query:"beforeScore"`
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
		ranked := input.Feed == "friends-of-friends"
		if ranked && (input.BeforeScore == nil) != (input.BeforeID == "") {
			return NewAPIError(http.StatusBadRequest, "invalid_pagination")
		}
		var before *feedCursor
		if input.BeforePostedAt != nil {
			before = &feedCursor{PostedAt: *input.BeforePostedAt, ID: input.BeforeID}
			if ranked {
				before.Score = *input.BeforeScore
			}
		}
		ctx := c.Request().Context()
		if items, hasMore, ok := cachedFeed(c, h, input.Feed, before); ok {
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
		if before != nil && !ranked {
			conditions += ` AND (p.posted_at, p.id) < ($2, $3)`
			args = append(args, before.PostedAt, before.ID)
		}
		if !ranked {
			conditions += ` ORDER BY p.posted_at DESC, p.id DESC LIMIT 51`
		}
		posts, err := selectPostsWithAuthorCTE(ctx, h.DB, viewerRelationsCTE, conditions, args...)
		if err != nil {
			return err
		}
		var scores []int64
		if ranked {
			if posts, scores, err = recommendFromDB(ctx, h.DB, GetSession(c).UserID, posts, before); err != nil {
				return err
			}
		}
		items, hasMore := feedItems(posts, scores)
		return c.JSON(http.StatusOK, getFeedResponse{Items: items, HasMore: hasMore})
	}
}

// feedItems builds a page of at most 50 items from up to 51 posts, with the
// scores of a friends-of-friends page.
func feedItems(posts []PostWithAuthor, scores []int64) ([]createPostResponse, bool) {
	items := make([]createPostResponse, 0, len(posts))
	for i, post := range posts {
		item := newPostResponse(post.Post, post.Author)
		if scores != nil {
			item.Score = &scores[i]
		}
		items = append(items, item)
	}
	hasMore := len(items) > 50
	if hasMore {
		items = items[:50]
	}
	return items, hasMore
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
func cachedFeed(c *echo.Context, h *HandlerContext, feed string, before *feedCursor) ([]createPostResponse, bool, bool) {
	user, ok := hotSessionUser(c, h)
	if !ok {
		return nil, false, false
	}
	posts, scores, ok := hot.feed(user.ID, feed, before)
	if !ok {
		return nil, false, false
	}
	items, hasMore := feedItems(posts, scores)
	return items, hasMore, true
}

// recommendFromDB ranks the friends-of-friends candidates like
// hotState.recommend, reading the relationships and interactions from the
// database.
func recommendFromDB(ctx context.Context, db sqlx.QueryerContext, viewerID string, candidates []PostWithAuthor, before *feedCursor) ([]PostWithAuthor, []int64, error) {
	if len(candidates) == 0 {
		return candidates, nil, nil
	}
	var paths []struct {
		Friend string `db:"friend"`
		Other  string `db:"other"`
	}
	if err := sqlx.SelectContext(ctx, db, &paths, `WITH friends AS (
    SELECT user_high_id AS id FROM friendships WHERE user_low_id = $1
    UNION
    SELECT user_low_id AS id FROM friendships WHERE user_high_id = $1
  )
  SELECT one.id AS friend, f.user_high_id AS other FROM friends one JOIN friendships f ON f.user_low_id = one.id
  UNION ALL
  SELECT one.id AS friend, f.user_low_id AS other FROM friends one JOIN friendships f ON f.user_high_id = one.id`, viewerID); err != nil {
		return nil, nil, err
	}
	friends := map[string]bool{}
	mutual := map[string]int{}
	for _, path := range paths {
		friends[path.Friend] = true
		if path.Other != viewerID {
			mutual[path.Other]++
		}
	}
	authorIDs := make([]string, 0, len(candidates))
	postIDs := make([]string, 0, len(candidates))
	for _, post := range candidates {
		authorIDs = append(authorIDs, post.AuthorID)
		postIDs = append(postIDs, post.ID)
	}
	var friendships []struct {
		Low  string `db:"user_low_id"`
		High string `db:"user_high_id"`
	}
	if err := sqlx.SelectContext(ctx, db, &friendships, `SELECT user_low_id, user_high_id FROM friendships
  WHERE user_low_id = ANY($1) OR user_high_id = ANY($1)`, authorIDs); err != nil {
		return nil, nil, err
	}
	connections := map[string]int64{}
	for _, f := range friendships {
		connections[f.Low] += int64(mutual[f.High])
		connections[f.High] += int64(mutual[f.Low])
	}
	var engagements []struct {
		PostID string `db:"post_id"`
		UserID string `db:"user_id"`
	}
	if err := sqlx.SelectContext(ctx, db, &engagements, `
  SELECT r.post_id, r.reactor_id AS user_id FROM realmojis r
  JOIN users u ON u.id = r.reactor_id AND u.status = 'active'
  WHERE r.post_id = ANY($1) AND r.replaced_at IS NULL AND r.deleted_at IS NULL
  UNION
  SELECT c.post_id, c.author_id AS user_id FROM comments c
  JOIN users u ON u.id = c.author_id AND u.status = 'active'
  WHERE c.post_id = ANY($1) AND c.deleted_at IS NULL`, postIDs); err != nil {
		return nil, nil, err
	}
	engaged := map[string]int64{}
	for _, e := range engagements {
		if friends[e.UserID] {
			engaged[e.PostID]++
		}
	}
	type item struct {
		score int64
		post  PostWithAuthor
	}
	items := make([]item, 0, len(candidates))
	for _, post := range candidates {
		score := 10*engaged[post.ID] + 3*int64(mutual[post.AuthorID]) + min(connections[post.AuthorID], 30)
		if before != nil && !rankedAfter(score, post.PostedAt, post.ID, before) {
			continue
		}
		items = append(items, item{score: score, post: post})
	}
	slices.SortFunc(items, func(a, b item) int {
		return cmp.Or(cmp.Compare(b.score, a.score), b.post.PostedAt.Compare(a.post.PostedAt), cmp.Compare(b.post.ID, a.post.ID))
	})
	items = items[:min(len(items), 51)]
	posts := make([]PostWithAuthor, len(items))
	scores := make([]int64, len(items))
	for i, it := range items {
		posts[i], scores[i] = it.post, it.score
	}
	return posts, scores, nil
}
