package isureal

import (
	"context"
	"github.com/jmoiron/sqlx"
)

// Original webapp/go visibility implementation, retained as a compatibility oracle.
func referenceViewerContext(ctx context.Context, db sqlx.QueryerContext, viewerID string) (viewerContext, error) {
	viewer := viewerContext{Friends: map[string]bool{}, FriendsOfFriends: map[string]bool{}}
	var err error
	if viewer.User, err = getUser(ctx, db, viewerID); err != nil {
		return viewer, err
	}
	if err := sqlx.GetContext(ctx, db, &viewer.HasPostedToday, `
  SELECT EXISTS(
    SELECT 1 FROM posts p
    JOIN posting_windows posted ON posted.id = p.posting_window_id
    JOIN posting_windows current ON current.region = $2 AND current.ended_at IS NULL
                                AND current.local_date = posted.local_date
    WHERE p.author_id = $1 AND p.deleted_at IS NULL
  )
 `, viewer.ID, viewer.Region); err != nil {
		return viewer, err
	}
	if viewer.Blocked, err = getBlockedUserIDs(ctx, db, viewer.ID); err != nil {
		return viewer, err
	}
	friends, err := getFriendIDs(ctx, db, viewer.ID)
	if err != nil {
		return viewer, err
	}
	if len(friends) == 0 {
		return viewer, nil
	}
	for _, id := range friends {
		viewer.Friends[id] = true
	}
	query, args, err := sqlx.In(`SELECT user_low_id, user_high_id FROM friendships WHERE user_low_id IN (?) OR user_high_id IN (?)`, friends, friends)
	if err != nil {
		return viewer, err
	}
	var friendships []struct {
		UserLowID  string `db:"user_low_id"`
		UserHighID string `db:"user_high_id"`
	}
	if err := sqlx.SelectContext(ctx, db, &friendships, sqlx.Rebind(sqlx.DOLLAR, query), args...); err != nil {
		return viewer, err
	}
	for _, friendship := range friendships {
		if viewer.Friends[friendship.UserLowID] {
			viewer.FriendsOfFriends[friendship.UserHighID] = true
		}
		if viewer.Friends[friendship.UserHighID] {
			viewer.FriendsOfFriends[friendship.UserLowID] = true
		}
	}
	return viewer, nil
}

// Original webapp/go recommendation score of the friends-of-friends feed,
// retained as a compatibility oracle.
func referenceRecommendationScores(ctx context.Context, db sqlx.QueryerContext, viewerID string, posts []PostWithAuthor) (map[string]int64, error) {
	scores := make(map[string]int64, len(posts))
	if len(posts) == 0 {
		return scores, nil
	}
	friends := map[string]bool{}
	mutual := map[string]int{}
	ids, err := getFriendIDs(ctx, db, viewerID)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		friends[id] = true
	}
	type friendship struct {
		UserLowID  string `db:"user_low_id"`
		UserHighID string `db:"user_high_id"`
	}
	var friendships []friendship
	if len(ids) > 0 {
		query, args, err := sqlx.In(`SELECT user_low_id, user_high_id FROM friendships WHERE user_low_id IN (?) OR user_high_id IN (?)`, ids, ids)
		if err != nil {
			return nil, err
		}
		if err := sqlx.SelectContext(ctx, db, &friendships, sqlx.Rebind(sqlx.DOLLAR, query), args...); err != nil {
			return nil, err
		}
	}
	for _, f := range friendships {
		if friends[f.UserLowID] && f.UserHighID != viewerID {
			mutual[f.UserHighID]++
		}
		if friends[f.UserHighID] && f.UserLowID != viewerID {
			mutual[f.UserLowID]++
		}
	}
	authorIDs := make([]string, 0, len(posts))
	postIDs := make([]string, 0, len(posts))
	for _, post := range posts {
		authorIDs = append(authorIDs, post.AuthorID)
		postIDs = append(postIDs, post.ID)
	}
	query, args, err := sqlx.In(`SELECT user_low_id, user_high_id FROM friendships WHERE user_low_id IN (?) OR user_high_id IN (?)`, authorIDs, authorIDs)
	if err != nil {
		return nil, err
	}
	var authorFriendships []friendship
	if err := sqlx.SelectContext(ctx, db, &authorFriendships, sqlx.Rebind(sqlx.DOLLAR, query), args...); err != nil {
		return nil, err
	}
	connections := map[string]int64{}
	for _, f := range authorFriendships {
		connections[f.UserLowID] += int64(mutual[f.UserHighID])
		connections[f.UserHighID] += int64(mutual[f.UserLowID])
	}
	query, args, err = sqlx.In(`
  SELECT r.post_id, r.reactor_id AS user_id FROM realmojis r
  JOIN users u ON u.id = r.reactor_id AND u.status = 'active'
  WHERE r.post_id IN (?) AND r.replaced_at IS NULL AND r.deleted_at IS NULL
  UNION
  SELECT c.post_id, c.author_id AS user_id FROM comments c
  JOIN users u ON u.id = c.author_id AND u.status = 'active'
  WHERE c.post_id IN (?) AND c.deleted_at IS NULL
 `, postIDs, postIDs)
	if err != nil {
		return nil, err
	}
	var engagements []struct {
		PostID string `db:"post_id"`
		UserID string `db:"user_id"`
	}
	if err := sqlx.SelectContext(ctx, db, &engagements, sqlx.Rebind(sqlx.DOLLAR, query), args...); err != nil {
		return nil, err
	}
	friendEngagements := map[string]int64{}
	for _, engagement := range engagements {
		if friends[engagement.UserID] {
			friendEngagements[engagement.PostID]++
		}
	}
	for _, post := range posts {
		scores[post.ID] = 10*friendEngagements[post.ID] + 3*int64(mutual[post.AuthorID]) + min(connections[post.AuthorID], 30)
	}
	return scores, nil
}
