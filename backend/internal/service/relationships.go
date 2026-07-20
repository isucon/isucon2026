package service

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/domain"
)

type RelationshipStore interface {
	CreateRequest(context.Context, uuid.UUID, uuid.UUID) (domain.FriendRequest, error)
	TransitionRequest(context.Context, uuid.UUID, uuid.UUID, domain.FriendRequestStatus) (domain.FriendRequest, error)
	DeleteFriendship(context.Context, uuid.UUID, uuid.UUID) error
	Follow(context.Context, uuid.UUID, uuid.UUID) error
	Unfollow(context.Context, uuid.UUID, uuid.UUID) error
	Block(context.Context, uuid.UUID, uuid.UUID) error
	Unblock(context.Context, uuid.UUID, uuid.UUID) error
	List(context.Context, uuid.UUID, domain.RelationshipKind, *time.Time, *uuid.UUID, int) ([]domain.RelationshipRecord, error)
}
type UserDirectory interface {
	Search(context.Context, uuid.UUID, string, *string, *uuid.UUID, int) ([]domain.User, error)
	GetPublic(context.Context, uuid.UUID, uuid.UUID) (domain.User, error)
}
type RelationshipService struct {
	relations RelationshipStore
	users     UserDirectory
}

func NewRelationshipService(relations RelationshipStore, users UserDirectory) *RelationshipService {
	return &RelationshipService{relations: relations, users: users}
}
func (s *RelationshipService) Search(ctx context.Context, viewer uuid.UUID, q string, beforeName *string, beforeID *uuid.UUID, limit int) ([]domain.User, error) {
	return s.users.Search(ctx, viewer, q, beforeName, beforeID, limit)
}
func (s *RelationshipService) GetUser(ctx context.Context, viewer, target uuid.UUID) (domain.User, error) {
	return s.users.GetPublic(ctx, viewer, target)
}
func (s *RelationshipService) CreateRequest(ctx context.Context, actor, target uuid.UUID) (domain.FriendRequest, error) {
	return s.relations.CreateRequest(ctx, actor, target)
}
func (s *RelationshipService) Transition(ctx context.Context, actor, request uuid.UUID, action string) (domain.FriendRequest, error) {
	next := map[string]domain.FriendRequestStatus{"accept": domain.RequestAccepted, "reject": domain.RequestRejected, "cancel": domain.RequestCancelled}[action]
	if next == "" {
		return domain.FriendRequest{}, domain.ErrInvalidTransition
	}
	return s.relations.TransitionRequest(ctx, actor, request, next)
}
func (s *RelationshipService) DeleteFriendship(ctx context.Context, actor, target uuid.UUID) error {
	return s.relations.DeleteFriendship(ctx, actor, target)
}
func (s *RelationshipService) Follow(ctx context.Context, actor, target uuid.UUID) error {
	return s.relations.Follow(ctx, actor, target)
}
func (s *RelationshipService) Unfollow(ctx context.Context, actor, target uuid.UUID) error {
	return s.relations.Unfollow(ctx, actor, target)
}
func (s *RelationshipService) List(ctx context.Context, actor uuid.UUID, kind domain.RelationshipKind, beforeAt *time.Time, beforeID *uuid.UUID, limit int) ([]domain.RelationshipRecord, error) {
	return s.relations.List(ctx, actor, kind, beforeAt, beforeID, limit)
}
