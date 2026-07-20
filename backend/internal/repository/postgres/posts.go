package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/domain"
	isurealimage "github.com/isucon/isucon2026/backend/internal/image"
	"github.com/jackc/pgx/v5"
)

var (
	ErrNoCurrentWindow   = errors.New("no current posting window")
	ErrPostSlotClosed    = errors.New("post slot is unavailable")
	ErrPostNotEditable   = errors.New("post is not editable")
	ErrFeedBeforePosting = errors.New("viewer must post before opening feeds")
)

type PostRecord = domain.PostRecord
type FeedQuery = domain.FeedQuery

type PostRepository struct{ db *DB }

func NewPostRepository(db *DB) *PostRepository { return &PostRepository{db: db} }

func (r *PostRepository) CurrentWindow(ctx context.Context, userID uuid.UUID) (domain.PostingWindow, domain.PostSlot, error) {
	var window domain.PostingWindow
	var slot domain.PostSlot
	var activePostID *uuid.UUID
	var postedAt *time.Time
	err := Executor(ctx, r.db.Pool).QueryRow(ctx, `
SELECT w.id,w.region,w.local_date,w.utc_offset_minutes,w.starts_at,w.on_time_deadline,w.ended_at,
       s.active_post_id,COALESCE(s.deletion_count,0),s.posted_at
FROM users u JOIN posting_windows w ON w.region=u.region AND w.ended_at IS NULL
LEFT JOIN user_posting_window_states s ON s.user_id=u.id AND s.posting_window_id=w.id
WHERE u.id=$1 AND u.status='active'`, userID).
		Scan(&window.ID, &window.Region, &window.LocalDate, &window.UTCOffsetMinutes, &window.StartsAt, &window.OnTimeDeadline, &window.EndedAt, &activePostID, &slot.DeletionCount, &postedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return window, slot, ErrNoCurrentWindow
	}
	if err != nil {
		return window, slot, MapError(err)
	}
	slot.ActivePostID, slot.PostedAt = activePostID, postedAt
	return window, slot, nil
}

func (r *PostRepository) Create(ctx context.Context, authorID uuid.UUID, object isurealimage.Object, caption string, visibility domain.Visibility, location domain.Location, postedAt time.Time, postID uuid.UUID) (domain.Post, error) {
	var created domain.Post
	err := r.db.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := LockUsers(ctx, tx, authorID); err != nil {
			return err
		}
		var window domain.PostingWindow
		if err := tx.QueryRow(ctx, `SELECT w.id,w.region,w.local_date,w.utc_offset_minutes,w.starts_at,w.on_time_deadline,w.ended_at FROM users u JOIN posting_windows w ON w.region=u.region AND w.ended_at IS NULL WHERE u.id=$1 AND u.status='active' FOR UPDATE OF w`, authorID).
			Scan(&window.ID, &window.Region, &window.LocalDate, &window.UTCOffsetMinutes, &window.StartsAt, &window.OnTimeDeadline, &window.EndedAt); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNoCurrentWindow
			}
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO user_posting_window_states(user_id,posting_window_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, authorID, window.ID); err != nil {
			return err
		}
		var activeID *uuid.UUID
		var deletions int
		if err := tx.QueryRow(ctx, `SELECT active_post_id,deletion_count FROM user_posting_window_states WHERE user_id=$1 AND posting_window_id=$2 FOR UPDATE`, authorID, window.ID).Scan(&activeID, &deletions); err != nil {
			return err
		}
		if activeID != nil || deletions >= 2 {
			return ErrPostSlotClosed
		}
		onTime, lateSeconds, err := domain.ClassifyPost(window, postedAt)
		if err != nil {
			return ErrPostSlotClosed
		}
		if _, err := tx.Exec(ctx, `INSERT INTO image_objects(id,owner_user_id,sha256,object_key,byte_size,media_type,created_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, object.ID, authorID, object.SHA256, object.ObjectKey, object.ByteSize, object.MediaType, postedAt); err != nil {
			return err
		}
		var latitude, longitude any
		var locality any
		if location.Latitude != nil {
			latitude = *location.Latitude
		}
		if location.Longitude != nil {
			longitude = *location.Longitude
		}
		if location.Locality != nil {
			locality = *location.Locality
		}
		if _, err := tx.Exec(ctx, `INSERT INTO posts(id,author_id,posting_window_id,image_id,caption,visibility,location_kind,latitude,longitude,locality,posted_at,is_on_time,late_seconds) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
			postID, authorID, window.ID, object.ID, caption, visibility, location.Kind, latitude, longitude, locality, postedAt, onTime, lateSeconds); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE user_posting_window_states SET active_post_id=$3,posted_at=$4,updated_at=$4 WHERE user_id=$1 AND posting_window_id=$2`, authorID, window.ID, postID, postedAt); err != nil {
			return err
		}
		created = domain.Post{ID: postID, AuthorID: authorID, PostingWindowID: window.ID, ImageID: object.ID, Caption: caption, Visibility: visibility, Location: location, PostedAt: postedAt, OnTime: onTime, LateSeconds: lateSeconds}
		return nil
	})
	return created, err
}

func (r *PostRepository) Get(ctx context.Context, postID uuid.UUID) (PostRecord, error) {
	return scanPost(Executor(ctx, r.db.Pool).QueryRow(ctx, postSelect+` WHERE p.id=$1 AND p.deleted_at IS NULL`, postID))
}

func (r *PostRepository) ImagePost(ctx context.Context, imageID uuid.UUID) (uuid.UUID, string, error) {
	var postID uuid.UUID
	var objectKey string
	err := Executor(ctx, r.db.Pool).QueryRow(ctx, `
