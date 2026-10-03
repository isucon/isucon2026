package isureal

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"
)

type PostLocation struct {
	Kind      string   `json:"kind" db:"location_kind"`
	Latitude  *float64 `json:"latitude,omitempty" db:"latitude"`
	Longitude *float64 `json:"longitude,omitempty" db:"longitude"`
	Locality  *string  `json:"locality,omitempty" db:"locality"`
}

func (location PostLocation) valid() bool {
	switch location.Kind {
	case "none":
		return location.Latitude == nil && location.Longitude == nil && location.Locality == nil
	case "exact":
		return location.Latitude != nil && location.Longitude != nil && location.Locality == nil &&
			*location.Latitude >= -90 && *location.Latitude <= 90 && *location.Longitude >= -180 && *location.Longitude <= 180
	case "approximate":
		return location.Latitude == nil && location.Longitude == nil && location.Locality != nil && validUserText(*location.Locality, 1, 100)
	default:
		return false
	}
}

type Post struct {
	ID              string `db:"id"`
	AuthorID        string `db:"author_id"`
	PostingWindowID string `db:"posting_window_id"`
	ImageID         string `db:"image_id"`
	Caption         string `db:"caption"`
	Visibility      string `db:"visibility"`
	PostLocation
	PostedAt       time.Time  `db:"posted_at"`
	OnTimeDeadline time.Time  `db:"on_time_deadline"`
	WindowEndedAt  *time.Time `db:"window_ended_at"`
}

const postColumns = `p.id, p.author_id, p.posting_window_id, p.image_id, p.caption, p.visibility,
  p.location_kind, p.latitude, p.longitude, p.locality, p.posted_at,
  w.on_time_deadline, w.ended_at AS window_ended_at`

func getPost(ctx context.Context, db sqlx.QueryerContext, postID string) (Post, error) {
	var post Post
	err := sqlx.GetContext(ctx, db, &post, `
  SELECT `+postColumns+`
  FROM posts p JOIN posting_windows w ON w.id = p.posting_window_id
  WHERE p.id = $1 AND p.deleted_at IS NULL
 `, postID)
	return post, err
}

type PostWithAuthor struct {
	Post
	Author User `db:"author"`
}

func selectPostsWithAuthor(ctx context.Context, db sqlx.QueryerContext, conditions string, args ...any) ([]PostWithAuthor, error) {
	return selectPostsWithAuthorCTE(ctx, db, "", conditions, args...)
}

func selectPostsWithAuthorCTE(ctx context.Context, db sqlx.QueryerContext, cte, conditions string, args ...any) ([]PostWithAuthor, error) {
	var posts []PostWithAuthor
	if err := sqlx.SelectContext(ctx, db, &posts, cte+`
  SELECT `+postColumns+`, `+userColumnsAs("u", "author")+`
  FROM posts p
  JOIN posting_windows w ON w.id = p.posting_window_id
  JOIN users u ON u.id = p.author_id AND u.status = 'active'
  WHERE p.deleted_at IS NULL `+conditions, args...); err != nil {
		return nil, err
	}
	for i := range posts {
		if err := posts[i].Author.setImageKey(); err != nil {
			return nil, err
		}
	}
	return posts, nil
}

func getPostWithAuthor(ctx context.Context, db sqlx.QueryerContext, postID string) (PostWithAuthor, error) {
	posts, err := selectPostsWithAuthor(ctx, db, `AND p.id = $1`, postID)
	if err != nil {
		return PostWithAuthor{}, err
	}
	if len(posts) == 0 {
		return PostWithAuthor{}, sql.ErrNoRows
	}
	return posts[0], nil
}

type viewerContext struct {
	User
	HasPostedToday   bool
	Friends          map[string]bool
	FriendsOfFriends map[string]bool
	Blocked          map[string]bool
}

// Both friendship directions have indexes. Keep the statement shape constant even
// for users with many friends, and do not cache mutable authorization state.
const viewerRelationsCTE = `WITH viewer AS (
  SELECT id, region FROM users WHERE id = $1 AND status = 'active'
), friends AS (
  SELECT user_high_id AS id FROM friendships WHERE user_low_id = $1
  UNION
  SELECT user_low_id AS id FROM friendships WHERE user_high_id = $1
), friends_of_friends AS (
  SELECT f.user_high_id AS id FROM friends one JOIN friendships f ON f.user_low_id = one.id
  UNION
  SELECT f.user_low_id AS id FROM friends one JOIN friendships f ON f.user_high_id = one.id
)
`

func (viewer viewerContext) canView(post Post) bool {
	if post.AuthorID == viewer.ID {
		return true
	}
	if post.WindowEndedAt != nil || !viewer.HasPostedToday || viewer.Blocked[post.AuthorID] {
		return false
	}
	switch post.Visibility {
	case "public":
		return true
	case "friends":
		return viewer.Friends[post.AuthorID]
	case "friends_of_friends":
		return viewer.Friends[post.AuthorID] || viewer.FriendsOfFriends[post.AuthorID]
	default:
		return false
	}
}

