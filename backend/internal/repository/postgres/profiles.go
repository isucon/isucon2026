package postgres

import (
	"context"
	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/domain"
	isurealimage "github.com/isucon/isucon2026/backend/internal/image"
	"github.com/jackc/pgx/v5"
	"time"
)

type ProfileRepository struct {
	db    *DB
	clock interface{ Now() time.Time }
}

func NewProfileRepository(db *DB, clock interface{ Now() time.Time }) *ProfileRepository {
	return &ProfileRepository{db: db, clock: clock}
}
func (r *ProfileRepository) Get(ctx context.Context, userID uuid.UUID) (domain.User, *string, error) {
	var user domain.User
	var pending *string
	err := Executor(ctx, r.db.Pool).QueryRow(ctx, `SELECT id,username,username_normalized,display_name,bio,profile_image_id,region,pending_region,account_type,status FROM users WHERE id=$1 AND status='active'`, userID).Scan(&user.ID, &user.Username, &user.UsernameNormalized, &user.DisplayName, &user.Bio, &user.ProfileImageID, &user.Region, &pending, &user.AccountType, &user.Status)
	return user, pending, MapError(err)
}
func (r *ProfileRepository) Update(ctx context.Context, userID uuid.UUID, input domain.ProfileUpdate) (domain.User, *string, error) {
	err := r.db.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := LockUsers(ctx, tx, userID); err != nil {
			return err
		}
		var currentRegion string
		if err := tx.QueryRow(ctx, `SELECT region FROM users WHERE id=$1 AND status='active'`, userID).Scan(&currentRegion); err != nil {
			return err
		}
		if input.Region != nil {
			var active bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM posting_windows WHERE region=$1 AND ended_at IS NULL)`, currentRegion).Scan(&active); err != nil {
				return err
			}
			if active {
				if *input.Region == currentRegion {
					_, err := tx.Exec(ctx, `UPDATE users SET pending_region=NULL WHERE id=$1`, userID)
					if err != nil {
						return err
					}
				} else {
					if _, err := tx.Exec(ctx, `UPDATE users SET pending_region=$2 WHERE id=$1`, userID, *input.Region); err != nil {
						return err
					}
				}
			} else {
				if _, err := tx.Exec(ctx, `UPDATE users SET region=$2,pending_region=NULL WHERE id=$1`, userID, *input.Region); err != nil {
					return err
				}
			}
		}
		_, err := tx.Exec(ctx, `UPDATE users SET display_name=COALESCE($2,display_name),display_name_search=CASE WHEN $2::text IS NULL THEN display_name_search ELSE $3 END,bio=COALESCE($4,bio),updated_at=$5 WHERE id=$1`, userID, input.DisplayName, func() any {
			if input.DisplayName == nil {
				return nil
			}
			return domain.NormalizeSearchText(*input.DisplayName)
		}(), input.Bio, r.clock.Now())
		return err
	})
	if err != nil {
		return domain.User{}, nil, err
	}
	return r.Get(ctx, userID)
}
func (r *ProfileRepository) SetImage(ctx context.Context, userID uuid.UUID, object isurealimage.Object) (string, error) {
	var oldKey string
	err := r.db.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := LockUsers(ctx, tx, userID); err != nil {
			return err
		}
		var oldID *uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT profile_image_id FROM users WHERE id=$1 AND status='active'`, userID).Scan(&oldID); err != nil {
			return err
		}
		if oldID != nil {
			_ = tx.QueryRow(ctx, `SELECT object_key FROM image_objects WHERE id=$1`, *oldID).Scan(&oldKey)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO image_objects(id,owner_user_id,sha256,object_key,byte_size,media_type,created_at)VALUES($1,$2,$3,$4,$5,$6,$7)`, object.ID, userID, object.SHA256, object.ObjectKey, object.ByteSize, object.MediaType, r.clock.Now()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE users SET profile_image_id=$2,updated_at=$3 WHERE id=$1`, userID, object.ID, r.clock.Now()); err != nil {
			return err
		}
		if oldID != nil {
			_, err := tx.Exec(ctx, `DELETE FROM image_objects WHERE id=$1`, *oldID)
			return err
		}
		return nil
	})
	return oldKey, err
}
func (r *ProfileRepository) DeleteImage(ctx context.Context, userID uuid.UUID) (string, error) {
	var key string
	err := r.db.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := LockUsers(ctx, tx, userID); err != nil {
			return err
		}
		var id uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT i.id,i.object_key FROM users u JOIN image_objects i ON i.id=u.profile_image_id WHERE u.id=$1 FOR UPDATE OF u`, userID).Scan(&id, &key); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE users SET profile_image_id=NULL,updated_at=$2 WHERE id=$1`, userID, r.clock.Now()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM image_objects WHERE id=$1`, id)
		return err
	})
	return key, MapError(err)
}
func (r *ProfileRepository) ProfileImage(ctx context.Context, viewer, imageID uuid.UUID) (string, error) {
	var key string
	err := Executor(ctx, r.db.Pool).QueryRow(ctx, `SELECT i.object_key FROM image_objects i JOIN users u ON u.profile_image_id=i.id JOIN users v ON v.id=$1 WHERE i.id=$2 AND u.status='active' AND v.status='active' AND NOT EXISTS(SELECT 1 FROM block_relations b WHERE (b.blocker_id=v.id AND b.blocked_id=u.id) OR (b.blocker_id=u.id AND b.blocked_id=v.id))`, viewer, imageID).Scan(&key)
	return key, MapError(err)
}