SELECT p.id,i.object_key FROM image_objects i JOIN posts p ON p.image_id=i.id
WHERE i.id=$1 AND p.deleted_at IS NULL`, imageID).Scan(&postID, &objectKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, "", ErrNotFound
	}
	return postID, objectKey, MapError(err)
}

func (r *PostRepository) CurrentSelf(ctx context.Context, userID uuid.UUID) (PostRecord, error) {
	return scanPost(Executor(ctx, r.db.Pool).QueryRow(ctx, postSelect+`
JOIN user_posting_window_states s ON s.active_post_id=p.id
JOIN posting_windows current ON current.id=s.posting_window_id AND current.ended_at IS NULL
WHERE p.author_id=$1 AND p.deleted_at IS NULL`, userID))
}

func (r *PostRepository) Update(ctx context.Context, actorID, postID uuid.UUID, caption *string, visibility *domain.Visibility) (PostRecord, error) {
	err := r.db.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := LockUsers(ctx, tx, actorID); err != nil {
			return err
		}
		var authorID uuid.UUID
		var endedAt *time.Time
		if err := tx.QueryRow(ctx, `SELECT p.author_id,w.ended_at FROM posts p JOIN posting_windows w ON w.id=p.posting_window_id WHERE p.id=$1 AND p.deleted_at IS NULL FOR UPDATE OF p`, postID).Scan(&authorID, &endedAt); err != nil {
			return err
		}
		if authorID != actorID || endedAt != nil {
			return ErrPostNotEditable
		}
		_, err := tx.Exec(ctx, `UPDATE posts SET caption=COALESCE($2,caption),visibility=COALESCE($3,visibility) WHERE id=$1`, postID, caption, visibility)
		return err
	})
	if err != nil {
		return PostRecord{}, MapError(err)
	}
	return r.Get(ctx, postID)
}

func (r *PostRepository) Delete(ctx context.Context, actorID, postID uuid.UUID, deletedAt time.Time) (string, error) {
	var objectKey string
	err := r.db.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := LockUsers(ctx, tx, actorID); err != nil {
			return err
		}
		var authorID, windowID uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT p.author_id,p.posting_window_id,i.object_key FROM posts p JOIN image_objects i ON i.id=p.image_id WHERE p.id=$1 AND p.deleted_at IS NULL FOR UPDATE OF p`, postID).Scan(&authorID, &windowID, &objectKey); err != nil {
			return err
		}
		if authorID != actorID {
			return ErrPostNotEditable
		}
		if _, err := tx.Exec(ctx, `UPDATE posts SET deleted_at=$2 WHERE id=$1`, postID, deletedAt); err != nil {
			return err
		}
		command, err := tx.Exec(ctx, `UPDATE user_posting_window_states SET active_post_id=NULL,deletion_count=deletion_count+1,updated_at=$3 WHERE user_id=$1 AND posting_window_id=$2 AND active_post_id=$4 AND deletion_count<2`, actorID, windowID, deletedAt, postID)
		if err != nil {
			return err
		}
		if command.RowsAffected() != 1 {
			return ErrPostSlotClosed
		}
		return nil
	})
	return objectKey, MapError(err)
}

