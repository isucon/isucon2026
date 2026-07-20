package service

import (
	"context"

	"github.com/google/uuid"
)

// Block and Unblock delegate to the transactional relationship store. The
// store locks both users in normalized order before block-priority cleanup.
func (s *RelationshipService) Block(ctx context.Context, actor, target uuid.UUID) error {
	return s.relations.Block(ctx, actor, target)
}

func (s *RelationshipService) Unblock(ctx context.Context, actor, target uuid.UUID) error {
	return s.relations.Unblock(ctx, actor, target)
}
