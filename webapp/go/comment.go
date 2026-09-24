package isureal

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"time"

	"github.com/jmoiron/sqlx"
)

type Comment struct {
	ID        string     `db:"id"`
	PostID    string     `db:"post_id"`
	AuthorID  string     `db:"author_id"`
	Body      string     `db:"body"`
	CreatedAt time.Time  `db:"created_at"`
	DeletedAt *time.Time `db:"deleted_at"`
}

func getComment(ctx context.Context, db sqlx.QueryerContext, id string) (Comment, error) {
	var comment Comment
	err := sqlx.GetContext(ctx, db, &comment, `SELECT id,post_id,author_id,body,created_at,deleted_at FROM comments WHERE id=$1`, id)
	return comment, err
}

func canComment(ctx context.Context, db sqlx.QueryerContext, viewerID, postID string) (bool, error) {
	allowed, err := canViewPost(ctx, db, viewerID, postID)
	if err != nil || !allowed {
		return false, err
	}
	post, err := getPost(ctx, db, postID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	window, err := getPostingWindow(ctx, db, post.PostingWindowID)
	if err != nil {
		return false, err
	}
	if window.EndedAt != nil {
		return false, nil
	}
	if viewerID == post.AuthorID {
		viewer, err := getUser(ctx, db, viewerID)
		if err != nil {
			return false, err
		}
		var currentWindowID string
		err = sqlx.GetContext(ctx, db, &currentWindowID, `SELECT id FROM posting_windows WHERE region=$1 AND ended_at IS NULL`, viewer.Region)
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
		if err := sqlx.SelectContext(ctx, db, &ownWindowIDs, `SELECT posting_window_id FROM posts WHERE author_id=$1 AND deleted_at IS NULL`, viewerID); err != nil {
			return false, err
		}
		for _, id := range ownWindowIDs {
			ownWindow, err := getPostingWindow(ctx, db, id)
			if err != nil {
				return false, err
			}
			if ownWindow.LocalDate.Equal(currentWindow.LocalDate) {
				return true, nil
			}
		}
		return false, nil
	}
	if post.Visibility == "public" {
		return true, nil
	}
	friends, err := getFriendIDs(ctx, db, viewerID)
	if err != nil {
		return false, err
	}
	return slices.Contains(friends, post.AuthorID), nil
}
