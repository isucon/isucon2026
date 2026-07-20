package postgres

import (
	"context"
	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/domain"
	"time"
)

type MemoryRepository struct {
	db           *DB
	interactions *InteractionRepository
}

func NewMemoryRepository(db *DB) *MemoryRepository {
	return &MemoryRepository{db: db, interactions: NewInteractionRepository(db)}
}
func (r *MemoryRepository) ListMonth(ctx context.Context, owner uuid.UUID, month string, beforeAt *time.Time, beforeID *uuid.UUID, limit int) ([]domain.MemoryRecord, error) {
	return r.list(ctx, owner, `to_char(w.local_date,'YYYY-MM')=$2`, month, beforeAt, beforeID, limit)
}
func (r *MemoryRepository) ListDate(ctx context.Context, owner uuid.UUID, date string, beforeAt *time.Time, beforeID *uuid.UUID, limit int) ([]domain.MemoryRecord, error) {
	return r.list(ctx, owner, `w.local_date=$2::date`, date, beforeAt, beforeID, limit)
}
func (r *MemoryRepository) list(ctx context.Context, owner uuid.UUID, predicate, value string, beforeAt *time.Time, beforeID *uuid.UUID, limit int) ([]domain.MemoryRecord, error) {
	rows, err := Executor(ctx, r.db.Pool).Query(ctx, postSelect+` JOIN posting_windows w ON w.id=p.posting_window_id WHERE p.author_id=$1 AND `+predicate+` AND p.deleted_at IS NULL AND ($3::timestamptz IS NULL OR (p.posted_at,p.id)<($3,$4)) ORDER BY p.posted_at DESC,p.id DESC LIMIT $5`, owner, value, beforeAt, beforeID, limit)
	if err != nil {
		return nil, MapError(err)
	}
	defer rows.Close()
	var records []domain.MemoryRecord
	for rows.Next() {
		post, err := scanPost(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, domain.MemoryRecord{PostRecord: post})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for index := range records {
		postID := records[index].PostRecord.Post.ID
		records[index].RealMojis, _ = r.interactions.ListRealMojis(ctx, postID, nil, nil, 50)
		records[index].Comments, _ = r.interactions.ListComments(ctx, postID, nil, nil, 50)
	}
	return records, nil
}
