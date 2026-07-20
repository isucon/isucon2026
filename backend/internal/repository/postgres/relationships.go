package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/domain"
	"github.com/jackc/pgx/v5"
)

var (
	ErrRelationshipConflict  = errors.New("relationship conflict")
	ErrRelationshipForbidden = errors.New("relationship action forbidden")
)

type RelationshipRepository struct {
	db    *DB
	ids   domain.IDGenerator
	clock interface{ Now() time.Time }
}

func NewRelationshipRepository(db *DB, ids domain.IDGenerator, clock interface{ Now() time.Time }) *RelationshipRepository {
	return &RelationshipRepository{db: db, ids: ids, clock: clock}
}

func (r *RelationshipRepository) CreateRequest(ctx context.Context, requester, recipient uuid.UUID) (domain.FriendRequest, error) {
	pair, err := domain.NormalizePair(requester, recipient)
	if err != nil {
		return domain.FriendRequest{}, ErrInvalid
	}
	var result domain.FriendRequest
	err = r.db.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := LockUsers(ctx, tx, requester, recipient); err != nil {
			return err
		}
		var active int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM users WHERE id IN($1,$2) AND status='active'`, requester, recipient).Scan(&active); err != nil || active != 2 {
			if err != nil {
				return err
			}
			return ErrRelationshipConflict
		}
		var blocked, friend bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM block_relations WHERE (blocker_id=$1 AND blocked_id=$2) OR (blocker_id=$2 AND blocked_id=$1)),EXISTS(SELECT 1 FROM friendships WHERE user_low_id=$3 AND user_high_id=$4)`, requester, recipient, pair.Low, pair.High).Scan(&blocked, &friend); err != nil {
			return err
		}
		if blocked || friend {
			return ErrRelationshipConflict
		}
		var existing domain.FriendRequest
		err := tx.QueryRow(ctx, `SELECT id,requester_id,recipient_id,status,created_at,updated_at FROM friend_requests WHERE user_low_id=$1 AND user_high_id=$2 AND status='pending' FOR UPDATE`, pair.Low, pair.High).Scan(&existing.ID, &existing.RequesterID, &existing.RecipientID, &existing.Status, &existing.CreatedAt, &existing.UpdatedAt)
		now := r.clock.Now()
		if err == nil {
			if existing.RequesterID == requester {
				return ErrRelationshipConflict
			}
			if _, err := tx.Exec(ctx, `UPDATE friend_requests SET status='mutualized',updated_at=$2 WHERE id=$1`, existing.ID, now); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO friendships(id,user_low_id,user_high_id,source_request_id,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$5)`, r.ids.New(), pair.Low, pair.High, existing.ID, now); err != nil {
				return err
			}
			existing.Status = domain.RequestMutualized
			existing.UpdatedAt = now
			result = existing
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		result = domain.FriendRequest{ID: r.ids.New(), RequesterID: requester, RecipientID: recipient, Status: domain.RequestPending, CreatedAt: now, UpdatedAt: now}
		_, err = tx.Exec(ctx, `INSERT INTO friend_requests(id,requester_id,recipient_id,user_low_id,user_high_id,status,created_at,updated_at) VALUES($1,$2,$3,$4,$5,'pending',$6,$6)`, result.ID, requester, recipient, pair.Low, pair.High, now)
		return err
	})
	return result, err
}

func (r *RelationshipRepository) TransitionRequest(ctx context.Context, actor, requestID uuid.UUID, next domain.FriendRequestStatus) (domain.FriendRequest, error) {
	var result domain.FriendRequest
	err := r.db.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT id,requester_id,recipient_id,status,created_at,updated_at FROM friend_requests WHERE id=$1 FOR UPDATE`, requestID).Scan(&result.ID, &result.RequesterID, &result.RecipientID, &result.Status, &result.CreatedAt, &result.UpdatedAt); err != nil {
			return err
		}
		if err := LockUsers(ctx, tx, result.RequesterID, result.RecipientID); err != nil {
			return err
		}
		if err := result.CanTransition(actor, next); err != nil {
			return ErrRelationshipForbidden
		}
		now := r.clock.Now()
		if _, err := tx.Exec(ctx, `UPDATE friend_requests SET status=$2,updated_at=$3 WHERE id=$1`, requestID, next, now); err != nil {
			return err
		}
		if next == domain.RequestAccepted {
			pair, _ := domain.NormalizePair(result.RequesterID, result.RecipientID)
			if _, err := tx.Exec(ctx, `INSERT INTO friendships(id,user_low_id,user_high_id,source_request_id,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$5)`, r.ids.New(), pair.Low, pair.High, result.ID, now); err != nil {
				return err
			}
		}
		result.Status = next
		result.UpdatedAt = now
		return nil
	})
	return result, MapError(err)
}

