package isureal

import (
	"context"
	"time"

	"github.com/jmoiron/sqlx"
)

type RealMoji struct {
	ID           string     `db:"id"`
	PostID       string     `db:"post_id"`
	ReactorID    string     `db:"reactor_id"`
	ImageID      string     `db:"image_id"`
	ReactionType string     `db:"reaction_type"`
	CreatedAt    time.Time  `db:"created_at"`
	ReplacedAt   *time.Time `db:"replaced_at"`
	DeletedAt    *time.Time `db:"deleted_at"`
}

func getRealMoji(ctx context.Context, db sqlx.QueryerContext, id string) (RealMoji, error) {
	var reaction RealMoji
	err := sqlx.GetContext(ctx, db, &reaction, `SELECT id, post_id, reactor_id, image_id, reaction_type, created_at, replaced_at, deleted_at FROM realmojis WHERE id=$1`, id)
	return reaction, err
}
