package postgres

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/domain"
	isurealimage "github.com/isucon/isucon2026/backend/internal/image"
	"github.com/jackc/pgx/v5"
	"time"
)

var ErrInteractionForbidden = errors.New("interaction forbidden")

type InteractionRepository struct{ db *DB }

func NewInteractionRepository(db *DB) *InteractionRepository { return &InteractionRepository{db: db} }
func (r *InteractionRepository) ReplaceRealMoji(ctx context.Context, reactor, postID uuid.UUID, object isurealimage.Object, reaction domain.ReactionType, id uuid.UUID, at time.Time) (domain.RealMojiRecord, error) {
	var result domain.RealMojiRecord
	err := r.db.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var author uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT author_id FROM posts WHERE id=$1`, postID).Scan(&author); err != nil {
			return err
		}
		if err := LockUsers(ctx, tx, reactor, author); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT p.author_id FROM posts p JOIN posting_windows w ON w.id=p.posting_window_id WHERE p.id=$1 AND p.deleted_at IS NULL AND w.ended_at IS NULL FOR UPDATE OF p`, postID).Scan(&author); err != nil {
			return err
		}
		allowed, err := interactionAllowed(ctx, tx, reactor, postID, false)
		if err != nil {
			return err
		}
		if author == reactor || !allowed {
			return ErrInteractionForbidden
		}
		if _, err := tx.Exec(ctx, `UPDATE realmojis SET replaced_at=$3 WHERE post_id=$1 AND reactor_id=$2 AND replaced_at IS NULL AND deleted_at IS NULL`, postID, reactor, at); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO image_objects(id,owner_user_id,sha256,object_key,byte_size,media_type,created_at)VALUES($1,$2,$3,$4,$5,$6,$7)`, object.ID, reactor, object.SHA256, object.ObjectKey, object.ByteSize, object.MediaType, at); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO realmojis(id,post_id,reactor_id,image_id,reaction_type,created_at)VALUES($1,$2,$3,$4,$5,$6)`, id, postID, reactor, object.ID, reaction, at); err != nil {
			return err
		}
		result = domain.RealMojiRecord{RealMoji: domain.RealMoji{ID: id, PostID: postID, ReactorID: reactor, ImageID: object.ID, ReactionType: reaction, CreatedAt: at}}
		return tx.QueryRow(ctx, `SELECT id,username,username_normalized,display_name,bio,region,account_type,status FROM users WHERE id=$1`, reactor).Scan(&result.Reactor.ID, &result.Reactor.Username, &result.Reactor.UsernameNormalized, &result.Reactor.DisplayName, &result.Reactor.Bio, &result.Reactor.Region, &result.Reactor.AccountType, &result.Reactor.Status)
	})
	return result, MapError(err)
}
func (r *InteractionRepository) CreateComment(ctx context.Context, author, postID, id uuid.UUID, body string, at time.Time) (domain.CommentRecord, error) {
	var result domain.CommentRecord
	err := r.db.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var postAuthor uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT author_id FROM posts WHERE id=$1`, postID).Scan(&postAuthor); err != nil {
			return err
		}
		if err := LockUsers(ctx, tx, author, postAuthor); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT p.author_id FROM posts p JOIN posting_windows w ON w.id=p.posting_window_id WHERE p.id=$1 AND p.deleted_at IS NULL AND w.ended_at IS NULL FOR UPDATE OF p`, postID).Scan(&postAuthor); err != nil {
			return err
		}
		allowed, err := interactionAllowed(ctx, tx, author, postID, true)
		if err != nil {
			return err
		}
		if !allowed {
			return ErrInteractionForbidden
		}
		if _, err := tx.Exec(ctx, `INSERT INTO comments(id,post_id,author_id,body,created_at)VALUES($1,$2,$3,$4,$5)`, id, postID, author, body, at); err != nil {
			return err
		}
		result = domain.CommentRecord{Comment: domain.Comment{ID: id, PostID: postID, AuthorID: author, Body: body, CreatedAt: at}}
		return tx.QueryRow(ctx, `SELECT id,username,username_normalized,display_name,bio,region,account_type,status FROM users WHERE id=$1`, author).Scan(&result.Author.ID, &result.Author.Username, &result.Author.UsernameNormalized, &result.Author.DisplayName, &result.Author.Bio, &result.Author.Region, &result.Author.AccountType, &result.Author.Status)
	})
	return result, MapError(err)
}