func (r *RelationshipRepository) List(ctx context.Context, userID uuid.UUID, kind domain.RelationshipKind, beforeAt *time.Time, beforeID *uuid.UUID, limit int) ([]domain.RelationshipRecord, error) {
	if err := kind.Validate(); err != nil {
		return nil, ErrInvalid
	}
	var query string
	switch kind {
	case domain.RelationshipFriends:
		query = `SELECT f.id,u.id,u.username,u.username_normalized,u.display_name,u.bio,u.region,u.account_type,u.status,'friends',f.updated_at FROM friendships f JOIN users u ON u.id=CASE WHEN f.user_low_id=$1 THEN f.user_high_id ELSE f.user_low_id END WHERE $1 IN(f.user_low_id,f.user_high_id) AND u.status='active' AND ($2::timestamptz IS NULL OR (f.updated_at,f.id)<($2,$3)) ORDER BY f.updated_at DESC,f.id DESC LIMIT $4`
	case domain.RelationshipFollowing:
		query = `SELECT f.id,u.id,u.username,u.username_normalized,u.display_name,u.bio,u.region,u.account_type,u.status,'following',f.updated_at FROM follows f JOIN users u ON u.id=f.followee_id WHERE f.follower_id=$1 AND u.status='active' AND ($2::timestamptz IS NULL OR (f.updated_at,f.id)<($2,$3)) ORDER BY f.updated_at DESC,f.id DESC LIMIT $4`
	case domain.RelationshipFollowers:
		query = `SELECT f.id,u.id,u.username,u.username_normalized,u.display_name,u.bio,u.region,u.account_type,u.status,'followers',f.updated_at FROM follows f JOIN users u ON u.id=f.follower_id WHERE f.followee_id=$1 AND u.status='active' AND ($2::timestamptz IS NULL OR (f.updated_at,f.id)<($2,$3)) ORDER BY f.updated_at DESC,f.id DESC LIMIT $4`
	case domain.RelationshipIncoming:
		query = `SELECT f.id,u.id,u.username,u.username_normalized,u.display_name,u.bio,u.region,u.account_type,u.status,f.status,f.updated_at FROM friend_requests f JOIN users u ON u.id=f.requester_id WHERE f.recipient_id=$1 AND f.status='pending' AND u.status='active' AND ($2::timestamptz IS NULL OR (f.updated_at,f.id)<($2,$3)) ORDER BY f.updated_at DESC,f.id DESC LIMIT $4`
	case domain.RelationshipOutgoing:
		query = `SELECT f.id,u.id,u.username,u.username_normalized,u.display_name,u.bio,u.region,u.account_type,u.status,f.status,f.updated_at FROM friend_requests f JOIN users u ON u.id=f.recipient_id WHERE f.requester_id=$1 AND f.status='pending' AND u.status='active' AND ($2::timestamptz IS NULL OR (f.updated_at,f.id)<($2,$3)) ORDER BY f.updated_at DESC,f.id DESC LIMIT $4`
	}
	rows, err := Executor(ctx, r.db.Pool).Query(ctx, query, userID, beforeAt, beforeID, limit)
	if err != nil {
		return nil, MapError(err)
	}
	defer rows.Close()
	var records []domain.RelationshipRecord
	for rows.Next() {
		var record domain.RelationshipRecord
		if err := rows.Scan(&record.RelationID, &record.User.ID, &record.User.Username, &record.User.UsernameNormalized, &record.User.DisplayName, &record.User.Bio, &record.User.Region, &record.User.AccountType, &record.User.Status, &record.Status, &record.UpdatedAt); err != nil {
			return nil, err
		}
		record.User = domain.DiscloseProfile(record.User, kind == domain.RelationshipFriends)
		records = append(records, record)
	}
	return records, rows.Err()
}

