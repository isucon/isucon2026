package service

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/domain"
	isurealimage "github.com/isucon/isucon2026/backend/internal/image"
)

var ErrInteractionNotAllowed = errors.New("interaction is not allowed")

type InteractionStore interface {
	ReplaceRealMoji(context.Context, uuid.UUID, uuid.UUID, isurealimage.Object, domain.ReactionType, uuid.UUID, time.Time) (domain.RealMojiRecord, error)
	CreateComment(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, string, time.Time) (domain.CommentRecord, error)
	DeleteRealMoji(context.Context, uuid.UUID, uuid.UUID, time.Time) error
	DeleteComment(context.Context, uuid.UUID, uuid.UUID, time.Time) error
	ListRealMojis(context.Context, uuid.UUID, *time.Time, *uuid.UUID, int) ([]domain.RealMojiRecord, error)
	ListComments(context.Context, uuid.UUID, *time.Time, *uuid.UUID, int) ([]domain.CommentRecord, error)
	RealMojiImage(context.Context, uuid.UUID) (uuid.UUID, string, error)
}
type InteractionService struct {
	repository InteractionStore
	visibility *VisibilityPolicy
	images     PostImageStore
	clock      interface{ Now() time.Time }
	ids        domain.IDGenerator
}

func NewInteractionService(repository InteractionStore, visibility *VisibilityPolicy, images PostImageStore, clock interface{ Now() time.Time }, ids domain.IDGenerator) *InteractionService {
	return &InteractionService{repository: repository, visibility: visibility, images: images, clock: clock, ids: ids}
}
func (s *InteractionService) ReplaceRealMoji(ctx context.Context, actor, postID uuid.UUID, reaction domain.ReactionType, image io.Reader) (domain.RealMojiRecord, error) {
	if reaction.Validate() != nil || image == nil {
		return domain.RealMojiRecord{}, ErrInvalidPost
	}
	allowed, err := s.visibility.CanAddRealMoji(ctx, actor, postID)
	if err != nil || !allowed {
		if err != nil {
			return domain.RealMojiRecord{}, err
		}
		return domain.RealMojiRecord{}, ErrInteractionNotAllowed
	}
	object, err := s.images.Save(ctx, actor, isurealimage.RealMojiImage, image)
	if err != nil {
		return domain.RealMojiRecord{}, err
	}
	record, err := s.repository.ReplaceRealMoji(ctx, actor, postID, object, reaction, s.ids.New(), s.clock.Now())
	if err != nil {
		_ = s.images.Remove(object.ObjectKey)
	}
	return record, err
}
func (s *InteractionService) CreateComment(ctx context.Context, actor, postID uuid.UUID, body string) (domain.CommentRecord, error) {
	if !domain.ValidateComment(body) {
		return domain.CommentRecord{}, ErrInvalidPost
	}
	allowed, err := s.visibility.CanComment(ctx, actor, postID)
	if err != nil || !allowed {
		if err != nil {
			return domain.CommentRecord{}, err
		}
		return domain.CommentRecord{}, ErrInteractionNotAllowed
	}
	return s.repository.CreateComment(ctx, actor, postID, s.ids.New(), body, s.clock.Now())
}
func (s *InteractionService) DeleteRealMoji(ctx context.Context, actor, id uuid.UUID) error {
	return s.repository.DeleteRealMoji(ctx, actor, id, s.clock.Now())
}
func (s *InteractionService) DeleteComment(ctx context.Context, actor, id uuid.UUID) error {
	return s.repository.DeleteComment(ctx, actor, id, s.clock.Now())
}
func (s *InteractionService) ListRealMojis(ctx context.Context, viewer, postID uuid.UUID, beforeAt *time.Time, beforeID *uuid.UUID, limit int) ([]domain.RealMojiRecord, error) {
	allowed, err := s.visibility.CanViewPost(ctx, viewer, postID)
	if err != nil || !allowed {
		if err != nil {
			return nil, err
		}
		return nil, ErrInteractionNotAllowed
	}
	return s.repository.ListRealMojis(ctx, postID, beforeAt, beforeID, limit)
}
func (s *InteractionService) ListComments(ctx context.Context, viewer, postID uuid.UUID, afterAt *time.Time, afterID *uuid.UUID, limit int) ([]domain.CommentRecord, error) {
	allowed, err := s.visibility.CanComment(ctx, viewer, postID)
	if err != nil || !allowed {
		if err != nil {
			return nil, err
		}
		return nil, ErrInteractionNotAllowed
	}
	return s.repository.ListComments(ctx, postID, afterAt, afterID, limit)
}
func (s *InteractionService) ResolveAuthorizedImage(ctx context.Context, viewer domain.User, imageID uuid.UUID) (string, error) {
	postID, key, err := s.repository.RealMojiImage(ctx, imageID)
	if err != nil {
		return "", err
	}
	allowed, err := s.visibility.CanViewPost(ctx, viewer.ID, postID)
	if err != nil || !allowed {
		return "", ErrInteractionNotAllowed
	}
	return key, nil
}