func interactionAllowed(ctx context.Context, tx pgx.Tx, viewer, postID uuid.UUID, comment bool) (bool, error) {
	var facts domain.VisibilityFacts
	var direct, distanceTwo bool
	err := tx.QueryRow(ctx, `SELECT $1,p.author_id,viewer.status='active',author.status='active',p.visibility,
EXISTS(SELECT 1 FROM block_relations b WHERE (b.blocker_id=$1 AND b.blocked_id=p.author_id) OR (b.blocker_id=p.author_id AND b.blocked_id=$1)),
EXISTS(SELECT 1 FROM friendships f WHERE f.user_low_id=LEAST($1,p.author_id) AND f.user_high_id=GREATEST($1,p.author_id)),
EXISTS(SELECT 1 FROM friendships vf JOIN friendships af ON (CASE WHEN vf.user_low_id=$1 THEN vf.user_high_id ELSE vf.user_low_id END)=(CASE WHEN af.user_low_id=p.author_id THEN af.user_high_id ELSE af.user_low_id END) WHERE $1 IN(vf.user_low_id,vf.user_high_id) AND p.author_id IN(af.user_low_id,af.user_high_id)),
EXISTS(SELECT 1 FROM posting_windows vw JOIN user_posting_window_states s ON s.user_id=$1 AND s.posting_window_id=vw.id AND s.active_post_id IS NOT NULL WHERE vw.region=viewer.region AND vw.ended_at IS NULL),w.ended_at IS NULL AND p.deleted_at IS NULL
FROM posts p JOIN users author ON author.id=p.author_id JOIN users viewer ON viewer.id=$1 JOIN posting_windows w ON w.id=p.posting_window_id WHERE p.id=$2`, viewer, postID).Scan(&facts.ViewerID, &facts.AuthorID, &facts.ViewerActive, &facts.AuthorActive, &facts.Visibility, &facts.Blocked, &direct, &distanceTwo, &facts.ViewerPosted, &facts.PostCurrent)
	if err != nil {
		return false, err
	}
	if direct {
		facts.Relationship = domain.DirectFriend
	} else if distanceTwo {
		facts.Relationship = domain.DistanceTwo
	}
	if !facts.ViewerActive || !facts.AuthorActive || facts.Blocked || !facts.ViewerPosted || !facts.PostCurrent {
		return false, nil
	}
	if comment {
		return domain.CanComment(facts.Visibility, facts.Relationship, viewer == facts.AuthorID), nil
	}
	return viewer != facts.AuthorID && domain.VisibilityAllows(facts.Visibility, facts.Relationship), nil
}

