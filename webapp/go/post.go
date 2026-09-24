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
	PostedAt       time.Time `db:"posted_at"`
	OnTimeDeadline time.Time `db:"on_time_deadline"`
}

func getPost(ctx context.Context, db sqlx.QueryerContext, postID string) (Post, error) {
	var post Post
	err := sqlx.GetContext(ctx, db, &post, `
  SELECT p.id, p.author_id, p.posting_window_id, p.image_id, p.caption, p.visibility,
         p.location_kind, p.latitude, p.longitude, p.locality, p.posted_at, w.on_time_deadline
  FROM posts p JOIN posting_windows w ON w.id = p.posting_window_id
  WHERE p.id = $1 AND p.deleted_at IS NULL
 `, postID)
	return post, err
}

func canViewPost(ctx context.Context, db sqlx.QueryerContext, viewerID, postID string) (bool, error) {
	post, err := getPost(ctx, db, postID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	author, err := getUser(ctx, db, post.AuthorID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	viewer, err := getUser(ctx, db, viewerID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if viewer.ID == author.ID {
		return true, nil
	}

	window, err := getPostingWindow(ctx, db, post.PostingWindowID)
	if err != nil {
		return false, err
	}
	if window.EndedAt != nil {
		return false, nil
	}
	var currentWindowID string
	err = sqlx.GetContext(ctx, db, &currentWindowID, `SELECT id FROM posting_windows WHERE region = $1 AND ended_at IS NULL`, viewer.Region)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	currentWindow, err := getPostingWindow(ctx, db, currentWindowID)
	if err != nil {
		return false, err
	}
	var ownWindowIDs []string
	if err := sqlx.SelectContext(ctx, db, &ownWindowIDs, `SELECT posting_window_id FROM posts WHERE author_id = $1 AND deleted_at IS NULL`, viewer.ID); err != nil {
		return false, err
	}
	hasPosted := false
	for _, windowID := range ownWindowIDs {
		ownWindow, err := getPostingWindow(ctx, db, windowID)
		if err != nil {
			return false, err
		}
		if ownWindow.LocalDate.Equal(currentWindow.LocalDate) {
			hasPosted = true
			break
		}
	}
	if !hasPosted {
		return false, nil
	}

	var blocks []struct {
		BlockerID string `db:"blocker_id"`
		BlockedID string `db:"blocked_id"`
	}
	if err := sqlx.SelectContext(ctx, db, &blocks, `SELECT blocker_id, blocked_id FROM block_relations WHERE blocker_id = $1 OR blocked_id = $1`, viewer.ID); err != nil {
		return false, err
	}
	for _, block := range blocks {
		if block.BlockerID == author.ID || block.BlockedID == author.ID {
			return false, nil
		}
	}
	if post.Visibility == "public" {
		return true, nil
	}
	if post.Visibility != "friends" && post.Visibility != "friends_of_friends" {
		return false, nil
	}
	viewerFriends, err := getFriendIDs(ctx, db, viewer.ID)
	if err != nil {
		return false, err
	}
	for _, friendID := range viewerFriends {
		if friendID == author.ID {
			return true, nil
		}
	}
	if post.Visibility == "friends" {
		return false, nil
	}
	authorFriends, err := getFriendIDs(ctx, db, author.ID)
	if err != nil {
		return false, err
	}
	for _, viewerFriendID := range viewerFriends {
		for _, authorFriendID := range authorFriends {
			if viewerFriendID == authorFriendID {
				return true, nil
			}
		}
	}
	return false, nil
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