func canViewPost(ctx context.Context, db sqlx.QueryerContext, viewerID, postID string) (bool, error) {
	post, viewer, err := loadPostAccess(ctx, db, viewerID, postID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return viewer.canView(post.Post), nil
}

func getFriendIDs(ctx context.Context, db sqlx.QueryerContext, userID string) ([]string, error) {
	var friendships []struct {
		UserLowID  string `db:"user_low_id"`
		UserHighID string `db:"user_high_id"`
	}
	if err := sqlx.SelectContext(ctx, db, &friendships, `SELECT user_low_id, user_high_id FROM friendships WHERE user_low_id = $1 OR user_high_id = $1`, userID); err != nil {
		return nil, err
	}
	var ids []string
	for _, friendship := range friendships {
		if friendship.UserLowID == userID {
			ids = append(ids, friendship.UserHighID)
		} else {
			ids = append(ids, friendship.UserLowID)
		}
	}
	return ids, nil
}

func getBlockedUserIDs(ctx context.Context, db sqlx.QueryerContext, userID string) (map[string]bool, error) {
	var ids []string
	if err := sqlx.SelectContext(ctx, db, &ids, `
  SELECT CASE WHEN blocker_id = $1 THEN blocked_id ELSE blocker_id END
  FROM block_relations WHERE blocker_id = $1 OR blocked_id = $1
 `, userID); err != nil {
		return nil, err
	}
	blocked := make(map[string]bool, len(ids))
	for _, id := range ids {
		blocked[id] = true
	}
	return blocked, nil
}

// Return the post, its author and fresh authorization state in one round trip.
// When db is a transaction, the caller's user locks and snapshot rules remain in force.
func loadPostAccess(ctx context.Context, db sqlx.QueryerContext, viewerID, postID string) (PostWithAuthor, viewerContext, error) {
	var state struct {
		PostWithAuthor
		Viewer         User `db:"viewer"`
		HasPosted      bool `db:"has_posted"`
		Friend         bool `db:"friend"`
		FriendOfFriend bool `db:"friend_of_friend"`
		IsBlocked      bool `db:"is_blocked"`
	}
	err := sqlx.GetContext(ctx, db, &state, `
  WITH neighbors AS (
    SELECT user_high_id AS id FROM friendships WHERE user_low_id=$1
    UNION ALL SELECT user_low_id FROM friendships WHERE user_high_id=$1
  )
  SELECT `+postColumns+`, `+authorProjection+`, `+viewerProjection+`,
    EXISTS (
      SELECT 1 FROM posts own
      JOIN posting_windows posted ON posted.id=own.posting_window_id
      JOIN posting_windows current ON current.region=v.region AND current.ended_at IS NULL
        AND current.local_date=posted.local_date
      WHERE own.author_id=v.id AND own.deleted_at IS NULL
    ) AS has_posted,
    EXISTS(SELECT 1 FROM neighbors WHERE id=p.author_id) AS friend,
    EXISTS(SELECT 1 FROM neighbors n JOIN friendships f
      ON (f.user_low_id=p.author_id AND f.user_high_id=n.id)
        OR (f.user_high_id=p.author_id AND f.user_low_id=n.id)) AS friend_of_friend,
    (EXISTS(SELECT 1 FROM block_relations WHERE blocker_id=$1 AND blocked_id=p.author_id)
     OR EXISTS(SELECT 1 FROM block_relations WHERE blocked_id=$1 AND blocker_id=p.author_id)) AS is_blocked
  FROM posts p JOIN posting_windows w ON w.id=p.posting_window_id
  JOIN users u ON u.id=p.author_id AND u.status='active'
  JOIN users v ON v.id=$1 AND v.status='active'
  WHERE p.id=$2 AND p.deleted_at IS NULL
 `, viewerID, postID)
	if err != nil {
		return PostWithAuthor{}, viewerContext{}, err
	}
	if err := state.Author.setImageKey(); err != nil {
		return PostWithAuthor{}, viewerContext{}, err
	}
	if err := state.Viewer.setImageKey(); err != nil {
		return PostWithAuthor{}, viewerContext{}, err
	}
	viewer := viewerContext{
		User: state.Viewer, HasPostedToday: state.HasPosted,
		Friends:          map[string]bool{state.AuthorID: state.Friend},
		FriendsOfFriends: map[string]bool{state.AuthorID: state.FriendOfFriend},
		Blocked:          map[string]bool{state.AuthorID: state.IsBlocked},
	}
	return state.PostWithAuthor, viewer, nil
}