func (r *PostRepository) ListFeed(ctx context.Context, query FeedQuery) ([]PostRecord, error) {
	if query.Limit < 1 || query.Limit > 51 {
		return nil, errors.New("feed limit out of range")
	}
	rows, err := Executor(ctx, r.db.Pool).Query(ctx, feedSelect,
		query.ViewerID, string(query.Kind), query.BeforePostedAt, query.BeforeID, query.Limit)
	if err != nil {
		return nil, MapError(err)
	}
	defer rows.Close()
	var records []PostRecord
	for rows.Next() {
		record, err := scanPost(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

const postSelect = `SELECT p.id,p.author_id,p.posting_window_id,p.image_id,p.caption,p.visibility,p.location_kind,p.latitude,p.longitude,p.locality,p.posted_at,p.is_on_time,p.late_seconds,p.deleted_at,
u.id,u.username,u.username_normalized,u.display_name,u.bio,u.region,u.account_type,u.status
FROM posts p JOIN users u ON u.id=p.author_id `

const feedSelect = `WITH candidates AS (
SELECT p.id,p.author_id,p.posting_window_id,p.image_id,p.caption,p.visibility,p.location_kind,p.latitude,p.longitude,p.locality,p.posted_at,p.is_on_time,p.late_seconds,p.deleted_at,
u.id AS uid,u.username,u.username_normalized,u.display_name,u.bio,u.region,u.account_type,u.status,
EXISTS(SELECT 1 FROM friendships f WHERE f.user_low_id=LEAST($1,p.author_id) AND f.user_high_id=GREATEST($1,p.author_id)) AS direct,
EXISTS(SELECT 1 FROM friendships vf JOIN friendships af ON
  (CASE WHEN vf.user_low_id=$1 THEN vf.user_high_id ELSE vf.user_low_id END)=
  (CASE WHEN af.user_low_id=p.author_id THEN af.user_high_id ELSE af.user_low_id END)
 WHERE $1 IN (vf.user_low_id,vf.user_high_id) AND p.author_id IN (af.user_low_id,af.user_high_id)) AS distance_two,
EXISTS(SELECT 1 FROM follows f WHERE f.follower_id=$1 AND f.followee_id=p.author_id) AS followed
FROM posts p JOIN users u ON u.id=p.author_id JOIN posting_windows w ON w.id=p.posting_window_id AND w.ended_at IS NULL
WHERE p.deleted_at IS NULL AND p.author_id<>$1 AND u.status='active'
AND NOT EXISTS(SELECT 1 FROM block_relations b WHERE (b.blocker_id=$1 AND b.blocked_id=p.author_id) OR (b.blocker_id=p.author_id AND b.blocked_id=$1))
AND EXISTS(SELECT 1 FROM users viewer JOIN posting_windows vw ON vw.region=viewer.region AND vw.ended_at IS NULL JOIN user_posting_window_states vs ON vs.user_id=viewer.id AND vs.posting_window_id=vw.id AND vs.active_post_id IS NOT NULL WHERE viewer.id=$1)
AND ($3::timestamptz IS NULL OR (p.posted_at,p.id)<($3,$4))
)
SELECT id,author_id,posting_window_id,image_id,caption,visibility,location_kind,latitude,longitude,locality,posted_at,is_on_time,late_seconds,deleted_at,
uid,username,username_normalized,display_name,bio,region,account_type,status
FROM candidates WHERE
((visibility='friends' AND direct) OR (visibility='friends_of_friends' AND (direct OR (distance_two AND NOT direct))) OR visibility='public')
AND CASE $2
  WHEN 'friends' THEN direct
  WHEN 'friends-of-friends' THEN distance_two AND NOT direct
  WHEN 'public' THEN visibility='public'
  WHEN 'following' THEN followed
  ELSE false END
ORDER BY posted_at DESC,id DESC LIMIT $5`

type rowScanner interface{ Scan(...any) error }

func scanPost(row rowScanner) (PostRecord, error) {
	var record PostRecord
	var visibility, locationKind string
	var latitude, longitude *float64
	var locality *string
	err := row.Scan(&record.Post.ID, &record.Post.AuthorID, &record.Post.PostingWindowID, &record.Post.ImageID,
		&record.Post.Caption, &visibility, &locationKind, &latitude, &longitude, &locality, &record.Post.PostedAt,
		&record.Post.OnTime, &record.Post.LateSeconds, &record.Post.DeletedAt,
		&record.Author.ID, &record.Author.Username, &record.Author.UsernameNormalized, &record.Author.DisplayName,
		&record.Author.Bio, &record.Author.Region, &record.Author.AccountType, &record.Author.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return record, ErrNotFound
	}
	if err != nil {
		return record, MapError(err)
	}
	record.Post.Visibility = domain.Visibility(visibility)
	record.Post.Location = domain.Location{Kind: domain.LocationKind(locationKind), Latitude: latitude, Longitude: longitude, Locality: locality}
	return record, nil
}

func (r *PostRepository) VisibilityFacts(ctx context.Context, viewerID, postID uuid.UUID) (domain.VisibilityFacts, error) {
	var facts domain.VisibilityFacts
	var direct, distanceTwo bool
	err := Executor(ctx, r.db.Pool).QueryRow(ctx, `
SELECT $1,p.author_id,viewer.status='active',author.status='active',p.visibility,
EXISTS(SELECT 1 FROM block_relations b WHERE (b.blocker_id=$1 AND b.blocked_id=p.author_id) OR (b.blocker_id=p.author_id AND b.blocked_id=$1)),
EXISTS(SELECT 1 FROM friendships f WHERE f.user_low_id=LEAST($1,p.author_id) AND f.user_high_id=GREATEST($1,p.author_id)),
EXISTS(SELECT 1 FROM friendships vf JOIN friendships af ON
  (CASE WHEN vf.user_low_id=$1 THEN vf.user_high_id ELSE vf.user_low_id END)=
  (CASE WHEN af.user_low_id=p.author_id THEN af.user_high_id ELSE af.user_low_id END)
 WHERE $1 IN (vf.user_low_id,vf.user_high_id) AND p.author_id IN (af.user_low_id,af.user_high_id)),
EXISTS(SELECT 1 FROM follows f WHERE f.follower_id=$1 AND f.followee_id=p.author_id),
EXISTS(SELECT 1 FROM posting_windows vw JOIN user_posting_window_states s ON s.user_id=$1 AND s.posting_window_id=vw.id AND s.active_post_id IS NOT NULL WHERE vw.region=viewer.region AND vw.ended_at IS NULL),
w.ended_at IS NULL AND p.deleted_at IS NULL
FROM posts p JOIN users author ON author.id=p.author_id JOIN users viewer ON viewer.id=$1 JOIN posting_windows w ON w.id=p.posting_window_id
WHERE p.id=$2`, viewerID, postID).
		Scan(&facts.ViewerID, &facts.AuthorID, &facts.ViewerActive, &facts.AuthorActive, &facts.Visibility,
			&facts.Blocked, &direct, &distanceTwo, &facts.Followed, &facts.ViewerPosted, &facts.PostCurrent)
	if errors.Is(err, pgx.ErrNoRows) {
		return facts, ErrNotFound
	}
	if err != nil {
		return facts, MapError(err)
	}
	switch {
	case direct:
		facts.Relationship = domain.DirectFriend
	case distanceTwo:
		facts.Relationship = domain.DistanceTwo
	default:
		facts.Relationship = domain.Unrelated
	}
	return facts, nil
}