func (r *InteractionRepository) ListRealMojis(ctx context.Context, postID uuid.UUID, beforeAt *time.Time, beforeID *uuid.UUID, limit int) ([]domain.RealMojiRecord, error) {
	rows, err := Executor(ctx, r.db.Pool).Query(ctx, `SELECT x.id,x.post_id,x.reactor_id,x.image_id,x.reaction_type,x.created_at,u.id,u.username,u.username_normalized,u.display_name,u.bio,u.region,u.account_type,u.status FROM realmojis x JOIN users u ON u.id=x.reactor_id JOIN posts p ON p.id=x.post_id WHERE x.post_id=$1 AND x.replaced_at IS NULL AND x.deleted_at IS NULL AND p.deleted_at IS NULL AND ($2::timestamptz IS NULL OR (x.created_at,x.id)<($2,$3)) ORDER BY x.created_at DESC,x.id DESC LIMIT $4`, postID, beforeAt, beforeID, limit)
	if err != nil {
		return nil, MapError(err)
	}
	defer rows.Close()
	var records []domain.RealMojiRecord
	for rows.Next() {
		var record domain.RealMojiRecord
		if err := rows.Scan(&record.RealMoji.ID, &record.RealMoji.PostID, &record.RealMoji.ReactorID, &record.RealMoji.ImageID, &record.RealMoji.ReactionType, &record.RealMoji.CreatedAt, &record.Reactor.ID, &record.Reactor.Username, &record.Reactor.UsernameNormalized, &record.Reactor.DisplayName, &record.Reactor.Bio, &record.Reactor.Region, &record.Reactor.AccountType, &record.Reactor.Status); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}
func (r *InteractionRepository) ListComments(ctx context.Context, postID uuid.UUID, afterAt *time.Time, afterID *uuid.UUID, limit int) ([]domain.CommentRecord, error) {
	rows, err := Executor(ctx, r.db.Pool).Query(ctx, `SELECT x.id,x.post_id,x.author_id,x.body,x.created_at,u.id,u.username,u.username_normalized,u.display_name,u.bio,u.region,u.account_type,u.status FROM comments x JOIN users u ON u.id=x.author_id JOIN posts p ON p.id=x.post_id WHERE x.post_id=$1 AND x.deleted_at IS NULL AND p.deleted_at IS NULL AND ($2::timestamptz IS NULL OR (x.created_at,x.id)>($2,$3)) ORDER BY x.created_at,x.id LIMIT $4`, postID, afterAt, afterID, limit)
	if err != nil {
		return nil, MapError(err)
	}
	defer rows.Close()
	var records []domain.CommentRecord
	for rows.Next() {
		var record domain.CommentRecord
		if err := rows.Scan(&record.Comment.ID, &record.Comment.PostID, &record.Comment.AuthorID, &record.Comment.Body, &record.Comment.CreatedAt, &record.Author.ID, &record.Author.Username, &record.Author.UsernameNormalized, &record.Author.DisplayName, &record.Author.Bio, &record.Author.Region, &record.Author.AccountType, &record.Author.Status); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}
func (r *InteractionRepository) RealMojiImage(ctx context.Context, imageID uuid.UUID) (uuid.UUID, string, error) {
	var postID uuid.UUID
	var key string
	err := Executor(ctx, r.db.Pool).QueryRow(ctx, `SELECT x.post_id,i.object_key FROM realmojis x JOIN image_objects i ON i.id=x.image_id JOIN posts p ON p.id=x.post_id WHERE x.image_id=$1 AND x.replaced_at IS NULL AND x.deleted_at IS NULL AND p.deleted_at IS NULL`, imageID).Scan(&postID, &key)
	return postID, key, MapError(err)
}
func (r *InteractionRepository) DeleteRealMoji(ctx context.Context, actor, id uuid.UUID, at time.Time) error {
	return r.delete(ctx, actor, id, at, true)
}
func (r *InteractionRepository) DeleteComment(ctx context.Context, actor, id uuid.UUID, at time.Time) error {
	return r.delete(ctx, actor, id, at, false)
}
func (r *InteractionRepository) delete(ctx context.Context, actor, id uuid.UUID, at time.Time, real bool) error {
	return r.db.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := LockUsers(ctx, tx, actor); err != nil {
			return err
		}
		table, owner := "comments", "author_id"
		if real {
			table, owner = "realmojis", "reactor_id"
		}
		query := `SELECT x.` + owner + `,p.author_id FROM ` + table + ` x JOIN posts p ON p.id=x.post_id WHERE x.id=$1 AND x.deleted_at IS NULL FOR UPDATE OF x`
		var sender, postAuthor uuid.UUID
		if err := tx.QueryRow(ctx, query, id).Scan(&sender, &postAuthor); err != nil {
			return err
		}
		if actor != sender && actor != postAuthor {
			return ErrInteractionForbidden
		}
		_, err := tx.Exec(ctx, `UPDATE `+table+` SET deleted_at=$2 WHERE id=$1`, id, at)
		return err
	})
}