func (r *RelationshipRepository) DeleteFriendship(ctx context.Context, actor, target uuid.UUID) error {
	pair, err := domain.NormalizePair(actor, target)
	if err != nil {
		return ErrInvalid
	}
	return r.db.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := LockUsers(ctx, tx, actor, target); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM friendships WHERE user_low_id=$1 AND user_high_id=$2`, pair.Low, pair.High)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrRelationshipConflict
		}
		return nil
	})
}
func (r *RelationshipRepository) Follow(ctx context.Context, actor, target uuid.UUID) error {
	return r.db.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := domain.NormalizePair(actor, target); err != nil {
			return ErrInvalid
		}
		if err := LockUsers(ctx, tx, actor, target); err != nil {
			return err
		}
		var kind, status string
		var blocked bool
		if err := tx.QueryRow(ctx, `SELECT u.account_type,u.status,EXISTS(SELECT 1 FROM block_relations WHERE (blocker_id=$1 AND blocked_id=$2) OR (blocker_id=$2 AND blocked_id=$1)) FROM users u WHERE u.id=$2`, actor, target).Scan(&kind, &status, &blocked); err != nil {
			return err
		}
		if !domain.CanFollow(kind) || status != "active" || blocked {
			return ErrRelationshipConflict
		}
		_, err := tx.Exec(ctx, `INSERT INTO follows(id,follower_id,followee_id,created_at,updated_at) VALUES($1,$2,$3,$4,$4)`, r.ids.New(), actor, target, r.clock.Now())
		return err
	})
}
func (r *RelationshipRepository) Unfollow(ctx context.Context, actor, target uuid.UUID) error {
	tag, err := Executor(ctx, r.db.Pool).Exec(ctx, `DELETE FROM follows WHERE follower_id=$1 AND followee_id=$2`, actor, target)
	if err != nil {
		return MapError(err)
	}
	if tag.RowsAffected() != 1 {
		return ErrRelationshipConflict
	}
	return nil
}
func (r *RelationshipRepository) Block(ctx context.Context, actor, target uuid.UUID) error {
	return r.db.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		pair, err := domain.NormalizePair(actor, target)
		if err != nil {
			return ErrInvalid
		}
		if err := LockUsers(ctx, tx, actor, target); err != nil {
			return err
		}
		now := r.clock.Now()
		if _, err := tx.Exec(ctx, `INSERT INTO block_relations(id,blocker_id,blocked_id,created_at) VALUES($1,$2,$3,$4)`, r.ids.New(), actor, target, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM friendships WHERE user_low_id=$1 AND user_high_id=$2`, pair.Low, pair.High); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM follows WHERE (follower_id=$1 AND followee_id=$2) OR (follower_id=$2 AND followee_id=$1)`, actor, target); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE friend_requests SET status='cancelled',updated_at=$3 WHERE user_low_id=$1 AND user_high_id=$2 AND status='pending'`, pair.Low, pair.High, now)
		return err
	})
}
func (r *RelationshipRepository) Unblock(ctx context.Context, actor, target uuid.UUID) error {
	tag, err := Executor(ctx, r.db.Pool).Exec(ctx, `DELETE FROM block_relations WHERE blocker_id=$1 AND blocked_id=$2`, actor, target)
	if err != nil {
		return MapError(err)
	}
	if tag.RowsAffected() != 1 {
		return ErrRelationshipForbidden
	}
	return nil
}
func (r *RelationshipRepository) Certify(ctx context.Context, target uuid.UUID) error {
	tag, err := Executor(ctx, r.db.Pool).Exec(ctx, `UPDATE users SET account_type='certified_public',updated_at=$2 WHERE id=$1 AND status='active' AND account_type='regular'`, target, r.clock.Now())
	if err != nil {
		return MapError(err)
	}
	if tag.RowsAffected() != 1 {
		return ErrRelationshipConflict
	}
	return nil
}
