package postgres

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/domain"
	"github.com/jackc/pgx/v5"
)

type UserRepository struct{ db *DB }

func NewUserRepository(db *DB) *UserRepository { return &UserRepository{db: db} }

func (r *UserRepository) Search(ctx context.Context, viewer uuid.UUID, query string, beforeName *string, beforeID *uuid.UUID, limit int) ([]domain.User, error) {
	normalized := domain.NormalizeSearchText(query)
	if !domain.ValidateCodePointLength(query, 1, 100) {
		return nil, ErrInvalid
	}
	pattern := "%" + strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(normalized) + "%"
	rows, err := Executor(ctx, r.db.Pool).Query(ctx, `SELECT id,username,username_normalized,display_name,bio,region,account_type,status FROM users u WHERE u.id<>$1 AND u.status='active' AND (u.username_normalized LIKE $2 ESCAPE E'\\' OR u.display_name_search LIKE $2 ESCAPE E'\\') AND NOT EXISTS(SELECT 1 FROM block_relations b WHERE (b.blocker_id=$1 AND b.blocked_id=u.id) OR (b.blocker_id=u.id AND b.blocked_id=$1)) AND ($3::text IS NULL OR (u.username_normalized,u.id)>($3,$4)) ORDER BY u.username_normalized,u.id LIMIT $5`, viewer, pattern, beforeName, beforeID, limit)
	if err != nil {
		return nil, MapError(err)
	}
	defer rows.Close()
	var users []domain.User
	for rows.Next() {
		var user domain.User
		if err := rows.Scan(&user.ID, &user.Username, &user.UsernameNormalized, &user.DisplayName, &user.Bio, &user.Region, &user.AccountType, &user.Status); err != nil {
			return nil, err
		}
		user = domain.DiscloseProfile(user, false)
		users = append(users, user)
	}
	return users, rows.Err()
}

func (r *UserRepository) GetPublic(ctx context.Context, viewer, target uuid.UUID) (domain.User, error) {
	var user domain.User
	var direct bool
	err := Executor(ctx, r.db.Pool).QueryRow(ctx, `SELECT u.id,u.username,u.username_normalized,u.display_name,u.bio,u.region,u.account_type,u.status,EXISTS(SELECT 1 FROM friendships f WHERE f.user_low_id=LEAST($1,u.id) AND f.user_high_id=GREATEST($1,u.id)) FROM users u WHERE u.id=$2 AND u.status='active' AND NOT EXISTS(SELECT 1 FROM block_relations b WHERE (b.blocker_id=$1 AND b.blocked_id=u.id) OR (b.blocker_id=u.id AND b.blocked_id=$1))`, viewer, target).Scan(&user.ID, &user.Username, &user.UsernameNormalized, &user.DisplayName, &user.Bio, &user.Region, &user.AccountType, &user.Status, &direct)
	if errors.Is(err, pgx.ErrNoRows) {
		return user, ErrNotFound
	}
	return domain.DiscloseProfile(user, direct), MapError(err)
}
